// Format of a key envelope, all integers big-endian:
//   version (1) | PBKDF2 iterations (4) | salt (16) | IV (12) | AES-GCM ciphertext of the library key (48)
// The version, iterations, and salt are the additional authenticated data.
// Format of an encrypted name: version (1) | IV (12) | AES-GCM ciphertext. The version is the additional data.
// A name token is HMAC-SHA-256 over the parent node ID, a NUL byte, and the NFC form of the name.
// HKDF-SHA-256 derives separate name encryption and name token keys from the library key.

const formatVersion = 1;
const kdfIterations = 600_000;
const saltLength = 16;
const ivLength = 12;
const libraryKeyLength = 32;
const envelopeHeaderLength = 1 + 4 + saltLength;
const encoder = new TextEncoder();
const decoder = new TextDecoder("utf-8", { fatal: true });

export type LibraryKeys = { names: CryptoKey; tokens: CryptoKey };

export class IncorrectPassphraseError extends Error {
  constructor() {
    super("The passphrase is incorrect.");
    this.name = "IncorrectPassphraseError";
  }
}

export function toBase64(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

export function fromBase64(value: string) {
  return Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
}

function randomBytes(length: number) {
  return crypto.getRandomValues(new Uint8Array(length));
}

async function passphraseKey(
  passphrase: string,
  salt: Uint8Array<ArrayBuffer>,
  iterations: number,
) {
  const material = await crypto.subtle.importKey(
    "raw",
    encoder.encode(passphrase),
    "PBKDF2",
    false,
    ["deriveKey"],
  );
  return crypto.subtle.deriveKey(
    { name: "PBKDF2", hash: "SHA-256", salt, iterations },
    material,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"],
  );
}

async function deriveLibraryKeys(libraryKey: Uint8Array<ArrayBuffer>): Promise<LibraryKeys> {
  const material = await crypto.subtle.importKey("raw", libraryKey, "HKDF", false, ["deriveKey"]);
  const hkdf = (info: string) => ({
    name: "HKDF",
    hash: "SHA-256",
    salt: new Uint8Array(),
    info: encoder.encode(info),
  });
  const [names, tokens] = await Promise.all([
    crypto.subtle.deriveKey(
      hkdf("stocat/v1/name-encryption"),
      material,
      { name: "AES-GCM", length: 256 },
      false,
      ["encrypt", "decrypt"],
    ),
    crypto.subtle.deriveKey(
      hkdf("stocat/v1/name-token"),
      material,
      { name: "HMAC", hash: "SHA-256", length: 256 },
      false,
      ["sign"],
    ),
  ]);
  return { names, tokens };
}

export async function createKeyEnvelope(passphrase: string) {
  const libraryKey = randomBytes(libraryKeyLength);
  const salt = randomBytes(saltLength);
  const iv = randomBytes(ivLength);
  const header = new Uint8Array(envelopeHeaderLength);
  const view = new DataView(header.buffer);
  view.setUint8(0, formatVersion);
  view.setUint32(1, kdfIterations);
  header.set(salt, 5);
  const wrappingKey = await passphraseKey(passphrase, salt, kdfIterations);
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv, additionalData: header },
      wrappingKey,
      libraryKey,
    ),
  );
  const envelope = new Uint8Array(header.length + iv.length + ciphertext.length);
  envelope.set(header);
  envelope.set(iv, header.length);
  envelope.set(ciphertext, header.length + iv.length);
  return { envelope: toBase64(envelope), keys: await deriveLibraryKeys(libraryKey) };
}

export async function openKeyEnvelope(envelope: string, passphrase: string) {
  const bytes = fromBase64(envelope);
  const header = bytes.slice(0, envelopeHeaderLength);
  const view = new DataView(header.buffer);
  if (header.length !== envelopeHeaderLength || view.getUint8(0) !== formatVersion) {
    throw new Error("The library key uses an unsupported format.");
  }
  const salt = header.slice(5);
  const iv = bytes.slice(envelopeHeaderLength, envelopeHeaderLength + ivLength);
  const ciphertext = bytes.slice(envelopeHeaderLength + ivLength);
  const wrappingKey = await passphraseKey(passphrase, salt, view.getUint32(1));
  let libraryKey: ArrayBuffer;
  try {
    libraryKey = await crypto.subtle.decrypt(
      { name: "AES-GCM", iv, additionalData: header },
      wrappingKey,
      ciphertext,
    );
  } catch {
    throw new IncorrectPassphraseError();
  }
  return deriveLibraryKeys(new Uint8Array(libraryKey));
}

export async function encryptName(keys: LibraryKeys, name: string) {
  const header = Uint8Array.of(formatVersion);
  const iv = randomBytes(ivLength);
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv, additionalData: header },
      keys.names,
      encoder.encode(name),
    ),
  );
  const encrypted = new Uint8Array(1 + ivLength + ciphertext.length);
  encrypted.set(header);
  encrypted.set(iv, 1);
  encrypted.set(ciphertext, 1 + ivLength);
  return toBase64(encrypted);
}

export async function decryptName(keys: LibraryKeys, encrypted: string) {
  const bytes = fromBase64(encrypted);
  const header = bytes.slice(0, 1);
  if (header[0] !== formatVersion) {
    throw new Error("The name uses an unsupported format.");
  }
  const plaintext = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: bytes.slice(1, 1 + ivLength), additionalData: header },
    keys.names,
    bytes.slice(1 + ivLength),
  );
  return decoder.decode(plaintext);
}

export async function nameToken(keys: LibraryKeys, parentId: string, name: string) {
  const signature = await crypto.subtle.sign(
    "HMAC",
    keys.tokens,
    encoder.encode(`${parentId}\0${name.normalize("NFC")}`),
  );
  return toBase64(new Uint8Array(signature));
}
