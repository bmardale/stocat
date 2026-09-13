import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import type { Passkey } from "@/api/generated/model";
import {
  jsonResponse,
  noLibraries,
  renderApp,
  serverVersion,
  stubApi,
  testUser,
  unauthorized,
} from "@/test/app";

class FakePublicKeyCredential {
  static parseRequestOptionsFromJSON(options: unknown) {
    return { parsed: options };
  }

  static parseCreationOptionsFromJSON(options: unknown) {
    return { parsed: options };
  }

  toJSON() {
    return { id: "credential" };
  }
}

const credentials = {
  get: vi.fn<() => Promise<unknown>>(),
  create: vi.fn<() => Promise<unknown>>(),
};

const laptop: Passkey = {
  id: "pky_laptop",
  name: "Laptop",
  created_at: "2026-09-01T08:30:00Z",
  last_used_at: "2026-09-13T15:02:58Z",
  synced: true,
};

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function stubPasskeySupport() {
  vi.stubGlobal("PublicKeyCredential", FakePublicKeyCredential);
  Object.defineProperty(navigator, "credentials", { configurable: true, value: credentials });
}

beforeEach(() => {
  credentials.get.mockReset();
  credentials.create.mockReset();
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "credentials");
});

describe("passkey sign-in", () => {
  it("signs in with a passkey and opens the redirect path", async () => {
    stubPasskeySupport();
    credentials.get.mockResolvedValue(new FakePublicKeyCredential());
    let body: unknown;
    stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "GET /api/v1/libraries": noLibraries,
      "POST /api/v1/auth/passkeys/login/options": () => jsonResponse(200, { challenge: "abc" }),
      "POST /api/v1/auth/passkeys/login": (init) => {
        body = JSON.parse(init?.body as string);
        return jsonResponse(200, testUser);
      },
    });
    const router = await renderApp("/login?redirect=%2F");
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with a passkey" }));
    expect(await screen.findByRole("button", { name: /Ada Lovelace/ })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
    expect(credentials.get).toHaveBeenCalledWith({ publicKey: { parsed: { challenge: "abc" } } });
    expect(body).toEqual({ id: "credential" });
  });

  it("shows a message when the user cancels the passkey request", async () => {
    stubPasskeySupport();
    credentials.get.mockRejectedValue(new DOMException("Cancelled", "NotAllowedError"));
    stubApi({
      "GET /api/v1/auth/me": unauthorized,
      "POST /api/v1/auth/passkeys/login/options": () => jsonResponse(200, { challenge: "abc" }),
    });
    await renderApp("/login");
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with a passkey" }));
    expect(
      await screen.findByText("The passkey request was cancelled or timed out."),
    ).toBeDefined();
  });

  it("hides passkey sign-in when the browser does not support passkeys", async () => {
    stubApi({ "GET /api/v1/auth/me": unauthorized });
    await renderApp("/login");
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Sign in with a passkey" })).toBeNull();
  });
});

describe("passkey settings", () => {
  function stubPasskeys(initial: Passkey[]) {
    let passkeys = initial;
    const requests: Record<string, unknown> = {};
    const record = (key: string, init: RequestInit | undefined) => {
      requests[key] = JSON.parse(init?.body as string);
    };
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(200, testUser),
      "GET /api/v1/version": serverVersion,
      "GET /api/v1/auth/passkeys": () => jsonResponse(200, passkeys),
      "POST /api/v1/auth/passkeys/registration/options": (init) => {
        record("options", init);
        return jsonResponse(200, { challenge: "abc" });
      },
      "POST /api/v1/auth/passkeys": (init) => {
        record("create", init);
        const created = { ...laptop, id: "pky_work", name: "Work laptop", last_used_at: null };
        passkeys = [created, ...passkeys];
        return jsonResponse(201, created);
      },
      "PATCH /api/v1/auth/passkeys/pky_laptop": (init) => {
        record("rename", init);
        passkeys = passkeys.map((passkey) =>
          passkey.id === "pky_laptop" ? { ...passkey, name: "Phone" } : passkey,
        );
        return jsonResponse(200, passkeys[0]);
      },
      "DELETE /api/v1/auth/passkeys/pky_laptop": () => {
        passkeys = passkeys.filter((passkey) => passkey.id !== "pky_laptop");
        return jsonResponse(204);
      },
    });
    return requests;
  }

  it("opens passkeys from the settings navigation", async () => {
    stubPasskeys([laptop]);
    const router = await renderApp("/settings");
    fireEvent.click(await screen.findByRole("link", { name: "Passkeys" }));
    expect(await screen.findByRole("heading", { name: "Passkeys" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/settings/passkeys");
    const item = within(screen.getByRole("list", { name: "Passkeys" })).getByRole("listitem");
    expect(within(item).getByText("Laptop")).toBeDefined();
    expect(within(item).getByText("Synced")).toBeDefined();
  });

  it("adds a passkey after the password confirmation", async () => {
    stubPasskeySupport();
    credentials.create.mockResolvedValue(new FakePublicKeyCredential());
    const requests = stubPasskeys([]);
    await renderApp("/settings/passkeys");
    expect(await screen.findByText("You have no passkeys.")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Add passkey" }));
    const dialog = await screen.findByRole("dialog");
    fill("Passkey name", "  Work laptop  ");
    fill("Current password", "correct horse battery staple");
    fireEvent.click(within(dialog).getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("Passkey added.")).toBeDefined();
    expect(await screen.findByText("Work laptop")).toBeDefined();
    expect(requests.options).toEqual({ password: "correct horse battery staple" });
    expect(requests.create).toEqual({ name: "Work laptop", credential: { id: "credential" } });
    expect(credentials.create).toHaveBeenCalledWith({
      publicKey: { parsed: { challenge: "abc" } },
    });
  });

  it("disables adding a passkey when the browser does not support passkeys", async () => {
    stubPasskeys([]);
    await renderApp("/settings/passkeys");
    expect(await screen.findByText("This browser does not support passkeys.")).toBeDefined();
    expect(screen.getByRole("button", { name: "Add passkey" }).hasAttribute("disabled")).toBe(true);
  });

  it("renames and deletes a passkey", async () => {
    const requests = stubPasskeys([laptop]);
    await renderApp("/settings/passkeys");
    fireEvent.click(await screen.findByRole("button", { name: "Rename Laptop" }));
    const dialog = await screen.findByRole("dialog");
    fill("Passkey name", " Phone ");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Passkey renamed.")).toBeDefined();
    expect(requests.rename).toEqual({ name: "Phone" });
    fireEvent.click(await screen.findByRole("button", { name: "Delete Phone" }));
    expect(await screen.findByText("Passkey deleted.")).toBeDefined();
    await waitFor(() => expect(screen.getByText("You have no passkeys.")).toBeDefined());
  });
});
