import Bowser from "bowser";

export type DeviceType = "desktop" | "mobile" | "tablet" | "unknown";

export type ParsedUserAgent = {
  browser?: string;
  os?: string;
  device: DeviceType;
  label: string;
};

export function parseUserAgent(userAgent: string): ParsedUserAgent {
  if (!userAgent.trim()) {
    return { device: "unknown", label: "Unknown device" };
  }
  const result = Bowser.parse(userAgent);
  const major = result.browser.version?.split(".")[0];
  const browser = result.browser.name
    ? [result.browser.name, major].filter(Boolean).join(" ")
    : undefined;
  // Browsers freeze the Windows and macOS versions in the user agent, so show only the name.
  const os = result.os.name || undefined;
  const device = result.platform.type;
  return {
    browser,
    os,
    device: device === "desktop" || device === "mobile" || device === "tablet" ? device : "unknown",
    label: browser && os ? `${browser} on ${os}` : (browser ?? os ?? userAgent),
  };
}
