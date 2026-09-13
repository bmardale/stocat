import { queryOptions } from "@tanstack/react-query";
import { ApiError } from "./fetcher";
import { authMe } from "./generated/auth/auth";

export const currentUserQueryOptions = queryOptions({
  queryKey: ["auth", "current-user"],
  queryFn: async ({ signal }) => {
    try {
      return await authMe({ signal });
    } catch (error) {
      // The server returns 401 when the request has no valid session.
      if (error instanceof ApiError && error.status === 401) {
        return null;
      }
      throw error;
    }
  },
  staleTime: 5 * 60 * 1000,
});
