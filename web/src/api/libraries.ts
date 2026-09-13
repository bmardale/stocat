import { queryOptions } from "@tanstack/react-query";
import {
  getLibrariesListQueryKey,
  getLibraryBackendsListQueryKey,
  librariesList,
  libraryBackendsList,
} from "./generated/libraries/libraries";

export const librariesQueryOptions = queryOptions({
  queryKey: getLibrariesListQueryKey(),
  queryFn: ({ signal }) => librariesList({ signal }),
});

export const libraryBackendsQueryOptions = queryOptions({
  queryKey: getLibraryBackendsListQueryKey(),
  queryFn: ({ signal }) => libraryBackendsList({ signal }),
});
