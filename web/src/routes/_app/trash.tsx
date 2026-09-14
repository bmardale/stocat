import {
  Delete02Icon,
  File01Icon,
  RestoreBinIcon,
  WasteRestoreIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
  type InfiniteData,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import {
  filesDeletePermanently,
  filesRestore,
  filesTrashList,
  getFilesTrashListQueryKey,
} from "@/api/generated/files/files";
import { getNodesListQueryKey } from "@/api/generated/libraries/libraries";
import type { Library, TrashedFile, TrashPage } from "@/api/generated/model";
import { librariesQueryOptions } from "@/api/libraries";
import { UnlockLibraryDialog } from "@/components/library-dialogs";
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
import { Card } from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { FieldError } from "@/components/ui/field";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import { decryptName } from "@/lib/library-crypto";

export const Route = createFileRoute("/_app/trash")({
  loader: ({ context }) => context.queryClient.ensureQueryData(librariesQueryOptions),
  component: Trash,
});

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const emptyTrash: TrashedFile[] = [];

function Trash() {
  const queryClient = useQueryClient();
  const { data: libraries } = useSuspenseQuery(librariesQueryOptions);
  const trash = useInfiniteQuery({
    queryKey: getFilesTrashListQueryKey(),
    queryFn: ({ pageParam, signal }) => filesTrashList({ cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor,
  });
  const libraryKeys = useLibraryKeys();
  const [names, setNames] = useState<Record<string, string>>({});
  const [unlocking, setUnlocking] = useState<Library>();
  const [deleting, setDeleting] = useState<TrashedFile>();
  const pages = trash.data?.pages;
  // The name effect depends on items. A new array on each render starts an endless render loop.
  const items = useMemo(() => pages?.flatMap((page) => page.items) ?? emptyTrash, [pages]);

  useEffect(() => {
    let active = true;
    void Promise.all(
      items.map(async (file) => {
        if (!file.encrypted_name) return undefined;
        const keys = libraryKeys.keys(file.library_id);
        if (!keys) return undefined;
        try {
          return [file.id, await decryptName(keys, file.encrypted_name)] as const;
        } catch {
          return undefined;
        }
      }),
    ).then((values) => {
      if (active) setNames(Object.fromEntries(values.filter((value) => value !== undefined)));
    });
    return () => {
      active = false;
    };
  }, [items, libraryKeys]);

  const refresh = async (libraryID: string) => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getFilesTrashListQueryKey() }),
      queryClient.invalidateQueries({ queryKey: getNodesListQueryKey(libraryID) }),
    ]);
  };
  const removeFromTrash = (id: string) => {
    queryClient.setQueryData<InfiniteData<TrashPage>>(getFilesTrashListQueryKey(), (data) =>
      data
        ? {
            ...data,
            pages: data.pages.map((page) => ({
              ...page,
              items: page.items.filter((item) => item.id !== id),
            })),
          }
        : data,
    );
  };
  const restore = useMutation({
    mutationFn: (id: string) => filesRestore(id),
    onSuccess: async (_, id) => {
      const file = items.find((item) => item.id === id);
      removeFromTrash(id);
      toast.add({ type: "success", description: "File restored." });
      if (file) await refresh(file.library_id);
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => filesDeletePermanently(id),
    onSuccess: async (_, id) => {
      const file = items.find((item) => item.id === id);
      removeFromTrash(id);
      setDeleting(undefined);
      toast.add({ type: "success", description: "File permanently deleted." });
      if (file) await refresh(file.library_id);
    },
  });

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Trash</h1>
        <p className="leading-relaxed text-muted-foreground">
          Restore files or delete them now. The server permanently deletes files after 30 days.
        </p>
      </div>
      {trash.isPending ? (
        <div className="flex flex-col gap-2" aria-busy>
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
        </div>
      ) : trash.isError ? (
        <FieldError>{trash.error.message}</FieldError>
      ) : items.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={RestoreBinIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>Trash is empty</EmptyTitle>
            <EmptyDescription>Files that you delete appear here for 30 days.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table aria-label="Trashed files">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Library</TableHead>
                <TableHead>Delete after</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((file) => {
                const name = file.name ?? names[file.id];
                const library = libraries.find((item) => item.id === file.library_id);
                return (
                  <TableRow key={file.id}>
                    <TableCell className="pl-4">
                      <span className="flex items-center gap-2">
                        <HugeiconsIcon
                          icon={File01Icon}
                          strokeWidth={2}
                          className="size-4 text-muted-foreground"
                        />
                        <span>{name ?? "Name cannot be decrypted"}</span>
                      </span>
                    </TableCell>
                    <TableCell>{file.library_name}</TableCell>
                    <TableCell>
                      <time dateTime={file.delete_after}>
                        {dateFormat.format(new Date(file.delete_after))}
                      </time>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-2">
                        {!name && library && (
                          <Button variant="outline" size="sm" onClick={() => setUnlocking(library)}>
                            Unlock
                          </Button>
                        )}
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={restore.isPending}
                          onClick={() => restore.mutate(file.id)}
                        >
                          <HugeiconsIcon
                            icon={WasteRestoreIcon}
                            strokeWidth={2}
                            data-icon="inline-start"
                          />
                          Restore
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={remove.isPending}
                          onClick={() => setDeleting(file)}
                        >
                          <HugeiconsIcon
                            icon={Delete02Icon}
                            strokeWidth={2}
                            data-icon="inline-start"
                          />
                          Delete
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}
      {trash.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          disabled={trash.isFetchingNextPage}
          onClick={() => void trash.fetchNextPage()}
        >
          {trash.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
      {restore.error && <FieldError>{restore.error.message}</FieldError>}
      <UnlockLibraryDialog
        open={Boolean(unlocking)}
        library={unlocking}
        onOpenChange={(open) => {
          if (!open) setUnlocking(undefined);
        }}
      />
      <AlertDialog
        open={Boolean(deleting)}
        onOpenChange={(open) => {
          if (open) return;
          setDeleting(undefined);
          remove.reset();
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Permanently delete {deleting?.name ?? names[deleting?.id ?? ""] ?? "this file"}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This deletes every version. You cannot undo this action.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {remove.error && <FieldError>{remove.error.message}</FieldError>}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={remove.isPending}
              onClick={() => deleting && remove.mutate(deleting.id)}
            >
              Delete permanently
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
