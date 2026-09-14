import sodium from "libsodium-wrappers";
import { hkdf } from "@noble/hashes/hkdf.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import type {
  Bundle,
  Identity,
  InitializeInputBody,
  SignedRecord,
  UpdateInputBody,
} from "@/api/generated/model";
import { derivePasswordKey, passwordSaltLength, validateEncryptionPassword } from "./password-kdf";
import {
  argon2Profile,
  bytesField,
  counterField,
  encodeRecord,
  equalBytes,
  fromBase64Url,
  parseRecord,
  recordAssociatedData,
  type RecordType,
  signatureInput,
  stringField,
  toBase64Url,
  Tuple,
  TupleReader,
  type WireRecord,
} from "./records";

const keyLength = 32;
const nonceLength = 24;
const zeroSalt = new Uint8Array(32);
const checkpointPrefix = "stocat:encryption-checkpoint:";

export class IncorrectSecretError extends Error {
  constructor() {
    super("The encryption password or recovery key is incorrect.");
    this.name = "IncorrectSecretError";
  }
}

export class InvalidBundleError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidBundleError";
  }
}

export type RecipientKey = { generation: bigint; privateKey: Uint8Array };

export type UnlockedAccount = {
  accountId: string;
  deployment: Uint8Array;
  generation: bigint;
  masterKey: Uint8Array;
  signingSeed: Uint8Array;
  signingPublicKey: Uint8Array;
  signingPrivateKey: Uint8Array;
  recipientPublicKey: Uint8Array;
  recipientPrivateKey: Uint8Array;
  previousRecipientKeys: RecipientKey[];
  identityRecord: Uint8Array;
  fingerprint: string;
};

export type RecoveryKey = { key: string; checksum: string };

type AccountContext = { accountId: string; deployment: Uint8Array; generation: bigint };

type BundleContext = AccountContext & {
  revision: bigint;
  identityRecord: Uint8Array;
  privateEnvelope: Envelope;
};

type Envelope = { data: Uint8Array; record: WireRecord };

export function bundleETag(bundle: Bundle) {
  return `"${bundle.generation}.${bundle.bundle_revision}"`;
}

export function recipientKeyId(
  deployment: Uint8Array,
  accountId: string,
  generation: bigint,
  recipientPublicKey: Uint8Array,
) {
  return sha256(
    new Tuple()
      .string("stocat/v2/recipient-key-id")
      .bytes(deployment)
      .string(accountId)
      .counter(generation)
      .bytes(recipientPublicKey)
      .finish(),
  );
}

export function identityFingerprint(identityRecord: Uint8Array) {
  return bytesToHex(sha256(identityRecord)).toUpperCase().match(/.{4}/g)?.join(" ") ?? "";
}

export function recoveryChecksum(secret: Uint8Array) {
  const hash = sha256(new Tuple().string("stocat/v2/recovery-checksum").bytes(secret).finish());
  return bytesToHex(hash.slice(0, 4))
    .toUpperCase()
    .replace(/^(.{4})/, "$1-");
}

export function parseRecoveryKey(value: string) {
  try {
    return fromBase64Url(value.replace(/\s+/g, ""), keyLength);
  } catch {
    throw new IncorrectSecretError();
  }
}

export async function createAccountEncryption(bundle: Bundle, password: string) {
  await sodium.ready;
  validateEncryptionPassword(password);
  if (bundle.state !== "absent") {
    throw new InvalidBundleError("Account encryption is already set up.");
  }
  const account = buildAccount({
    accountId: bundle.account_id,
    deployment: fromBase64Url(bundle.deployment_id, 16),
    generation: 1n,
    masterKey: random(keyLength),
    signingSeed: random(keyLength),
    recipientPrivateKey: random(keyLength),
    previousRecipientKeys: [],
  });
  const recovery = random(keyLength);
  const request: InitializeInputBody = {
    identity: signRecord(account.identityRecord, account),
    password_envelope: await passwordEnvelope(account, 1n, password),
    recovery_envelope: recoveryEnvelope(account, 1n, recovery),
    private_envelope: privateEnvelope(account, 1n),
  };
  return { request, account, recoveryKey: formatRecoveryKey(recovery) };
}

