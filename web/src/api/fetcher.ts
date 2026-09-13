import type { Problem } from "./generated/model";

// NO_RESPONSE is the status of a request that never reached the server.
export const NO_RESPONSE = 0;

export class ApiError extends Error {
  readonly status: number;
  readonly problem?: Problem;

  constructor(status: number, problem?: Problem, message?: string, options?: ErrorOptions) {
    super(
      message ?? problem?.detail ?? problem?.title ?? `Unknown error. Status ${status}.`,
      options,
    );
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }
}

export class NetworkError extends ApiError {
  constructor(cause?: unknown) {
    super(NO_RESPONSE, undefined, "Your connection dropped. Check your connection and try again.", {
      cause,
    });
    this.name = "NetworkError";
  }
}

export async function apiFetch<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await send(url, init);
  const body = await readBody(response);
  if (!response.ok) {
    throw new ApiError(response.status, body as Problem | undefined);
  }
  return body as T;
}

// Check the browser's offline signal before classifying a fetch TypeError.
async function send(url: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch (cause) {
    if (cause instanceof TypeError && !init?.signal?.aborted && isOffline()) {
      throw new NetworkError(cause);
    }
    throw cause;
  }
}

function isOffline() {
  return typeof navigator !== "undefined" && navigator.onLine === false;
}

async function readBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export type ErrorType<_Error> = ApiError;
export type BodyType<BodyData> = BodyData;
