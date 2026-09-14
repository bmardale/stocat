// Canonical v2 records. docs/specs/encrypted-sharing-records.md defines the format.
// internal/encryptionv2 implements the same schemas. Shared fixtures keep both implementations equal.
import { sha256 } from "@noble/hashes/sha2.js";
import { concatBytes, utf8ToBytes } from "@noble/hashes/utils.js";

export const suite = 1;
export const argon2Profile = "argon2id-v19-m65536-t3-p4-l32";
export const maxKeyRecord = 16 * 1024;
export const maxMetadataCiphertext = 64 * 1024;
export const fileHeaderSize = 64;

const maxWireRecord = maxMetadataCiphertext + 2048;
const maxCounter = (1n << 63n) - 1n;
const tagSize = 16;
const decoder = new TextDecoder("utf-8", { fatal: true });

type FieldKind =
  | "counter"
  | "binary"
  | "ciphertext"
  | "profile"
  | "header"
  | "usr"
  | "lib"
  | "nod"
  | "ver";

type FieldSchema = { name: string; kind: FieldKind; size: number; optional?: boolean };

const resource = (name: string, prefix: "usr" | "lib" | "nod" | "ver"): FieldSchema => ({
  name,
  kind: prefix,
  size: prefix.length + 27,
});
const counter = (name: string): FieldSchema => ({ name, kind: "counter", size: 8 });
const binary = (name: string, size: number): FieldSchema => ({ name, kind: "binary", size });
const ciphertext = (size: number): FieldSchema => ({
  name: "ciphertext",
  kind: "ciphertext",
  size,
});
const optional = (field: FieldSchema): FieldSchema => ({ ...field, optional: true });
const nonce = binary("nonce", 24);
const account = resource("account_id", "usr");
const library = resource("library_id", "lib");
const node = resource("node_id", "nod");

const schemas = {
  identity: [
    account,
    counter("generation"),
    binary("signing_public_key", 32),
    binary("recipient_public_key", 32),
    binary("recipient_key_id", 32),
  ],
  "identity-continuity": [
    account,
    counter("previous_generation"),
    counter("generation"),
    binary("previous_identity_hash", 32),
    binary("identity_hash", 32),
  ],
  "contact-store": [
    account,
    counter("generation"),
    counter("revision"),
    optional(binary("previous_revision_hash", 32)),
    nonce,
    ciphertext(maxMetadataCiphertext),
  ],
  "account-password": [
    account,
    counter("generation"),
    counter("bundle_revision"),
    { name: "profile", kind: "profile", size: argon2Profile.length },
    binary("salt", 16),
    nonce,
    binary("ciphertext", 48),
  ],
  "account-recovery": [
    account,
    counter("generation"),
    counter("bundle_revision"),
    nonce,
    binary("ciphertext", 48),
  ],
  "account-private": [
    account,
    counter("generation"),
    counter("bundle_revision"),
    nonce,
    ciphertext(maxKeyRecord),
  ],
  "owner-root": [
    account,
    library,
    node,
    counter("generation"),
    counter("node_epoch"),
    counter("revision"),
    nonce,
    binary("ciphertext", 48),
  ],
  "parent-envelope": [
    account,
    library,
    resource("parent_id", "nod"),
    resource("child_id", "nod"),
    counter("parent_epoch"),
    counter("child_epoch"),
    counter("generation"),
    counter("revision"),
    nonce,
    binary("ciphertext", 48),
  ],
  "node-metadata": [
    account,
    library,
    node,
    counter("node_epoch"),
    counter("revision"),
    nonce,
    ciphertext(maxMetadataCiphertext),
  ],
  "file-key": [
    account,
    library,
    node,
    resource("version_id", "ver"),
    binary("content_id", 16),
    counter("node_epoch"),
    counter("generation"),
    nonce,
    binary("ciphertext", 48),
  ],
  "version-manifest": [
    account,
    library,
    node,
    resource("version_id", "ver"),
    binary("content_id", 16),
    counter("node_epoch"),
    counter("revision"),
    { name: "header", kind: "header", size: fileHeaderSize },
    binary("ciphertext_hash", 32),
    binary("key_envelope_hash", 32),
  ],
  "current-version": [
    account,
    library,
    node,
    counter("node_epoch"),
    counter("revision"),
    optional(resource("version_id", "ver")),
  ],
} satisfies Record<string, FieldSchema[]>;

export type RecordType = keyof typeof schemas;
export type FieldValue = bigint | Uint8Array | string;
export type WireRecord = {
  type: RecordType;
  deployment: Uint8Array;
  fields: Record<string, FieldValue>;
};

export class RecordFormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RecordFormatError";
  }
}

export function recordSchema(type: string): FieldSchema[] {
  if (!Object.hasOwn(schemas, type)) {
    throw new RecordFormatError("The record type is unknown.");
  }
  return schemas[type as RecordType];
}

