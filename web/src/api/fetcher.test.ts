import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { ApiError, apiFetch } from "./fetcher";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubFetch(response: Response) {
  const fetch = vi.fn(async () => response);
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

describe("apiFetch", () => {
  it("adds the API prefix and returns the JSON body", async () => {
    const fetch = stubFetch(Response.json({ status: "ok" }));
    await expect(apiFetch("/healthz", { method: "GET" })).resolves.toEqual({ status: "ok" });
    expect(fetch).toHaveBeenCalledWith("/api/healthz", { method: "GET" });
  });

  it("returns undefined for an empty body", async () => {
    stubFetch(new Response(null, { status: 204 }));
    await expect(apiFetch("/auth/logout")).resolves.toBeUndefined();
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
      message: "Request failed with status 502",
    },
  ])("throws ApiError for $name", async ({ response, status, message }) => {
    stubFetch(response);
    const error = await apiFetch("/auth/me").catch((error: unknown) => error);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status, message });
  });
});