export async function unlockWithPassword(bundle: Bundle, password: string) {
  await sodium.ready;
  const context = bundleContext(bundle);
  const envelope = accountEnvelope(context, bundle.password_envelope, "account-password");
  if (stringField(envelope.record, "profile") !== argon2Profile) {
    throw new InvalidBundleError("The password envelope uses an unsupported profile.");
  }
  const key = await derivePasswordKey(password, bytesField(envelope.record, "salt"));
  return openAccount(context, openEnvelope(envelope, key));
}

export async function unlockWithRecoveryKey(bundle: Bundle, recoveryKey: string) {
  await sodium.ready;
  const context = bundleContext(bundle);
  const envelope = accountEnvelope(context, bundle.recovery_envelope, "account-recovery");
  const key = deriveKey(parseRecoveryKey(recoveryKey), accountWrapInfo(context, "recovery"));
  return openAccount(context, openEnvelope(envelope, key));
}

// Every change also replaces the private envelope. Its authenticated revision then matches the bundle revision.
export async function changeEncryptionPassword(
  account: UnlockedAccount,
  bundle: Bundle,
  password: string,
): Promise<UpdateInputBody> {
  await sodium.ready;
  const revision = nextRevision(account, bundle);
  return {
    password_envelope: await passwordEnvelope(account, revision, password),
    private_envelope: privateEnvelope(account, revision),
  };
}

export async function replaceRecoveryKey(account: UnlockedAccount, bundle: Bundle) {
  await sodium.ready;
  const revision = nextRevision(account, bundle);
  const recovery = random(keyLength);
  const request: UpdateInputBody = {
    recovery_envelope: recoveryEnvelope(account, revision, recovery),
    private_envelope: privateEnvelope(account, revision),
  };
  return { request, recoveryKey: formatRecoveryKey(recovery) };
}

export function recordCheckpoint(account: UnlockedAccount, bundle: Bundle) {
  storeCheckpoint(account.accountId, account.generation, BigInt(bundle.bundle_revision ?? "0"));
}

// Browsers keep their own copies of key material, so erasure is best effort.
export function lockAccount(account: UnlockedAccount) {
  for (const key of [
    account.masterKey,
    account.signingSeed,
    account.signingPrivateKey,
    account.recipientPrivateKey,
    ...account.previousRecipientKeys.map((entry) => entry.privateKey),
  ]) {
    key.fill(0);
  }
}

function buildAccount(
  input: AccountContext & {
    masterKey: Uint8Array;
    signingSeed: Uint8Array;
    recipientPrivateKey: Uint8Array;
    previousRecipientKeys: RecipientKey[];
  },
): UnlockedAccount {
  const signing = sodium.crypto_sign_seed_keypair(input.signingSeed);
  const recipientPublicKey = sodium.crypto_scalarmult_base(input.recipientPrivateKey);
  const identityRecord = encodeRecord({
    type: "identity",
    deployment: input.deployment,
    fields: {
      account_id: input.accountId,
      generation: input.generation,
      signing_public_key: signing.publicKey,
      recipient_public_key: recipientPublicKey,
      recipient_key_id: recipientKeyId(
        input.deployment,
        input.accountId,
        input.generation,
        recipientPublicKey,
      ),
    },
  });
  return {
    ...input,
    signingPublicKey: signing.publicKey,
    signingPrivateKey: signing.privateKey,
    recipientPublicKey,
    identityRecord,
    fingerprint: identityFingerprint(identityRecord),
  };
}

async function passwordEnvelope(account: UnlockedAccount, revision: bigint, password: string) {
  const salt = random(passwordSaltLength);
  const key = await derivePasswordKey(password, salt);
  const fields = { ...accountFields(account, revision), profile: argon2Profile, salt };
  return sealAndSign("account-password", account, fields, key, account.masterKey);
}

function recoveryEnvelope(account: UnlockedAccount, revision: bigint, recovery: Uint8Array) {
  const key = deriveKey(recovery, accountWrapInfo(account, "recovery"));
  return sealAndSign(
    "account-recovery",
    account,
    accountFields(account, revision),
    key,
    account.masterKey,
  );
}

function privateEnvelope(account: UnlockedAccount, revision: bigint) {
  const key = deriveKey(account.masterKey, accountWrapInfo(account, "private"));
  const plaintext = encodePrivateBundle(account);
  return sealAndSign("account-private", account, accountFields(account, revision), key, plaintext);
}

function accountFields(account: UnlockedAccount, revision: bigint) {
  return {
    account_id: account.accountId,
    generation: account.generation,
    bundle_revision: revision,
  };
}

