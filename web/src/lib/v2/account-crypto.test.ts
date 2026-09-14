import sodium from "libsodium-wrappers";
import { describe, expect, it } from "vite-plus/test";
import type { Bundle, InitializeInputBody, UpdateInputBody } from "@/api/generated/model";
import {
  changeEncryptionPassword,
  createAccountEncryption,
  IncorrectSecretError,
  InvalidBundleError,
  replaceRecoveryKey,
  unlockWithPassword,
  unlockWithRecoveryKey,
} from "./account-crypto";
import { fromBase64Url, signatureInput, toBase64Url } from "./records";

const password = "correct horse battery staple";
const accountId = "usr_01K4W9T5V8QK3M7ZB0YHXC2FNE";
const absent: Bundle = {
  state: "absent",
  deployment_id: "AAECAwQFBgcICQoLDA0ODw",
  account_id: accountId,
  identities: [],
};

// These helpers store accepted requests in the same way as the server.
function configure(request: InitializeInputBody): Bundle {
  return {
    ...absent,
    state: "configured",
    generation: "1",
    bundle_revision: "1",
    password_envelope: request.password_envelope.record,
    recovery_envelope: request.recovery_envelope.record,
    private_envelope: request.private_envelope.record,
    identities: [
      { generation: "1", record: request.identity.record, signature: request.identity.signature },
    ],
  };
}

function apply(bundle: Bundle, update: UpdateInputBody): Bundle {
  return {
    ...bundle,
    bundle_revision: String(BigInt(bundle.bundle_revision ?? "0") + 1n),
    password_envelope: update.password_envelope?.record ?? bundle.password_envelope,
    recovery_envelope: update.recovery_envelope?.record ?? bundle.recovery_envelope,
    private_envelope: update.private_envelope?.record ?? bundle.private_envelope,
  };
}

describe("account encryption", { timeout: 60_000 }, () => {
  it("creates signed records that unlock with the password and the recovery key", async () => {
    const created = await createAccountEncryption(absent, password);
    await sodium.ready;
    for (const signed of Object.values(created.request)) {
      const data = fromBase64Url(signed.record);
      const signature = fromBase64Url(signed.signature, 64);
      expect(
        sodium.crypto_sign_verify_detached(
          signature,
          signatureInput(data),
          created.account.signingPublicKey,
        ),
      ).toBe(true);
    }
    expect(created.account.fingerprint).toMatch(/^([0-9A-F]{4} ){15}[0-9A-F]{4}$/);
    expect(created.recoveryKey.checksum).toMatch(/^[0-9A-F]{4}-[0-9A-F]{4}$/);

    const bundle = configure(created.request);
    const unlocked = await unlockWithPassword(bundle, password);
    expect(unlocked.masterKey).toEqual(created.account.masterKey);
    expect(unlocked.fingerprint).toBe(created.account.fingerprint);
    const recovered = await unlockWithRecoveryKey(bundle, ` ${created.recoveryKey.key} `);
    expect(recovered.recipientPrivateKey).toEqual(created.account.recipientPrivateKey);

    await expect(unlockWithPassword(bundle, "an incorrect password value")).rejects.toThrow(
      IncorrectSecretError,
    );
    await expect(
      unlockWithRecoveryKey(bundle, toBase64Url(sodium.randombytes_buf(32))),
    ).rejects.toThrow(IncorrectSecretError);
  });

  it("changes the password and replaces the recovery key", async () => {
    const created = await createAccountEncryption(absent, password);
    const first = configure(created.request);
    const account = await unlockWithPassword(first, password);
    const newPassword = "a different encryption password";
    const second = apply(first, await changeEncryptionPassword(account, first, newPassword));
    expect((await unlockWithPassword(second, newPassword)).masterKey).toEqual(account.masterKey);
    await expect(unlockWithPassword(second, password)).rejects.toThrow(IncorrectSecretError);

    const replaced = await replaceRecoveryKey(account, second);
    const third = apply(second, replaced.request);
    expect((await unlockWithRecoveryKey(third, replaced.recoveryKey.key)).masterKey).toEqual(
      account.masterKey,
    );
    await expect(unlockWithRecoveryKey(third, created.recoveryKey.key)).rejects.toThrow(
      IncorrectSecretError,
    );
    expect((await unlockWithPassword(third, newPassword)).generation).toBe(1n);
  });

  it("rejects substituted records and rollback", async () => {
    const created = await createAccountEncryption(absent, password);
    const other = await createAccountEncryption(absent, password);
    const bundle = configure(created.request);

    await expect(
      unlockWithPassword(
        { ...bundle, private_envelope: other.request.private_envelope.record },
        password,
      ),
    ).rejects.toThrow(InvalidBundleError);
    const [identity] = bundle.identities;
    await expect(
      unlockWithPassword(
        { ...bundle, identities: [{ ...identity, signature: other.request.identity.signature }] },
        password,
      ),
    ).rejects.toThrow(InvalidBundleError);

    const account = await unlockWithPassword(bundle, password);
    const changed = apply(
      bundle,
      await changeEncryptionPassword(account, bundle, "a different encryption password"),
    );
    await unlockWithPassword(changed, "a different encryption password");
    await expect(unlockWithPassword(bundle, password)).rejects.toThrow(InvalidBundleError);
    await expect(
      unlockWithPassword({ ...changed, private_envelope: bundle.private_envelope }, password),
    ).rejects.toThrow(InvalidBundleError);
  });
});
