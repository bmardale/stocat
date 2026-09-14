import { readFileSync } from "node:fs";
import path from "node:path";
import sodium from "libsodium-wrappers";
import { bytesToHex, hexToBytes } from "@noble/hashes/utils.js";
import { describe, expect, it } from "vite-plus/test";
import { recipientKeyId } from "./account-crypto";
import {
  encodeRecord,
  fromBase64Url,
  parseRecord,
  recordAssociatedData,
  RecordFormatError,
  recordSchema,
  signatureInput,
  toBase64Url,
  type WireRecord,
} from "./records";

type Fixture = {
  record: { type: string; deployment_id: string; fields: Record<string, string> };
  hex: string;
  signature_input_hex: string;
  aad_hex: string | null;
  signature_hex: string;
  signing_public_key_hex: string;
};

// The Go package and this module share these fixtures.
const fixtures: Fixture[] = JSON.parse(
  readFileSync(
    path.resolve(import.meta.dirname, "../../../../internal/encryptionv2/testdata/records.json"),
    "utf8",
  ),
);

function wireRecord(fixture: Fixture): WireRecord {
  const fields: WireRecord["fields"] = {};
  for (const field of recordSchema(fixture.record.type)) {
    const value = fixture.record.fields[field.name];
    if (value === undefined) continue;
    if (field.kind === "counter") fields[field.name] = BigInt(value);
    else if (["binary", "header", "ciphertext"].includes(field.kind)) {
      fields[field.name] = fromBase64Url(value);
    } else fields[field.name] = value;
  }
  return {
    type: fixture.record.type as WireRecord["type"],
    deployment: fromBase64Url(fixture.record.deployment_id, 16),
    fields,
  };
}

describe("v2 records", () => {
  it("matches the Go fixtures", async () => {
    await sodium.ready;
    for (const fixture of fixtures) {
      const encoded = encodeRecord(wireRecord(fixture));
      expect(bytesToHex(encoded), fixture.record.type).toBe(fixture.hex);
      expect(encodeRecord(parseRecord(encoded))).toEqual(encoded);
      expect(bytesToHex(signatureInput(encoded))).toBe(fixture.signature_input_hex);
      if (fixture.aad_hex !== null) {
        expect(bytesToHex(recordAssociatedData(encoded))).toBe(fixture.aad_hex);
      }
      expect(
        sodium.crypto_sign_verify_detached(
          hexToBytes(fixture.signature_hex),
          signatureInput(encoded),
          hexToBytes(fixture.signing_public_key_hex),
        ),
      ).toBe(true);
    }
  });

  it("derives the recipient key identifier of the identity fixture", () => {
    const identity = fixtures.find((fixture) => fixture.record.type === "identity");
    if (!identity) throw new Error("The fixtures contain no identity record.");
    const record = wireRecord(identity);
    const expected = recipientKeyId(
      record.deployment,
      identity.record.fields.account_id,
      BigInt(identity.record.fields.generation),
      fromBase64Url(identity.record.fields.recipient_public_key, 32),
    );
    expect(toBase64Url(expected)).toBe(identity.record.fields.recipient_key_id);
  });

  it("rejects noncanonical records", () => {
    const encoded = hexToBytes(fixtures[0].hex);
    const unknownSuite = encoded.slice();
    unknownSuite[4 + "stocat/v2/identity".length + 4 + 16] = 2;
    for (const invalid of [Uint8Array.of(...encoded, 0), encoded.slice(0, -1), unknownSuite]) {
      expect(() => parseRecord(invalid)).toThrow(RecordFormatError);
    }
    expect(() => fromBase64Url(`${fixtures[0].record.deployment_id}=`)).toThrow(RecordFormatError);
  });
});
