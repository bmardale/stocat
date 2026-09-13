import { createRouter } from "@tanstack/react-router";
import type { RouterHistory } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

export function createAppRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    defaultPreload: "intent",
    scrollRestoration: true,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
