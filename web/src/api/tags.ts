import { queryOptions } from "@tanstack/react-query";
import { getTagsListQueryKey, tagsList } from "./generated/libraries/libraries";
import type { Library, Tag } from "./generated/model";
import { decryptName, type LibraryKeys } from "../lib/library-crypto";

export type DisplayTag = Tag & { displayName?: string; displayColor?: string };

export async function withDisplayTag(tag: Tag, keys?: LibraryKeys): Promise<DisplayTag> {
  if (!tag.encrypted_name) return { ...tag, displayName: tag.name, displayColor: tag.color };
  if (!keys) return tag;
  try {
    if (!tag.encrypted_color) return tag;
    const [displayName, displayColor] = await Promise.all([
      decryptName(keys, tag.encrypted_name),
      decryptName(keys, tag.encrypted_color),
    ]);
    if (!/^#[0-9A-F]{6}$/.test(displayColor)) return tag;
    return { ...tag, displayName, displayColor };
  } catch {
    return tag;
  }
}

export function tagsQueryOptions(library: Library, keys?: LibraryKeys, enabled = true) {
  return queryOptions({
    queryKey: getTagsListQueryKey(library.id),
    queryFn: async ({ signal }) => {
      const tags = await tagsList(library.id, { signal });
      return Promise.all(tags.map((tag) => withDisplayTag(tag, keys)));
    },
    enabled: enabled && (library.encryption_mode === "none" || keys !== undefined),
  });
}
