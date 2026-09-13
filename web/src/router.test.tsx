import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { createAppRouter } from "./router";

beforeEach(() => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("routing", () => {
  it("renders the home route", async () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ["/"] }));
    await router.load();
    render(<RouterProvider router={router} />);
    expect(
      await screen.findByRole("heading", { name: "A little home for your files." }),
    ).toBeDefined();
  });

  it("returns home from an unknown route", async () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ["/missing"] }));
    await router.load();
    render(<RouterProvider router={router} />);
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeDefined();
    fireEvent.click(screen.getByRole("link", { name: "Go home" }));
    expect(
      await screen.findByRole("heading", { name: "A little home for your files." }),
    ).toBeDefined();
    expect(router.state.location.pathname).toBe("/");
  });
});
