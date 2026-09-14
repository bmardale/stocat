import { argon2Key } from "./password-kdf";

self.onmessage = async (event: MessageEvent<{ password: string; salt: Uint8Array }>) => {
  try {
    self.postMessage({ key: await argon2Key(event.data.password, event.data.salt) });
  } catch (error) {
    self.postMessage({
      error: error instanceof Error ? error.message : "The password key derivation failed.",
    });
  }
};
