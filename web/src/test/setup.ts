import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vite-plus/test";

beforeEach(() => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  // jsdom does not implement matchMedia.
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
