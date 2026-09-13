import { queryOptions } from "@tanstack/react-query";
import { getVersionQueryOptions } from "./generated/default/default";

export const versionQueryOptions = queryOptions({
  ...getVersionQueryOptions(),
  staleTime: 5 * 60 * 1000,
});
