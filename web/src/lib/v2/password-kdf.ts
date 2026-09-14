import { argon2id } from "hash-wasm";

export const passwordSaltLength = 16;

export class PasswordRequirementError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PasswordRequirementError";
  }
}

// Count code points and do not normalize, so every device derives the same key.
export function validateEncryptionPassword(password: string) {
  if (Array.from(password).length < 15) {
    throw new PasswordRequirementError("Use at least 15 characters.");
  }
  if (new TextEncoder().encode(password).length > 1024) {
    throw new PasswordRequirementError("Use at most 1024 bytes.");
  }
}

export function argon2Key(password: string, salt: Uint8Array) {
  return argon2id({
    password: new TextEncoder().encode(password),
    salt,
    parallelism: 4,
    iterations: 3,
    memorySize: 65536,
    hashLength: 32,
    outputType: "binary",
  });
}

type WorkerResult = { key?: Uint8Array; error?: string };

// A worker keeps the page responsive during the memory-hard derivation.
export async function derivePasswordKey(password: string, salt: Uint8Array) {
  validateEncryptionPassword(password);
  if (salt.length !== passwordSaltLength) {
    throw new Error("The password salt must contain 16 bytes.");
  }
  if (typeof Worker === "undefined") {
    return argon2Key(password, salt);
  }
  const worker = new Worker(new URL("./password-kdf.worker.ts", import.meta.url), {
    type: "module",
  });
  try {
    return await new Promise<Uint8Array>((resolve, reject) => {
      worker.onmessage = (event: MessageEvent<WorkerResult>) => {
        if (event.data.key) resolve(event.data.key);
        else reject(new Error(event.data.error ?? "The password key derivation failed."));
      };
      worker.onerror = () => reject(new Error("The password key derivation failed."));
      worker.postMessage({ password, salt });
    });
  } finally {
    worker.terminate();
  }
}
