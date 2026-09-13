import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { jsonResponse, renderApp, stubApi, unauthorized } from "@/test/app";

describe("routing", () => {
  it("renders the home route", async () => {
    stubApi({ "GET /api/auth/me": unauthorized });
    await renderApp("/");
    expect(
      await screen.findByRole("heading", { name: "A little home for your files." }),
    ).toBeDefined();
  });

  it("returns home from an unknown route", async () => {
    stubApi({ "GET /api/auth/me": unauthorized });
    const router = await renderApp("/missing");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeDefined();
    expect(screen.getAllByRole("banner")).toHaveLength(1);
    fireEvent.click(screen.getByRole("link", { name: "Go home" }));
    expect(
      await screen.findByRole("heading", { name: "A little home for your files." }),
    ).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
  });

  it("retries after the current user request fails", async () => {
    let available = false;
    stubApi({
      "GET /api/auth/me": () =>
        available ? unauthorized() : jsonResponse(502, { detail: "The server is unavailable." }),
    });
    await renderApp("/");
    expect(await screen.findByText("The server is unavailable.")).toBeDefined();
    available = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByRole("heading", { name: "A little home for your files." }),
    ).toBeDefined();
  });
});
