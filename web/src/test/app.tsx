import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { vi } from "vite-plus/test";
import type { User } from "@/api/generated/model";
import { createAppRouter } from "@/router";

export const testUser: User = {
  public_id: "usr_test",
  name: "Ada Lovelace",
  email: "ada@example.com",
  is_admin: false,
  created_at: "2026-01-01T00:00:00Z",
};

export function jsonResponse(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export const unauthorized = () =>
  jsonResponse(401, { title: "Unauthorized", status: 401, detail: "Sign in to continue." });

type Handler = (init: RequestInit | undefined) => Response;

// Keys use the form "METHOD /api/path". An unknown request fails the test.
export function stubApi(handlers: Record<string, Handler>) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${url}`;
    const handler = handlers[key];
    if (!handler) {
      throw new Error(`Unexpected request: ${key}`);
    }
    return handler(init);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

export async function renderApp(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }));
  await router.load();
  render(<RouterProvider router={router} />);
  return router;
}
