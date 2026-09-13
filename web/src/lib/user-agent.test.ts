import { describe, expect, it } from "vite-plus/test";
import { parseUserAgent } from "./user-agent";

describe("parseUserAgent", () => {
  it.each([
    [
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
      { browser: "Chrome 140", os: "macOS", device: "desktop", label: "Chrome 140 on macOS" },
    ],
    [
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:143.0) Gecko/20100101 Firefox/143.0",
      { browser: "Firefox 143", os: "Windows", device: "desktop", label: "Firefox 143 on Windows" },
    ],
    [
      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
      { browser: "Safari 18", os: "iOS", device: "mobile", label: "Safari 18 on iOS" },
    ],
    [
      "Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
      { browser: "Safari 18", os: "iOS", device: "tablet", label: "Safari 18 on iOS" },
    ],
    [
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0",
      {
        browser: "Microsoft Edge 140",
        os: "Windows",
        device: "desktop",
        label: "Microsoft Edge 140 on Windows",
      },
    ],
  ])("parses %s", (userAgent, expected) => {
    expect(parseUserAgent(userAgent)).toEqual(expected);
  });

  it("uses the raw value when it cannot identify the client", () => {
    expect(parseUserAgent("stocat-cli")).toEqual({ device: "unknown", label: "stocat-cli" });
  });

  it("names an empty user agent", () => {
    expect(parseUserAgent(" ")).toEqual({ device: "unknown", label: "Unknown device" });
  });
});
