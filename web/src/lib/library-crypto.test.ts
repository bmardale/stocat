import { describe, expect, it } from "vite-plus/test";
import {
  createKeyEnvelope,
  decryptName,
  encryptName,
  fromBase64,
  IncorrectPassphraseError,
  nameToken,
  openKeyEnvelope,
  toBase64,
} from "./library-crypto";

describe("library crypto", () => {
  it("opens an envelope only with the passphrase that created it", async () => {
    const { envelope, keys } = await createKeyEnvelope("correct horse battery staple");
    expect(fromBase64(envelope)).toHaveLength(81);
    const opened = await openKeyEnvelope(envelope, "correct horse battery staple");
    expect(await decryptName(opened, await encryptName(keys, "Tax returns"))).toBe("Tax returns");
    await expect(openKeyEnvelope(envelope, "wrong passphrase")).rejects.toBeInstanceOf(
      IncorrectPassphraseError,
    );
  });

  it("encrypts the same name to different ciphertexts", async () => {
    const { keys } = await createKeyEnvelope("passphrase");
    const first = await encryptName(keys, "Photos");
    expect(await encryptName(keys, "Photos")).not.toBe(first);
    expect(await decryptName(keys, first)).toBe("Photos");
  });

  it("computes a 32-byte name token that depends on the parent and the normalized name", async () => {
    const { envelope, keys } = await createKeyEnvelope("passphrase");
    const token = await nameToken(keys, "nod_parent", "Café");
    expect(fromBase64(token)).toHaveLength(32);
    const opened = await openKeyEnvelope(envelope, "passphrase");
    expect(await nameToken(opened, "nod_parent", "Café")).toBe(token);
    expect(await nameToken(keys, "nod_other", "Café")).not.toBe(token);
    expect(await nameToken(keys, "nod_parent", "café")).not.toBe(token);
  });

  it("converts bytes to standard base64", () => {
    const bytes = Uint8Array.of(0, 255, 62, 63);
    expect(toBase64(bytes)).toBe("AP8+Pw==");
    expect(fromBase64("AP8+Pw==")).toEqual(bytes);
  });
});
