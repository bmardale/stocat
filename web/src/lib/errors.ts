import { ApiError, NO_RESPONSE } from "@/api/fetcher";

type Copy = {
  title: string;
  description: string;
};

export type PageError = Copy & {
  status: string;
  detail?: string;
};

export const NOT_FOUND_COPY: Copy = {
  title: "Nothing here. Wild.",
  description: "Someone must have moved it. Or never made it.",
};

const SERVER_DOWN: Copy = {
  title: "The server is ghosting you.",
  description: "It read your request. It chose not to answer.",
};

const OFFLINE: Copy = {
  title: "Your connection dropped.",
  description: "It does that sometimes. Probably not your fault. Probably.",
};

const UNKNOWN: Copy = {
  title: "Unknown error.",
  description: "Your guess is as good as ours.",
};

const COPY: Record<number, Copy> = {
  403: { title: "Admins only.", description: "This is not personal. It is permissions." },
  404: NOT_FOUND_COPY,
  429: { title: "Your enthusiasm is noted.", description: "And rate limited. Wait a moment." },
  500: { title: "It is not a bug. It is a feature that failed.", description: "Sorry. Try again." },
  502: SERVER_DOWN,
  503: SERVER_DOWN,
  504: SERVER_DOWN,
};

// pageError turns any thrown value into the copy of the full-page error view.
export function pageError(error: unknown): PageError {
  if (error instanceof ApiError && error.status === NO_RESPONSE) {
    return { status: "offline", ...OFFLINE };
  }

  if (error instanceof ApiError) {
    const copy = COPY[error.status];
    return {
      status: String(error.status),
      title: copy?.title ?? UNKNOWN.title,
      description: copy?.description ?? `Status ${error.status}. ${UNKNOWN.description}`,
      detail: error.problem?.detail ?? error.problem?.title,
    };
  }

  return {
    status: "",
    title: UNKNOWN.title,
    description: UNKNOWN.description,
    detail: error instanceof Error ? error.message : undefined,
  };
}
