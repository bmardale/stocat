import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Bundle, InitializeInputBody, UpdateInputBody } from "@/api/generated/model";
import { createAccountEncryption } from "@/lib/v2/account-crypto";
import { jsonResponse, renderApp, stubApi, testUser } from "@/test/app";

const accountId = "usr_01K4W9T5V8QK3M7ZB0YHXC2FNE";
const user = { ...testUser, id: accountId };
const password = "correct horse battery staple";
const slow = { timeout: 30_000 };
const absent: Bundle = {
  state: "absent",
  deployment_id: "AAECAwQFBgcICQoLDA0ODw",
  account_id: accountId,
  identities: [],
};

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function configured(request: InitializeInputBody): Bundle {
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

describe("encryption settings", { timeout: 90_000 }, () => {
  it("sets up encryption after the account password confirmation", async () => {
    let attempts = 0;
    let stored: Bundle | undefined;
    let confirmation: unknown;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, user),
      "GET /api/v2/encryption/bundle": () => jsonResponse(200, stored ?? absent),
      "POST /api/v2/encryption/bundle": (init) => {
        attempts++;
        if (attempts === 1) {
          return jsonResponse(403, { status: 403, detail: "Confirm your password to continue." });
        }
        stored = configured(JSON.parse(init?.body as string));
        return jsonResponse(201, stored);
      },
      "POST /api/v1/auth/reauthenticate": (init) => {
        confirmation = JSON.parse(init?.body as string);
        return jsonResponse(204);
      },
    });
    await renderApp("/settings/encryption");
    fill("Encryption password", "a separate encryption password");
    fill("Confirm encryption password", "a separate encryption password");
    fireEvent.click(screen.getByRole("button", { name: "Create keys" }));
    const recoveryKey = await screen.findByLabelText("Recovery key", {}, slow);

    fill("Enter the recovery key", "a wrong recovery key");
    fireEvent.click(screen.getByRole("button", { name: "Finish setup" }));
    expect(await screen.findByText("The recovery key does not match.")).toBeDefined();
    fill("Enter the recovery key", ` ${recoveryKey.textContent} `);
    fireEvent.click(screen.getByRole("button", { name: "Finish setup" }));
    fireEvent.change(await screen.findByLabelText("Account password"), {
      target: { value: password },
    });
    fireEvent.click(screen.getByRole("button", { name: "Confirm password" }));

    expect(await screen.findByText("Encryption is set up.", {}, slow)).toBeDefined();
    expect(screen.getByLabelText("Identity fingerprint").textContent).toMatch(
      /^([0-9A-F]{4} ){15}[0-9A-F]{4}$/,
    );
    expect(confirmation).toEqual({ password });
    expect(attempts).toBe(2);
  });

  it("unlocks encryption and changes the encryption password", async () => {
    const setup = await createAccountEncryption(absent, password);
    const bundle = configured(setup.request);
    let ifMatch: string | null = null;
    let update: UpdateInputBody | undefined;
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, user),
      "GET /api/v2/encryption/bundle": () => jsonResponse(200, bundle),
      "PUT /api/v2/encryption/bundle": (init) => {
        ifMatch = new Headers(init?.headers).get("If-Match");
        update = JSON.parse(init?.body as string) as UpdateInputBody;
        return jsonResponse(200, {
          ...bundle,
          bundle_revision: "2",
          password_envelope: update.password_envelope?.record,
          private_envelope: update.private_envelope?.record,
        });
      },
    });
    await renderApp("/settings/encryption");
    fill("Encryption password", "short");
    fireEvent.click(screen.getByRole("button", { name: "Unlock" }));
    expect(
      await screen.findByText("The encryption password or recovery key is incorrect.", {}, slow),
    ).toBeDefined();
    fill("Encryption password", password);
    fireEvent.click(screen.getByRole("button", { name: "Unlock" }));
    expect(await screen.findByText(setup.account.fingerprint, {}, slow)).toBeDefined();

    fill("New encryption password", "a different encryption password");
    fill("Confirm new encryption password", "a different encryption password");
    fireEvent.click(screen.getByRole("button", { name: "Change encryption password" }));
    expect(await screen.findByText("Encryption password changed.", {}, slow)).toBeDefined();
    expect(ifMatch).toBe('"1.1"');
    expect(update?.password_envelope).toBeDefined();
    expect(update?.private_envelope).toBeDefined();

    fireEvent.click(screen.getByRole("button", { name: "Lock encryption" }));
    expect(await screen.findByRole("button", { name: "Unlock" })).toBeDefined();
  });
});
