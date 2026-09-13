import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { ApiError, NetworkError, NO_RESPONSE, apiFetch } from "./fetcher";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubFetch(response: Response) {
  const fetch = vi.fn(async () => response);
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function stubRejection(cause: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      throw cause;
    }),
  );
}

describe("apiFetch", () => {
  it("adds the API prefix and returns the JSON body", async () => {
    const fetch = stubFetch(Response.json({ status: "ok" }));
    await expect(apiFetch("/healthz", { method: "GET" })).resolves.toEqual({ status: "ok" });
    expect(fetch).toHaveBeenCalledWith("/healthz", { method: "GET" });
  });

  it("returns undefined for an empty body", async () => {
    stubFetch(new Response(null, { status: 204 }));
    await expect(apiFetch("/api/v1/auth/logout")).resolves.toBeUndefined();
  });

  it.each([
    {
      name: "problem details",
      response: Response.json({ status: 401, title: "Unauthorized" }, { status: 401 }),
      status: 401,
      message: "Unauthorized",
    },
    {
      name: "an empty body",
      response: new Response(null, { status: 502 }),
      status: 502,
      message: "Unknown error. Status 502.",
    },
  ])("throws ApiError for $name", async ({ response, status, message }) => {
    stubFetch(response);
    const error = await apiFetch("/api/v1/auth/me").catch((error: unknown) => error);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status, message });
  });

  it("throws NetworkError when the request never reaches the server", async () => {
    vi.stubGlobal("navigator", { onLine: false });
    stubRejection(new TypeError("Failed to fetch"));
    const error = await apiFetch("/api/v1/auth/me").catch((error: unknown) => error);
    expect(error).toBeInstanceOf(NetworkError);
    expect(error).toMatchObject({
      name: "NetworkError",
      status: NO_RESPONSE,
      message: "Your connection dropped. Check your connection and try again.",
    });
  });

  it("rethrows an aborted request", async () => {
    const controller = new AbortController();
    controller.abort();
    stubRejection(new DOMException("The operation was aborted.", "AbortError"));
    const error = await apiFetch("/api/v1/auth/me", { signal: controller.signal }).catch(
      (error: unknown) => error,
    );
    expect(error).toBeInstanceOf(DOMException);
  });

  it("rethrows an error that is not a network failure", async () => {
    stubRejection(new Error("Unexpected request: GET /api/v1/auth/me"));
    await expect(apiFetch("/api/v1/auth/me")).rejects.toThrow("Unexpected request");
  });

  it("rethrows a TypeError while the browser is online", async () => {
    vi.stubGlobal("navigator", { onLine: true });
    stubRejection(new TypeError("Invalid URL"));
    await expect(apiFetch("/api/v1/auth/me")).rejects.toThrow("Invalid URL");
  });
});