export class Tuple {
  #parts: Uint8Array[] = [];

  bytes(value: Uint8Array) {
    const size = new Uint8Array(4);
    new DataView(size.buffer).setUint32(0, value.length);
    this.#parts.push(size, value);
    return this;
  }

  string(value: string) {
    return this.bytes(utf8ToBytes(value));
  }

  counter(value: bigint) {
    const encoded = new Uint8Array(8);
    new DataView(encoded.buffer).setBigUint64(0, value);
    this.#parts.push(encoded);
    return this;
  }

  uint32(value: number) {
    const encoded = new Uint8Array(4);
    new DataView(encoded.buffer).setUint32(0, value);
    this.#parts.push(encoded);
    return this;
  }

  byte(value: number) {
    this.#parts.push(Uint8Array.of(value));
    return this;
  }

  finish() {
    return concatBytes(...this.#parts);
  }
}

export class TupleReader {
  readonly #data: Uint8Array;
  readonly #view: DataView;
  #offset = 0;

  constructor(data: Uint8Array) {
    this.#data = data;
    this.#view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  }

  get remaining() {
    return this.#data.length - this.#offset;
  }

  bytes(limit: number) {
    const size = this.uint32();
    if (size > limit || size > this.remaining) {
      throw new RecordFormatError("A field has an invalid length.");
    }
    return this.#take(size);
  }

  string(limit: number) {
    try {
      return decoder.decode(this.bytes(limit));
    } catch (error) {
      if (error instanceof RecordFormatError) throw error;
      throw new RecordFormatError("A field is not valid UTF-8.");
    }
  }

  counter() {
    const value = this.#view.getBigUint64(this.#advance(8));
    return value;
  }

  uint32() {
    return this.#view.getUint32(this.#advance(4));
  }

  byte() {
    return this.#data[this.#advance(1)];
  }

  #take(size: number) {
    const start = this.#advance(size);
    return this.#data.slice(start, start + size);
  }

  #advance(size: number) {
    if (size > this.remaining) {
      throw new RecordFormatError("The record is truncated.");
    }
    const start = this.#offset;
    this.#offset += size;
    return start;
  }
}

export function encodeRecord(record: WireRecord) {
  const schema = recordSchema(record.type);
  if (record.deployment.length !== 16) {
    throw new RecordFormatError("The deployment identifier must contain 16 bytes.");
  }
  const tuple = new Tuple().string(`stocat/v2/${record.type}`).bytes(record.deployment).byte(suite);
  let count = 0;
  for (const field of schema) {
    const present = Object.hasOwn(record.fields, field.name);
    if (field.optional) {
      tuple.byte(present ? 1 : 0);
      if (!present) continue;
    }
    if (!present) {
      throw new RecordFormatError(`The ${field.name} field is missing.`);
    }
    count++;
    encodeField(tuple, field, record.fields[field.name]);
  }
  if (count !== Object.keys(record.fields).length) {
    throw new RecordFormatError("The record has an unknown field.");
  }
  const encoded = tuple.finish();
  const limit =
    record.type === "node-metadata" || record.type === "contact-store"
      ? maxWireRecord
      : maxKeyRecord;
  if (encoded.length > limit) {
    throw new RecordFormatError("The record is too large.");
  }
  validateContext(record);
  return encoded;
}

function encodeField(tuple: Tuple, field: FieldSchema, value: FieldValue) {
  const fail = (message: string) => new RecordFormatError(`The ${field.name} field ${message}.`);
  switch (field.kind) {
    case "counter":
      if (typeof value !== "bigint" || value < 1n || value > maxCounter)
        throw fail("is out of range");
      tuple.counter(value);
      return;
    case "binary":
    case "header":
    case "ciphertext":
      if (!(value instanceof Uint8Array)) throw fail("must contain bytes");
      if (
        field.kind === "ciphertext"
          ? value.length < tagSize || value.length > field.size
          : value.length !== field.size
      ) {
        throw fail("has an invalid length");
      }
      if (field.kind === "header") validateFileHeader(value);
      tuple.bytes(value);
      return;
    case "profile":
      if (value !== argon2Profile) throw fail("uses an unsupported password profile");
      tuple.string(value);
      return;
    default:
      if (typeof value !== "string" || !validIdentifier(field.kind, value))
        throw fail("is not a valid identifier");
      tuple.string(value);
  }
}

