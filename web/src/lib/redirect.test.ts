import { describe, expect, it } from "vite-plus/test";
import { safeRedirectPath } from "./redirect";

describe("safeRedirectPath", () => {
  it.each([
    { value: "/settings", expected: "/settings" },
    { value: "/settings?tab=files#top", expected: "/settings?tab=files#top" },
    { value: "/", expected: "/" },
    { value: "//evil.example", expected: undefined },
    { value: "/\\evil.example", expected: undefined },
    { value: "https://evil.example", expected: undefined },
    { value: "settings", expected: undefined },
    { value: "", expected: undefined },
    { value: 42, expected: undefined },
    { value: undefined, expected: undefined },
  ])("returns $expected for $value", ({ value, expected }) => {
    expect(safeRedirectPath(value)).toBe(expected);
  });
});