function sealAndSign(
  type: RecordType,
  account: UnlockedAccount,
  fields: WireRecord["fields"],
  key: Uint8Array,
  plaintext: Uint8Array,
): SignedRecord {
  const nonce = random(nonceLength);
  const deployment = account.deployment;
  const draft = encodeRecord({
    type,
    deployment,
    fields: { ...fields, nonce, ciphertext: new Uint8Array(plaintext.length + 16) },
  });
  const ciphertext = sodium.crypto_aead_xchacha20poly1305_ietf_encrypt(
    plaintext,
    recordAssociatedData(draft),
    null,
    nonce,
    key,
  );
  return signRecord(
    encodeRecord({ type, deployment, fields: { ...fields, nonce, ciphertext } }),
    account,
  );
}

function signRecord(data: Uint8Array, account: UnlockedAccount): SignedRecord {
  const signature = sodium.crypto_sign_detached(signatureInput(data), account.signingPrivateKey);
  return { record: toBase64Url(data), signature: toBase64Url(signature) };
}

function bundleContext(bundle: Bundle): BundleContext {
  if (bundle.state !== "configured" || !bundle.generation || !bundle.bundle_revision) {
    throw new InvalidBundleError("Account encryption is not set up.");
  }
  const account: AccountContext = {
    accountId: bundle.account_id,
    deployment: fromBase64Url(bundle.deployment_id, 16),
    generation: BigInt(bundle.generation),
  };
  const current = bundle.identities.at(-1);
  if (!current || BigInt(current.generation) !== account.generation) {
    throw new InvalidBundleError("The account identity is missing.");
  }
  const identityRecord = fromBase64Url(current.record);
  verifyIdentity(current, account, identityRecord);
  const revision = BigInt(bundle.bundle_revision);
  const privateEnvelope = accountEnvelope(
    { ...account, revision },
    bundle.private_envelope,
    "account-private",
  );
  if (counterField(privateEnvelope.record, "bundle_revision") !== revision) {
    throw new InvalidBundleError(
      "The private key envelope does not use the current bundle revision.",
    );
  }
  const checkpoint = readCheckpoint(account.accountId);
  if (
    checkpoint &&
    (account.generation < checkpoint.generation ||
      (account.generation === checkpoint.generation && revision < checkpoint.revision))
  ) {
    throw new InvalidBundleError(
      "The server returned older encryption keys than this browser already used.",
    );
  }
  return { ...account, revision, identityRecord, privateEnvelope };
}

function verifyIdentity(identity: Identity, account: AccountContext, data: Uint8Array) {
  const record = parseRecord(data);
  if (
    record.type !== "identity" ||
    !equalBytes(record.deployment, account.deployment) ||
    stringField(record, "account_id") !== account.accountId ||
    counterField(record, "generation") !== account.generation
  ) {
    throw new InvalidBundleError("The account identity is not valid.");
  }
  const recipientKey = bytesField(record, "recipient_public_key");
  const expectedKeyId = recipientKeyId(
    account.deployment,
    account.accountId,
    account.generation,
    recipientKey,
  );
  const signature = fromBase64Url(identity.signature, 64);
  if (
    !equalBytes(bytesField(record, "recipient_key_id"), expectedKeyId) ||
    !sodium.crypto_sign_verify_detached(
      signature,
      signatureInput(data),
      bytesField(record, "signing_public_key"),
    )
  ) {
    throw new InvalidBundleError("The account identity is not valid.");
  }
}

function accountEnvelope(
  context: AccountContext & { revision: bigint },
  value: string | undefined,
  type: RecordType,
): Envelope {
  if (!value) {
    throw new InvalidBundleError("The encryption bundle is incomplete.");
  }
  const data = fromBase64Url(value);
  const record = parseRecord(data);
  if (
    record.type !== type ||
    !equalBytes(record.deployment, context.deployment) ||
    stringField(record, "account_id") !== context.accountId ||
    counterField(record, "generation") !== context.generation ||
    counterField(record, "bundle_revision") > context.revision
  ) {
    throw new InvalidBundleError("An encryption envelope does not match the account.");
  }
  return { data, record };
}

function openEnvelope(envelope: Envelope, key: Uint8Array) {
  try {
    return sodium.crypto_aead_xchacha20poly1305_ietf_decrypt(
      null,
      bytesField(envelope.record, "ciphertext"),
      recordAssociatedData(envelope.data),
      bytesField(envelope.record, "nonce"),
      key,
    );
  } catch {
    throw new IncorrectSecretError();
  }
}

