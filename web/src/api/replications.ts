import { queryOptions } from "@tanstack/react-query";
import {
  getReplicationsListQueryKey,
  replicationsList,
} from "./generated/replications/replications";

export const replicationsQueryOptions = queryOptions({
  queryKey: getReplicationsListQueryKey(),
  queryFn: ({ signal }) => replicationsList({ signal }),
  refetchInterval: 15_000,
});
