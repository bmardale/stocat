import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vite-plus/test";
import { jsonResponse, renderApp, stubApi, testUser, unauthorized } from "@/test/app";

describe("routing", () => {
  it("renders the home route for a signed-in user", async () => {
    stubApi({ "GET /api/v1/auth/me": () => jsonResponse(200, testUser) });
    await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Home" })).toBeDefined();
  });

  it("returns home from an unknown route", async () => {
    stubApi({ "GET /api/v1/auth/me": () => jsonResponse(200, testUser) });
    const router = await renderApp("/missing");
    expect(await screen.findByRole("heading", { name: "Nothing here. Wild." })).toBeDefined();
    expect(screen.getByText("404")).toBeDefined();
    expect(screen.getAllByRole("banner")).toHaveLength(1);
    fireEvent.click(screen.getByRole("link", { name: "Go home" }));
    expect(await screen.findByRole("heading", { name: "Home" })).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
  });

  it("retries after the current user request fails", async () => {
    let available = false;
    stubApi({
      "GET /api/v1/auth/me": () =>
        available ? unauthorized() : jsonResponse(502, { detail: "The server is unavailable." }),
    });
    await renderApp("/");
    expect(
      await screen.findByRole("heading", { name: "The server is ghosting you." }),
    ).toBeDefined();
    expect(screen.getByText("502")).toBeDefined();
    expect(screen.getByText("The server is unavailable.")).toBeDefined();
    available = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeDefined();
  });

  it("reports a 500 as a failed feature", async () => {
    stubApi({
      "GET /api/v1/auth/me": () => jsonResponse(500, { detail: "Authentication is unavailable." }),
    });
    await renderApp("/");
    expect(
      await screen.findByRole("heading", { name: "It is not a bug. It is a feature that failed." }),
    ).toBeDefined();
    expect(screen.getByText("Authentication is unavailable.")).toBeDefined();
  });

  it("reports a rate limit without an apology", async () => {
    stubApi({ "GET /api/v1/auth/me": () => jsonResponse(429, { detail: "Too many requests." }) });
    await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Your enthusiasm is noted." })).toBeDefined();
    expect(screen.getByText("And rate limited. Wait a moment.")).toBeDefined();
  });

  it("reports a forbidden request", async () => {
    stubApi({
      "GET /api/v1/auth/me": () =>
        jsonResponse(403, { detail: "Only administrators can do this." }),
    });
    await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Admins only." })).toBeDefined();
  });

  it("reports an unknown status", async () => {
    stubApi({ "GET /api/v1/auth/me": () => jsonResponse(418) });
    await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Unknown error." })).toBeDefined();
    expect(screen.getByText("Status 418. Your guess is as good as ours.")).toBeDefined();
  });

  it("reports a connection that never reached the server", async () => {
    vi.stubGlobal("navigator", { onLine: false });
    stubApi({
      "GET /api/v1/auth/me": () => {
        throw new TypeError("Failed to fetch");
      },
    });
    await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Your connection dropped." })).toBeDefined();
    expect(screen.getByText("offline")).toBeDefined();
  });
});
