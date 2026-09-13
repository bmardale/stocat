import {
  authPasskeyCreate,
  authPasskeyLogin,
  authPasskeyLoginOptions,
  authPasskeyRegistrationOptions,
} from "@/api/generated/auth/auth";

export function passkeysSupported() {
  return (
    typeof PublicKeyCredential !== "undefined" &&
    typeof PublicKeyCredential.parseRequestOptionsFromJSON === "function" &&
    typeof navigator.credentials?.get === "function"
  );
}

export async function signInWithPasskey() {
  const options = await authPasskeyLoginOptions();
  const credential = await browserCeremony(() =>
    navigator.credentials.get({
      publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(
        options as PublicKeyCredentialRequestOptionsJSON,
      ),
    }),
  );
  return authPasskeyLogin(credential.toJSON());
}

export async function registerPasskey({ password, name }: { password: string; name: string }) {
  const options = await authPasskeyRegistrationOptions({ password });
  const credential = await browserCeremony(() =>
    navigator.credentials.create({
      publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(
        options as PublicKeyCredentialCreationOptionsJSON,
      ),
    }),
  );
  return authPasskeyCreate({ name, credential: credential.toJSON() });
}

async function browserCeremony(run: () => Promise<Credential | null>) {
  let credential: Credential | null;
  try {
    credential = await run();
  } catch (error) {
    throw new Error(ceremonyMessage(error), { cause: error });
  }
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error("The browser did not return a passkey.");
  }
  return credential;
}

function ceremonyMessage(error: unknown) {
  if (error instanceof DOMException && error.name === "NotAllowedError") {
    return "The passkey request was cancelled or timed out.";
  }
  if (error instanceof DOMException && error.name === "InvalidStateError") {
    return "This authenticator already has a passkey for your account.";
  }
  return "The browser could not use a passkey.";
}
