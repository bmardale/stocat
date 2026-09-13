import { sha256 } from "@noble/hashes/sha2.js";
import { fromBase64, type LibraryKeys, toBase64 } from "./library-crypto";

export const fileEncryptionFormat = "stocat-framed-v1";
export const defaultFrameSize = 8 * 1024 * 1024;

const headerSize = 32;
const tagSize = 16;
const nonceSize = 12;
const wrappedKeySize = 1 + nonceSize + 32 + tagSize;
const magic = new TextEncoder().encode("STOCAT01");

export type EncryptedFileMetadata = {
  ciphertextSha256: string;
  dedupFingerprint: string;
};

export type EncryptedFile = {
  size: number;
  encryptionFormat: typeof fileEncryptionFormat;
  encryptedFileKey: string;
  stream: ReadableStream<Uint8Array>;
  metadata: Promise<EncryptedFileMetadata>;
};

export type DecryptedFile = {
  size: number;
  stream: ReadableStream<Uint8Array>;
};

export async function encryptFile(
  source: Blob,
  keys: LibraryKeys,
  frameSize = defaultFrameSize,
): Promise<EncryptedFile> {
  validateFrameSize(frameSize);
  const frameCount = Math.max(1, Math.ceil(source.size / frameSize));
  if (frameCount > 0xffff_ffff) {
    throw new Error("The file has too many encryption frames.");
  }
  const fileKeyBytes = crypto.getRandomValues(new Uint8Array(32));
  const fileKey = await crypto.subtle.importKey("raw", fileKeyBytes, "AES-GCM", false, [
    "encrypt",
    "decrypt",
  ]);
  const noncePrefix = crypto.getRandomValues(new Uint8Array(8));
  const header = makeHeader(source.size, frameSize, noncePrefix);
  const encryptedFileKey = await wrapFileKey(keys.files, fileKeyBytes);
  let resolveMetadata: (value: EncryptedFileMetadata) => void;
  let rejectMetadata: (reason?: unknown) => void;
  const metadata = new Promise<EncryptedFileMetadata>((resolve, reject) => {
    resolveMetadata = resolve;
    rejectMetadata = reject;
  });
  const plaintextHash = sha256.create();
  const ciphertextHash = sha256.create();
  let frameIndex = 0;
  let headerSent = false;
  let finished = false;
  const stream = new ReadableStream<Uint8Array>({
    async pull(controller) {
      if (finished) return;
      try {
        if (!headerSent) {
          headerSent = true;
          controller.enqueue(header);
          ciphertextHash.update(header);
          return;
        }
        const start = frameIndex * frameSize;
        const plaintext = new Uint8Array(
          await source.slice(start, Math.min(source.size, start + frameSize)).arrayBuffer(),
        );
        plaintextHash.update(plaintext);
        const ciphertext = new Uint8Array(
          await crypto.subtle.encrypt(
            {
              name: "AES-GCM",
              iv: frameNonce(noncePrefix, frameIndex),
              additionalData: frameAdditionalData(header, frameIndex, plaintext.length),
            },
            fileKey,
            plaintext,
          ),
        );
        ciphertextHash.update(ciphertext);
        controller.enqueue(ciphertext);
        frameIndex += 1;
        if (frameIndex === frameCount) {
          finished = true;
          const fingerprint = new Uint8Array(
            await crypto.subtle.sign("HMAC", keys.deduplication, plaintextHash.digest()),
          );
          resolveMetadata!({
            ciphertextSha256: toBase64(ciphertextHash.digest()),
            dedupFingerprint: toBase64(fingerprint),
          });
          controller.close();
        }
      } catch (error) {
        finished = true;
        rejectMetadata!(error);
        controller.error(error);
      }
    },
  });
  return {
    size: encryptedSize(source.size, frameSize),
    encryptionFormat: fileEncryptionFormat,
    encryptedFileKey,
    stream,
    metadata,
  };
}

export async function decryptFileStream(
  source: ReadableStream<Uint8Array>,
  keys: LibraryKeys,
  encryptedFileKey: string,
  storedSize?: number,
): Promise<DecryptedFile> {
  const bytes = new StreamBytes(source);
  const header = await bytes.read(headerSize);
  const parsed = parseHeader(header);
  if (
    storedSize !== undefined &&
    storedSize !== encryptedSize(parsed.plaintextSize, parsed.frameSize)
  ) {
    throw new Error("The encrypted file size is invalid.");
  }
  const fileKeyBytes = await unwrapFileKey(keys.files, encryptedFileKey);
  const fileKey = await crypto.subtle.importKey("raw", fileKeyBytes, "AES-GCM", false, ["decrypt"]);
  const frameCount = Math.max(1, Math.ceil(parsed.plaintextSize / parsed.frameSize));
  let frameIndex = 0;
  const stream = new ReadableStream<Uint8Array>({
    async pull(controller) {
      if (frameIndex === frameCount) {
        try {
          if (await bytes.hasMore()) throw new Error("The encrypted file has trailing data.");
          controller.close();
        } catch (error) {
          controller.error(error);
        }
        return;
      }
      try {
        const plaintextLength = Math.min(
          parsed.frameSize,
          Math.max(0, parsed.plaintextSize - frameIndex * parsed.frameSize),
        );
        const ciphertextLength = plaintextLength + tagSize;
        const ciphertext = await bytes.read(ciphertextLength);
        const plaintext = await crypto.subtle.decrypt(
          {
            name: "AES-GCM",
            iv: frameNonce(parsed.noncePrefix, frameIndex),
            additionalData: frameAdditionalData(header, frameIndex, plaintextLength),
          },
          fileKey,
          ciphertext,
        );
        controller.enqueue(new Uint8Array(plaintext));
        frameIndex += 1;
      } catch (error) {
        controller.error(error);
      }
    },
    cancel(reason) {
      return bytes.cancel(reason);
    },
  });
  return { size: parsed.plaintextSize, stream };
}

