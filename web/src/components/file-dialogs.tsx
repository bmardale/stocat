import {
  ArrowLeft01Icon,
  ArrowRight01Icon,
  Folder01Icon,
  LibraryIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { z } from "zod";
import { filesMove, filesRename, useFilesDelete } from "@/api/generated/files/files";
import { getNodesListQueryKey, nodesList } from "@/api/generated/libraries/libraries";
import type { Library, Node } from "@/api/generated/model";
import { replicationsQueryOptions } from "@/api/replications";
import type { FileTarget } from "@/components/file-preview";
import { useAppForm } from "@/components/form";
import { BackendIcon, nodeNameSchema } from "@/components/library-dialogs";
import { useLibraryKeys } from "@/components/library-keys";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { toast } from "@/components/ui/toast";
import { decryptName, encryptName, type LibraryKeys, nameToken } from "@/lib/library-crypto";

// The dialog keeps the file while it closes, so the content does not change during the animation.
export type FileDialogState = { open: boolean; file?: FileTarget };

const renameSchema = z.object({ name: nodeNameSchema("Enter a file name.") });

export function RenameFileDialog({
  state,
  onOpenChange,
  library,
  keys,
}: {
  state: FileDialogState;
  onOpenChange: (open: boolean) => void;
  library: Library;
  keys?: LibraryKeys;
}) {
  const file = state.file;
  return (
    <Dialog open={state.open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {file && (
          <RenameFileForm
            key={file.id}
            file={file}
            library={library}
            keys={keys}
            onRenamed={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function RenameFileForm({
  file,
  library,
  keys,
  onRenamed,
}: {
  file: FileTarget;
  library: Library;
  keys?: LibraryKeys;
  onRenamed: () => void;
}) {
  const queryClient = useQueryClient();
  const formId = useId();
  const rename = useMutation({
    mutationFn: async (name: string) => {
      if (library.encryption_mode === "none") {
        return filesRename(file.id, { name });
      }
      if (!keys) {
        throw new Error("Unlock the library first.");
      }
      const normalized = name.normalize("NFC");
      const [encrypted_name, name_token] = await Promise.all([
        encryptName(keys, normalized),
        nameToken(keys, file.parentId ?? library.root_node_id, normalized),
      ]);
      return filesRename(file.id, { encrypted_name, name_token });
    },
    // Keep the mutation pending until the list shows the result.
    onSuccess: () => {
      toast.add({ type: "success", description: "File renamed." });
      return queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) });
    },
  });
  const form = useAppForm({
    defaultValues: { name: file.name },
    validators: { onSubmit: renameSchema },
    onSubmit: ({ value }) => rename.mutate(value.name.trim(), { onSuccess: onRenamed }),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Rename file</DialogTitle>
        <DialogDescription>
          {library.encryption_mode === "e2ee"
            ? "This browser encrypts the name before it sends it to the server."
            : `Enter a new name for ${file.name}.`}
        </DialogDescription>
      </DialogHeader>
      <form
        id={formId}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void form.handleSubmit();
        }}
      >
        <FieldGroup>
          <form.AppField name="name">
            {(field) => (
              <field.TextField
                label="Name"
                autoComplete="off"
                autoFocus
                // Select the name without the extension, so the user can type a new name at once.
                onFocus={(event) => {
                  const end = event.target.value.lastIndexOf(".");
                  event.target.setSelectionRange(0, end > 0 ? end : event.target.value.length);
                }}
              />
            )}
          </form.AppField>
          {rename.error && <FieldError>{rename.error.message}</FieldError>}
        </FieldGroup>
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={rename.isPending}>
          Rename
        </Button>
      </DialogFooter>
    </>
  );
}

type MoveFolder = { id: string; name?: string };
type MovePath = { id: string; name: string };

export function MoveFileDialog({
  state,
  onOpenChange,
  library,
  libraries,
}: {
  state: FileDialogState;
  onOpenChange: (open: boolean) => void;
  library: Library;
  libraries: Library[];
}) {
  const file = state.file;
  return (
    <Dialog open={state.open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        {file && (
          <MoveFileForm
            key={`${file.id}:${state.open}`}
            file={file}
            library={library}
            libraries={libraries}
            open={state.open}
            onMoved={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function MoveFileForm({
  file,
  library,
  libraries,
  open,
  onMoved,
}: {
  file: FileTarget;
  library: Library;
  libraries: Library[];
  open: boolean;
  onMoved: () => void;
}) {
  const queryClient = useQueryClient();
  const libraryKeys = useLibraryKeys();
  const [destinationID, setDestinationID] = useState(library.id);
  const [path, setPath] = useState<MovePath[]>([]);
  const replications = useQuery({ ...replicationsQueryOptions, enabled: open });
  const readOnlyIDs = new Set(
    (replications.data ?? []).map((replication) => replication.destination.id),
  );
  const choices = libraries.filter(
    (item) =>
      item.backend.id === library.backend.id &&
      item.encryption_mode === library.encryption_mode &&
      !readOnlyIDs.has(item.id),
  );
  const destination = choices.find((item) => item.id === destinationID);
  const currentFolderID = path[path.length - 1]?.id;
  const destinationKeys = destination ? libraryKeys.keys(destination.id) : undefined;
  const folders = useQuery({
    queryKey: ["move-folders", destination?.id ?? "move-destination", currentFolderID],
    queryFn: async ({ signal }) => {
      if (!destination) return [];
      const page = await nodesList(destination.id, { parent_id: currentFolderID }, { signal });
      return Promise.all(
        page.items
          .filter((node) => node.kind === "folder")
          .map(async (node): Promise<MoveFolder> => ({
            id: node.id,
            name: await moveFolderName(node, destinationKeys),
          })),
      );
    },
    enabled: open && destination !== undefined,
  });
  const move = useMutation({
    mutationFn: ({ libraryID, parentID }: { libraryID: string; parentID?: string }) => {
      const data = parentID
        ? { library_id: libraryID, parent_id: parentID }
        : { library_id: libraryID };
      return filesMove(file.id, data);
    },
    onSuccess: async (_node, variables) => {
      toast.add({ type: "success", description: "File moved." });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) }),
        queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(variables.libraryID) }),
      ]);
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Move file</DialogTitle>
        <DialogDescription>Choose a library and folder for {file.name}.</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-5">
        <div className="space-y-2">
          <p className="text-sm font-medium">Destination library</p>
          {choices.length === 0 ? (
            <div className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">
              No compatible libraries are available.
            </div>
          ) : (
            <RadioGroup
              value={destinationID}
              onValueChange={(value) => {
                setDestinationID(value);
                setPath([]);
              }}
            >
              {choices.map((item) => (
                <FieldLabel key={item.id} htmlFor={`move-library-${item.id}`}>
                  <Field orientation="horizontal">
                    <BackendIcon
                      type={item.backend.type}
                      className="size-4 text-muted-foreground"
                    />
                    <FieldContent className="min-w-0">
                      <FieldTitle className="truncate">{item.name}</FieldTitle>
                      <FieldDescription className="truncate">{item.backend.name}</FieldDescription>
                    </FieldContent>
                    <RadioGroupItem id={`move-library-${item.id}`} value={item.id} />
                  </Field>
                </FieldLabel>
              ))}
            </RadioGroup>
          )}
        </div>
        {destination && (
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-2">
              <p className="text-sm font-medium">Destination folder</p>
              {path.length > 0 && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setPath((current) => current.slice(0, -1))}
                >
                  <HugeiconsIcon icon={ArrowLeft01Icon} strokeWidth={2} data-icon="inline-start" />
                  Back
                </Button>
              )}
            </div>
            <div className="rounded-lg border p-1">
              <div className="flex items-center gap-2 px-2 py-2 text-sm font-medium">
                <HugeiconsIcon icon={LibraryIcon} strokeWidth={2} className="size-4" />
                <span className="truncate">{path[path.length - 1]?.name ?? destination.name}</span>
                <span className="text-xs font-normal text-muted-foreground">Current</span>
              </div>
              <div className="border-t pt-1">
                {folders.isPending ? (
                  <p className="px-2 py-2 text-sm text-muted-foreground">Loading folders…</p>
                ) : folders.isError ? (
                  <FieldError className="px-2 py-2">{folders.error.message}</FieldError>
                ) : folders.data.length === 0 ? (
                  <p className="px-2 py-2 text-sm text-muted-foreground">No folders here.</p>
                ) : (
                  <div className="flex flex-col">
                    {folders.data.map((folder) => (
                      <Button
                        key={folder.id}
                        variant="ghost"
                        className="justify-start"
                        onClick={() => {
                          const name = folder.name;
                          if (!name) return;
                          setPath((current) => [...current, { id: folder.id, name }]);
                        }}
                        disabled={!folder.name}
                      >
                        <HugeiconsIcon icon={Folder01Icon} strokeWidth={2} />
                        <span className="truncate">{folder.name ?? "Folder name unavailable"}</span>
                        <HugeiconsIcon
                          icon={ArrowRight01Icon}
                          strokeWidth={2}
                          className="ml-auto"
                        />
                      </Button>
                    ))}
                  </div>
                )}
              </div>
            </div>
            {destination.encryption_mode === "e2ee" && !destinationKeys && (
              <p className="text-xs leading-relaxed text-muted-foreground">
                Unlock this library in Files to browse encrypted folder names.
              </p>
            )}
          </div>
        )}
        {move.error && <FieldError>{move.error.message}</FieldError>}
      </div>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button
          disabled={!destination || folders.isPending || move.isPending}
          onClick={() =>
            destination &&
            move.mutate(
              { libraryID: destination.id, parentID: currentFolderID },
              { onSuccess: onMoved },
            )
          }
        >
          {move.isPending ? "Moving…" : "Move"}
        </Button>
      </DialogFooter>
    </>
  );
}

async function moveFolderName(node: Node, keys?: LibraryKeys) {
  if (node.name) return node.name;
  if (!node.encrypted_name || !keys) return undefined;
  try {
    return await decryptName(keys, node.encrypted_name);
  } catch {
    return undefined;
  }
}

export function DeleteFileDialog({
  state,
  onOpenChange,
  library,
}: {
  state: FileDialogState;
  onOpenChange: (open: boolean) => void;
  library: Library;
}) {
  const queryClient = useQueryClient();
  const file = state.file;
  const remove = useFilesDelete({
    mutation: {
      onSuccess: async () => {
        toast.add({ type: "success", description: "File moved to trash." });
        await queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) });
      },
    },
  });

  return (
    <AlertDialog
      open={state.open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) {
          remove.reset();
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {file?.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            You can restore this file from Trash for 30 days.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {remove.error && <FieldError>{remove.error.message}</FieldError>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              file && remove.mutate({ id: file.id }, { onSuccess: () => onOpenChange(false) })
            }
          >
            Move to trash
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
