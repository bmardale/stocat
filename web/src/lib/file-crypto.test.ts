import { describe, expect, it } from "vite-plus/test";
import { sha256 } from "@noble/hashes/sha2.js";
import { createKeyEnvelope, fromBase64, toBase64 } from "./library-crypto";
import { decryptFileStream, encryptedSize, encryptFile, fileEncryptionFormat } from "./file-crypto";

async function readStream(stream: ReadableStream<Uint8Array>) {
  const chunks: Uint8Array[] = [];
  for await (const chunk of stream) chunks.push(chunk);
  return new Uint8Array(
    await new Blob(chunks.map((chunk) => Uint8Array.from(chunk))).arrayBuffer(),
  );
}

// jsdom does not implement Blob.stream.
function streamOf(...parts: Uint8Array[]) {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const part of parts) controller.enqueue(part);
      controller.close();
    },
  });
}

describe("file crypto", () => {
  it("encrypts and decrypts independent authenticated frames", async () => {
    const { keys } = await createKeyEnvelope("correct horse battery staple");
    const plaintext = new TextEncoder().encode("A framed secret. ".repeat(20_000));
    const encrypted = await encryptFile(new Blob([plaintext]), keys, 64 * 1024);
    const ciphertext = await readStream(encrypted.stream);
    const metadata = await encrypted.metadata;
    expect(encrypted.encryptionFormat).toBe(fileEncryptionFormat);
    expect(ciphertext).toHaveLength(encryptedSize(plaintext.length, 64 * 1024));
    expect(fromBase64(encrypted.encryptedFileKey)).toHaveLength(61);
    expect(fromBase64(metadata.ciphertextSha256)).toHaveLength(32);
    expect(metadata.ciphertextSha256).toBe(toBase64(sha256(ciphertext)));
    expect(fromBase64(metadata.dedupFingerprint)).toHaveLength(32);
    const decrypted = (
      await decryptFileStream(
        streamOf(ciphertext),
        keys,
        encrypted.encryptedFileKey,
        ciphertext.length,
      )
    ).stream;
    expect(toBase64(await readStream(decrypted))).toBe(toBase64(plaintext));
  });

  it("uses a stable library-scoped fingerprint and randomized ciphertext", async () => {
    const { keys } = await createKeyEnvelope("passphrase");
    const source = new Blob(["same content"]);
    const first = await encryptFile(source, keys, 64 * 1024);
    const firstBytes = await readStream(first.stream);
    const second = await encryptFile(source, keys, 64 * 1024);
    const secondBytes = await readStream(second.stream);
    expect(firstBytes).not.toEqual(secondBytes);
    expect((await first.metadata).dedupFingerprint).toBe((await second.metadata).dedupFingerprint);
  });

  it("rejects a modified frame", async () => {
    const { keys } = await createKeyEnvelope("passphrase");
    const encrypted = await encryptFile(new Blob(["secret"]), keys, 64 * 1024);
    const ciphertext = await readStream(encrypted.stream);
    ciphertext[ciphertext.length - 1] ^= 1;
    const decrypted = (
      await decryptFileStream(
        streamOf(ciphertext),
        keys,
        encrypted.encryptedFileKey,
        ciphertext.length,
      )
    ).stream;
    await expect(readStream(decrypted)).rejects.toThrow();
  });

  it("authenticates an empty file", async () => {
    const { keys } = await createKeyEnvelope("passphrase");
    const encrypted = await encryptFile(new Blob(), keys, 64 * 1024);
    const ciphertext = await readStream(encrypted.stream);
    expect(ciphertext).toHaveLength(48);
    const decrypted = (
      await decryptFileStream(
        streamOf(ciphertext),
        keys,
        encrypted.encryptedFileKey,
        ciphertext.length,
      )
    ).stream;
    expect(await readStream(decrypted)).toHaveLength(0);
  });

  it("decrypts a response stream that arrives in small chunks", async () => {
    const { keys } = await createKeyEnvelope("passphrase");
    const plaintext = new TextEncoder().encode("A chunked secret. ".repeat(10_000));
    const encrypted = await encryptFile(new Blob([plaintext]), keys, 64 * 1024);
    const ciphertext = await readStream(encrypted.stream);
    let offset = 0;
    const source = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (offset >= ciphertext.length) {
          controller.close();
          return;
        }
        controller.enqueue(ciphertext.slice(offset, offset + 1000));
        offset += 1000;
      },
    });
    const decrypted = await decryptFileStream(
      source,
      keys,
      encrypted.encryptedFileKey,
      ciphertext.length,
    );
    expect(decrypted.size).toBe(plaintext.length);
    expect(toBase64(await readStream(decrypted.stream))).toBe(toBase64(plaintext));
  });

  it("rejects data after the last frame", async () => {
    const { keys } = await createKeyEnvelope("passphrase");
    const encrypted = await encryptFile(new Blob(["secret"]), keys, 64 * 1024);
    const ciphertext = await readStream(encrypted.stream);
    const source = streamOf(ciphertext, new Uint8Array([0]));
    const decrypted = await decryptFileStream(source, keys, encrypted.encryptedFileKey);
    await expect(readStream(decrypted.stream)).rejects.toThrow("trailing data");
  });
});