// Network chunks are small and a frame is large. A chunk queue copies each byte once.
class StreamBytes {
  private readonly reader: ReadableStreamDefaultReader<Uint8Array>;
  private readonly chunks: Uint8Array[] = [];
  private buffered = 0;
  private done = false;

  constructor(stream: ReadableStream<Uint8Array>) {
    this.reader = stream.getReader();
  }

  async read(length: number) {
    while (this.buffered < length && !this.done) await this.pull();
    if (this.buffered < length) throw new Error("The encrypted file is incomplete.");
    const result = new Uint8Array(length);
    let offset = 0;
    while (offset < length) {
      const chunk = this.chunks.shift();
      if (!chunk) throw new Error("The encrypted file is incomplete.");
      const count = Math.min(chunk.byteLength, length - offset);
      result.set(chunk.subarray(0, count), offset);
      offset += count;
      if (count < chunk.byteLength) this.chunks.unshift(chunk.subarray(count));
    }
    this.buffered -= length;
    return result;
  }

  async hasMore() {
    while (this.buffered === 0 && !this.done) await this.pull();
    return this.buffered > 0;
  }

  cancel(reason?: unknown) {
    return this.reader.cancel(reason);
  }

  private async pull() {
    const next = await this.reader.read();
    if (next.done) {
      this.done = true;
      return;
    }
    if (next.value.byteLength > 0) {
      this.chunks.push(next.value);
      this.buffered += next.value.byteLength;
    }
  }
}

export function encryptedSize(plaintextSize: number, frameSize = defaultFrameSize) {
  validateSafeSize(plaintextSize);
  validateFrameSize(frameSize);
  return headerSize + plaintextSize + Math.max(1, Math.ceil(plaintextSize / frameSize)) * tagSize;
}

async function wrapFileKey(wrappingKey: CryptoKey, fileKey: Uint8Array<ArrayBuffer>) {
  const version = Uint8Array.of(1);
  const iv = crypto.getRandomValues(new Uint8Array(nonceSize));
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: "AES-GCM", iv, additionalData: version },
      wrappingKey,
      fileKey,
    ),
  );
  const result = new Uint8Array(wrappedKeySize);
  result.set(version);
  result.set(iv, 1);
  result.set(ciphertext, 1 + nonceSize);
  return toBase64(result);
}

async function unwrapFileKey(wrappingKey: CryptoKey, encoded: string) {
  const wrapped = fromBase64(encoded);
  if (wrapped.length !== wrappedKeySize || wrapped[0] !== 1) {
    throw new Error("The encrypted file key uses an unsupported format.");
  }
  return crypto.subtle.decrypt(
    { name: "AES-GCM", iv: wrapped.slice(1, 1 + nonceSize), additionalData: wrapped.slice(0, 1) },
    wrappingKey,
    wrapped.slice(1 + nonceSize),
  );
}

function makeHeader(plaintextSize: number, frameSize: number, noncePrefix: Uint8Array) {
  validateSafeSize(plaintextSize);
  const header = new Uint8Array(headerSize);
  header.set(magic);
  const view = new DataView(header.buffer);
  view.setUint8(8, 1);
  view.setUint32(12, frameSize);
  view.setBigUint64(16, BigInt(plaintextSize));
  header.set(noncePrefix, 24);
  return header;
}

function parseHeader(header: Uint8Array) {
  if (header.length !== headerSize || !magic.every((value, index) => header[index] === value)) {
    throw new Error("The file header is invalid.");
  }
  const view = new DataView(header.buffer, header.byteOffset, header.byteLength);
  if (view.getUint8(8) !== 1 || header.slice(9, 12).some((value) => value !== 0)) {
    throw new Error("The file encryption format is unsupported.");
  }
  const plaintextSize = Number(view.getBigUint64(16));
  const frameSize = view.getUint32(12);
  validateSafeSize(plaintextSize);
  validateFrameSize(frameSize);
  return { plaintextSize, frameSize, noncePrefix: header.slice(24, 32) };
}

function frameNonce(prefix: Uint8Array, index: number) {
  const nonce = new Uint8Array(nonceSize);
  nonce.set(prefix);
  new DataView(nonce.buffer).setUint32(8, index);
  return nonce;
}

function frameAdditionalData(header: Uint8Array, index: number, plaintextLength: number) {
  const data = new Uint8Array(headerSize + 8);
  data.set(header);
  const view = new DataView(data.buffer);
  view.setUint32(headerSize, index);
  view.setUint32(headerSize + 4, plaintextLength);
  return data;
}

function validateFrameSize(frameSize: number) {
  if (!Number.isSafeInteger(frameSize) || frameSize < 64 * 1024 || frameSize > 64 * 1024 * 1024) {
    throw new Error("Use an encryption frame size from 64 KiB through 64 MiB.");
  }
}

function validateSafeSize(size: number) {
  if (!Number.isSafeInteger(size) || size < 0) {
    throw new Error("The file size is invalid.");
  }
}
