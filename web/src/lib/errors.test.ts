import { describe, expect, it } from "vite-plus/test";
import { ApiError, NetworkError } from "@/api/fetcher";
import { pageError } from "./errors";

describe("pageError", () => {
  it.each([
    [403, "Admins only."],
    [404, "Nothing here. Wild."],
    [429, "Your enthusiasm is noted."],
    [500, "It is not a bug. It is a feature that failed."],
    [502, "The server is ghosting you."],
    [503, "The server is ghosting you."],
    [504, "The server is ghosting you."],
  ])("uses the copy for status %d", (status, title) => {
    expect(pageError(new ApiError(status)).title).toBe(title);
  });

  it("keeps the problem detail for the user to report", () => {
    const error = new ApiError(502, { status: 502, detail: "The server is unavailable." });
    expect(pageError(error)).toMatchObject({
      status: "502",
      detail: "The server is unavailable.",
    });
  });

  it("reports a request that never reached the server", () => {
    expect(pageError(new NetworkError())).toMatchObject({
      status: "offline",
      title: "Your connection dropped.",
    });
  });

  it("falls back for an unknown status", () => {
    expect(pageError(new ApiError(418))).toMatchObject({
      title: "Unknown error.",
      description: "Status 418. Your guess is as good as ours.",
    });
  });

  it("falls back for an error that is not an ApiError", () => {
    expect(pageError(new Error("useAuth must be used within an AuthProvider"))).toMatchObject({
      status: "",
      title: "Unknown error.",
      detail: "useAuth must be used within an AuthProvider",
    });
  });

  it("falls back for a thrown value that is not an error", () => {
    expect(pageError("boom")).toMatchObject({ status: "", title: "Unknown error." });
  });
});
