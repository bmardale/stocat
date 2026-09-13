import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId } from "react";
import { z } from "zod";
import { filesDelete, filesRename } from "@/api/generated/files/files";
import { foldersDelete, getNodesListQueryKey } from "@/api/generated/libraries/libraries";
import type { Library } from "@/api/generated/model";
import type { FileTarget } from "@/components/file-preview";
import { useAppForm } from "@/components/form";
import { nodeNameSchema } from "@/components/library-dialogs";
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
import { FieldError, FieldGroup } from "@/components/ui/field";
import { toast } from "@/components/ui/toast";
import { encryptName, type LibraryKeys, nameToken } from "@/lib/library-crypto";

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

export type DeleteDialogState = {
  open: boolean;
  target?: FileTarget & { kind: "file" | "folder" };
};

export function DeleteNodeDialog({
  state,
  onOpenChange,
  library,
}: {
  state: DeleteDialogState;
  onOpenChange: (open: boolean) => void;
  library: Library;
}) {
  const queryClient = useQueryClient();
  const target = state.target;
  const remove = useMutation({
    mutationFn: (item: FileTarget & { kind: "file" | "folder" }) =>
      item.kind === "folder" ? foldersDelete(library.id, item.id) : filesDelete(item.id),
    onSuccess: async () => {
      toast.add({ type: "success", description: "Item moved to the trash." });
      await queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(library.id) });
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
          <AlertDialogTitle>Move {target?.name} to the trash?</AlertDialogTitle>
          <AlertDialogDescription>
            The item stays in the trash for 30 days. After that, the server deletes it permanently.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {remove.error && <FieldError>{remove.error.message}</FieldError>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={remove.isPending}
            onClick={() =>
              target && remove.mutate(target, { onSuccess: () => onOpenChange(false) })
            }
          >
            Move to trash
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