export function parseRecord(data: Uint8Array): WireRecord {
  if (data.length > maxWireRecord) {
    throw new RecordFormatError("The record is too large.");
  }
  const reader = new TupleReader(data);
  const domain = reader.string(64);
  if (!domain.startsWith("stocat/v2/")) {
    throw new RecordFormatError("The record type is unknown.");
  }
  const type = domain.slice("stocat/v2/".length);
  const schema = recordSchema(type);
  const deployment = reader.bytes(16);
  if (deployment.length !== 16 || reader.byte() !== suite) {
    throw new RecordFormatError("The record uses an unsupported suite or deployment.");
  }
  const fields: Record<string, FieldValue> = {};
  for (const field of schema) {
    if (field.optional) {
      const flag = reader.byte();
      if (flag > 1) throw new RecordFormatError("An optional-field flag is invalid.");
      if (flag === 0) continue;
    }
    if (field.kind === "counter") {
      fields[field.name] = reader.counter();
    } else if (field.kind === "binary" || field.kind === "header" || field.kind === "ciphertext") {
      fields[field.name] = reader.bytes(field.size);
    } else {
      fields[field.name] = reader.string(field.size);
    }
  }
  if (reader.remaining !== 0) {
    throw new RecordFormatError("The record has trailing bytes.");
  }
  const record: WireRecord = { type: type as RecordType, deployment, fields };
  const canonical = encodeRecord(record);
  if (!equalBytes(canonical, data)) {
    throw new RecordFormatError("The record is not canonical.");
  }
  return record;
}

// The associated data contains every record byte before the final ciphertext length prefix.
export function recordAssociatedData(data: Uint8Array) {
  const record = parseRecord(data);
  const schema = recordSchema(record.type);
  if (schema[schema.length - 1].name !== "ciphertext") {
    throw new RecordFormatError("The record has no ciphertext.");
  }
  const size = bytesField(record, "ciphertext").length;
  return data.slice(0, data.length - 4 - size);
}

export function signatureInput(data: Uint8Array) {
  const record = parseRecord(data);
  return new Tuple().string("stocat/v2/signature").string(record.type).bytes(sha256(data)).finish();
}

export function counterField(record: WireRecord, name: string) {
  const value = record.fields[name];
  if (typeof value !== "bigint") throw new RecordFormatError(`The ${name} field is missing.`);
  return value;
}

export function bytesField(record: WireRecord, name: string) {
  const value = record.fields[name];
  if (!(value instanceof Uint8Array)) throw new RecordFormatError(`The ${name} field is missing.`);
  return value;
}

export function stringField(record: WireRecord, name: string) {
  const value = record.fields[name];
  if (typeof value !== "string") throw new RecordFormatError(`The ${name} field is missing.`);
  return value;
}

export function validateFileHeader(header: Uint8Array) {
  const magic = utf8ToBytes("STOCAT02");
  const invalid = () => new RecordFormatError("The file header is not valid.");
  if (
    header.length !== fileHeaderSize ||
    !equalBytes(header.slice(0, 8), magic) ||
    header[8] !== 2 ||
    header[9] !== suite ||
    header.slice(10, 12).some(Boolean) ||
    header.slice(56, 64).some(Boolean)
  ) {
    throw invalid();
  }
  const view = new DataView(header.buffer, header.byteOffset, header.byteLength);
  const frameSize = view.getUint32(12);
  const plaintextSize = view.getBigUint64(16);
  if (frameSize < 64 * 1024 || frameSize > 8 * 1024 * 1024 || (frameSize & (frameSize - 1)) !== 0) {
    throw invalid();
  }
  const safe = BigInt(Number.MAX_SAFE_INTEGER);
  const frames = plaintextSize === 0n ? 1n : 1n + (plaintextSize - 1n) / BigInt(frameSize);
  if (
    plaintextSize > safe ||
    BigInt(fileHeaderSize) + plaintextSize + BigInt(tagSize) * frames > safe
  ) {
    throw invalid();
  }
}

function validateContext(record: WireRecord) {
  if (record.type === "parent-envelope" && record.fields.parent_id === record.fields.child_id) {
    throw new RecordFormatError("The parent and child identifiers are equal.");
  }
  if (record.type === "version-manifest") {
    const header = bytesField(record, "header");
    if (!equalBytes(header.slice(24, 40), bytesField(record, "content_id"))) {
      throw new RecordFormatError("The manifest content identifier differs from the header.");
    }
  }
}

function validIdentifier(prefix: string, value: string) {
  return new RegExp(`^${prefix}_[0-7][0-9A-HJKMNP-TV-Z]{25}$`).test(value);
}

export function equalBytes(left: Uint8Array, right: Uint8Array) {
  if (left.length !== right.length) return false;
  let difference = 0;
  for (let index = 0; index < left.length; index++) difference |= left[index] ^ right[index];
  return difference === 0;
}

export function toBase64Url(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

export function fromBase64Url(value: string, size?: number) {
  if (!/^[A-Za-z0-9_-]*$/.test(value) || value.length % 4 === 1) {
    throw new RecordFormatError("The value is not canonical base64url.");
  }
  const padded =
    value.replaceAll("-", "+").replaceAll("_", "/") + "=".repeat((4 - (value.length % 4)) % 4);
  const bytes = Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
  if (toBase64Url(bytes) !== value || (size !== undefined && bytes.length !== size)) {
    throw new RecordFormatError("The value is not canonical base64url.");
  }
  return bytes;
}
