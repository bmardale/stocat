import { Delete02Icon, PencilEdit02Icon, TagsIcon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { filesTagsAdd, filesTagsRemove } from "@/api/generated/files/files";
import {
  getNodesListQueryKey,
  getTagsListQueryKey,
  tagsCreate,
  tagsDelete,
  tagsUpdate,
} from "@/api/generated/libraries/libraries";
import type { Library, TagBody } from "@/api/generated/model";
import { type DisplayTag, tagsQueryOptions } from "@/api/tags";
import type { FileTarget } from "@/components/file-preview";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { encryptName, type LibraryKeys, nameToken } from "@/lib/library-crypto";

const defaultColor = "#6366F1";

export type TaggedFileTarget = FileTarget & { tags: DisplayTag[] };
export type TagFileDialogState = { open: boolean; file?: TaggedFileTarget };

export function TagBadge({ tag }: { tag: DisplayTag }) {
  const color = tag.displayColor ?? "#64748B";
  return (
    <Badge
      variant="outline"
      className="max-w-36 border-transparent"
      style={{ backgroundColor: `${color}1A`, color }}
    >
      <span className="size-1.5 shrink-0 rounded-full bg-current" aria-hidden />
      <span className="truncate">{tag.displayName ?? "Encrypted tag"}</span>
    </Badge>
  );
}

export function ManageTagsDialog({
  open,
  onOpenChange,
  library,
  keys,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  library: Library;
  keys?: LibraryKeys;
}) {
  const queryClient = useQueryClient();
  const { data: tags = [] } = useQuery(tagsQueryOptions(library, keys, open));
  const [editing, setEditing] = useState<DisplayTag>();
  const [name, setName] = useState("");
  const [color, setColor] = useState(defaultColor);
  const mutation = useMutation({
    mutationFn: async () => {
      const body = await tagBody(library, keys, name, color);
      return editing ? tagsUpdate(library.id, editing.id, body) : tagsCreate(library.id, body);
    },
    onSuccess: async () => {
      setEditing(undefined);
      setName("");
      setColor(defaultColor);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: getTagsListQueryKey(library.id) }),
        queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) }),
      ]);
    },
  });
  const remove = useMutation({
    mutationFn: (tag: DisplayTag) => tagsDelete(library.id, tag.id),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: getTagsListQueryKey(library.id) }),
        queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) }),
      ]);
    },
  });

  const edit = (tag: DisplayTag) => {
    setEditing(tag);
    setName(tag.displayName ?? "");
    setColor(tag.displayColor ?? defaultColor);
    mutation.reset();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Manage tags</DialogTitle>
          <DialogDescription>
            Create tags for this library. Deleting a tag removes it from every file.
          </DialogDescription>
        </DialogHeader>
        {tags.length > 0 && (
          <div className="flex max-h-56 flex-col gap-1 overflow-y-auto">
            {tags.map((tag) => (
              <div key={tag.id} className="flex items-center gap-2 rounded-lg border p-2">
                <TagBadge tag={tag} />
                <span className="flex-1" />
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Edit ${tag.displayName ?? "tag"}`}
                  onClick={() => edit(tag)}
                >
                  <HugeiconsIcon icon={PencilEdit02Icon} strokeWidth={2} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Delete ${tag.displayName ?? "tag"}`}
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(tag)}
                >
                  <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
                </Button>
              </div>
            ))}
          </div>
        )}
        <form
          className="flex items-end gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim()) mutation.mutate();
          }}
        >
          <Field className="flex-1">
            <FieldLabel htmlFor="tag-name">{editing ? "New name" : "New tag"}</FieldLabel>
            <Input
              id="tag-name"
              value={name}
              maxLength={100}
              autoComplete="off"
              placeholder="Important"
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <Field className="w-auto">
            <FieldLabel htmlFor="tag-color">Color</FieldLabel>
            <Input
              id="tag-color"
              type="color"
              className="w-12 px-1"
              value={color}
              onChange={(event) => setColor(event.target.value.toUpperCase())}
            />
          </Field>
          <Button type="submit" disabled={!name.trim() || mutation.isPending}>
            {editing ? "Save" : "Add"}
          </Button>
          {editing && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setEditing(undefined);
                setName("");
                setColor(defaultColor);
              }}
            >
              Cancel
            </Button>
          )}
        </form>
        {(mutation.error || remove.error) && (
          <FieldError>{mutation.error?.message ?? remove.error?.message}</FieldError>
        )}
        <DialogFooter>
          <DialogClose render={<Button variant="outline" />}>Done</DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function FileTagsDialog({
  state,
  onOpenChange,
  library,
  keys,
}: {
  state: TagFileDialogState;
  onOpenChange: (open: boolean) => void;
  library: Library;
  keys?: LibraryKeys;
}) {
  const file = state.file;
  return (
    <Dialog open={state.open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {file && (
          <FileTagsForm
            key={file.id}
            file={file}
            library={library}
            keys={keys}
            onSaved={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function FileTagsForm({
  file,
  library,
  keys,
  onSaved,
}: {
  file: TaggedFileTarget;
  library: Library;
  keys?: LibraryKeys;
  onSaved: () => void;
}) {
  const formId = useId();
  const queryClient = useQueryClient();
  const { data: tags = [] } = useQuery(tagsQueryOptions(library, keys));
  const original = new Set(file.tags.map((tag) => tag.id));
  const [selected, setSelected] = useState(original);
  const save = useMutation({
    mutationFn: async () => {
      await Promise.all(
        tags.flatMap((tag) => {
          if (selected.has(tag.id) && !original.has(tag.id)) {
            return [filesTagsAdd(file.id, tag.id)];
          }
          if (!selected.has(tag.id) && original.has(tag.id)) {
            return [filesTagsRemove(file.id, tag.id)];
          }
          return [];
        }),
      );
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) });
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Tags for {file.name}</DialogTitle>
        <DialogDescription>Select the tags that apply to this file.</DialogDescription>
      </DialogHeader>
      <form
        id={formId}
        className="flex max-h-72 flex-col gap-1 overflow-y-auto"
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate(undefined, { onSuccess: onSaved });
        }}
      >
        {tags.length === 0 ? (
          <p className="text-sm text-muted-foreground">Create a tag before you tag this file.</p>
        ) : (
          tags.map((tag) => (
            <label
              key={tag.id}
              className="flex cursor-pointer items-center gap-3 rounded-lg border p-3"
            >
              <input
                type="checkbox"
                checked={selected.has(tag.id)}
                onChange={(event) => {
                  const next = new Set(selected);
                  if (event.target.checked) next.add(tag.id);
                  else next.delete(tag.id);
                  setSelected(next);
                }}
              />
              <TagBadge tag={tag} />
            </label>
          ))
        )}
        {save.error && <FieldError>{save.error.message}</FieldError>}
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={save.isPending || tags.length === 0}>
          Save tags
        </Button>
      </DialogFooter>
    </>
  );
}

async function tagBody(
  library: Library,
  keys: LibraryKeys | undefined,
  name: string,
  color: string,
): Promise<TagBody> {
  const normalized = name.trim().normalize("NFC");
  if (library.encryption_mode === "none") return { name: normalized, color };
  if (!keys) throw new Error("Unlock the library first.");
  const [encrypted_name, name_token, encrypted_color] = await Promise.all([
    encryptName(keys, normalized),
    nameToken(keys, `tags:${library.id}`, normalized),
    encryptName(keys, color),
  ]);
  return { encrypted_name, name_token, encrypted_color };
}

export function TagsButton({ onClick }: { onClick: () => void }) {
  return (
    <Button variant="outline" onClick={onClick}>
      <HugeiconsIcon icon={TagsIcon} strokeWidth={2} data-icon="inline-start" />
      Manage tags
    </Button>
  );
}