function openAccount(context: BundleContext, masterKey: Uint8Array) {
  let plaintext: Uint8Array;
  try {
    plaintext = openEnvelope(
      context.privateEnvelope,
      deriveKey(masterKey, accountWrapInfo(context, "private")),
    );
  } catch {
    throw new InvalidBundleError("The private key envelope does not match the account master key.");
  }
  const secrets = decodePrivateBundle(plaintext);
  if (secrets.generation !== context.generation) {
    throw new InvalidBundleError("The private keys belong to another identity generation.");
  }
  const account = buildAccount({
    accountId: context.accountId,
    deployment: context.deployment,
    generation: context.generation,
    masterKey,
    signingSeed: secrets.signingSeed,
    recipientPrivateKey: secrets.recipientPrivateKey,
    previousRecipientKeys: secrets.previousRecipientKeys,
  });
  if (!equalBytes(account.identityRecord, context.identityRecord)) {
    throw new InvalidBundleError("The private keys do not match the account identity.");
  }
  storeCheckpoint(context.accountId, context.generation, context.revision);
  return account;
}

function encodePrivateBundle(account: UnlockedAccount) {
  const tuple = new Tuple()
    .string("stocat/v2/private-bundle")
    .counter(account.generation)
    .bytes(account.signingSeed)
    .bytes(account.recipientPrivateKey)
    .uint32(account.previousRecipientKeys.length);
  for (const entry of account.previousRecipientKeys) {
    tuple.counter(entry.generation).bytes(entry.privateKey);
  }
  return tuple.finish();
}

function decodePrivateBundle(plaintext: Uint8Array) {
  const reader = new TupleReader(plaintext);
  if (reader.string(64) !== "stocat/v2/private-bundle") {
    throw new InvalidBundleError("The private key bundle uses an unknown format.");
  }
  const generation = reader.counter();
  const signingSeed = exactKey(reader.bytes(keyLength));
  const recipientPrivateKey = exactKey(reader.bytes(keyLength));
  const count = reader.uint32();
  const previousRecipientKeys: RecipientKey[] = [];
  for (let index = 0; index < count; index++) {
    previousRecipientKeys.push({
      generation: reader.counter(),
      privateKey: exactKey(reader.bytes(keyLength)),
    });
  }
  if (reader.remaining !== 0) {
    throw new InvalidBundleError("The private key bundle has trailing bytes.");
  }
  return { generation, signingSeed, recipientPrivateKey, previousRecipientKeys };
}

function exactKey(value: Uint8Array) {
  if (value.length !== keyLength) {
    throw new InvalidBundleError("A private key has an invalid length.");
  }
  return value;
}

function nextRevision(account: UnlockedAccount, bundle: Bundle) {
  if (bundle.state !== "configured" || BigInt(bundle.generation ?? "0") !== account.generation) {
    throw new InvalidBundleError("The encryption bundle changed. Unlock encryption again.");
  }
  return BigInt(bundle.bundle_revision ?? "0") + 1n;
}

function accountWrapInfo(context: AccountContext, purpose: string) {
  return new Tuple()
    .string("stocat/v2/account-wrap")
    .bytes(context.deployment)
    .string(context.accountId)
    .counter(context.generation)
    .string(purpose)
    .finish();
}

function deriveKey(input: Uint8Array, info: Uint8Array) {
  return hkdf(sha256, input, zeroSalt, info, keyLength);
}

function formatRecoveryKey(secret: Uint8Array): RecoveryKey {
  return { key: toBase64Url(secret), checksum: recoveryChecksum(secret) };
}

function random(length: number) {
  return sodium.randombytes_buf(length);
}

function readCheckpoint(accountId: string) {
  const match = localStorage
    .getItem(checkpointPrefix + accountId)
    ?.match(/^([1-9][0-9]*)\.([1-9][0-9]*)$/);
  return match ? { generation: BigInt(match[1]), revision: BigInt(match[2]) } : undefined;
}

// A checkpoint contains only public counters. It never contains key material.
function storeCheckpoint(accountId: string, generation: bigint, revision: bigint) {
  const current = readCheckpoint(accountId);
  if (
    current &&
    (current.generation > generation ||
      (current.generation === generation && current.revision >= revision))
  ) {
    return;
  }
  localStorage.setItem(checkpointPrefix + accountId, `${generation}.${revision}`);
}
