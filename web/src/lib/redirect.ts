// Accept only paths on this origin. This prevents an open redirect after sign-in.
export function safeRedirectPath(value: unknown): string | undefined {
  return typeof value === "string" && /^\/(?![/\\])/.test(value) ? value : undefined;
}
