import {
  Alert02Icon,
  Clock01Icon,
  Delete02Icon,
  DeletePutBackIcon,
  Folder01Icon,
  File01Icon,
  LibraryIcon,
  MoreVerticalIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import type { InfiniteData, UseInfiniteQueryResult } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import {
  getTrashListQueryKey,
  trashDelete,
  trashList,
  trashRestore,
  useTrashEmpty,
} from "@/api/generated/trash/trash";
import type { Library, TrashItem, TrashPage } from "@/api/generated/model";
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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Empty,
  EmptyContent,
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
import { decryptName, type LibraryKeys } from "@/lib/library-crypto";

export const Route = createFileRoute("/_app/trash")({
  loader: ({ context }) => context.queryClient.ensureQueryData(librariesQueryOptions),
  component: Trash,
});

type TrashRowItem = TrashItem & { displayName?: string; library?: Library };
type TrashPageData = Omit<TrashPage, "items"> & { items: TrashRowItem[] };
type TrashQuery = UseInfiniteQueryResult<InfiniteData<TrashPageData, unknown>, Error>;

async function withDisplayName(
  item: TrashItem,
  libraries: Library[],
  keys: LibraryKeys | undefined,
): Promise<TrashRowItem> {
  const library = libraries.find((candidate) => candidate.id === item.library_id);
  if (!item.encrypted_name) {
    return { ...item, displayName: item.name, library };
  }
  if (!keys) {
    return { ...item, library };
  }
  try {
    return { ...item, displayName: await decryptName(keys, item.encrypted_name), library };
  } catch {
    return { ...item, library };
  }
}

function trashQueryOptions(
  libraries: Library[],
  unlocked: string,
  keyring: (id: string) => LibraryKeys | undefined,
) {
  return infiniteQueryOptions({
    queryKey: [...getTrashListQueryKey(), unlocked],
    queryFn: async ({ pageParam, signal }): Promise<TrashPageData> => {
      const page = await trashList({ cursor: pageParam, limit: 100 }, { signal });
      const items = await Promise.all(
        page.items.map((item) => withDisplayName(item, libraries, keyring(item.library_id))),
      );
      return { ...page, items };
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor,
  });
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const dayMs = 86_400_000;

function expiryLabel(expiresAt: string) {
  const days = Math.ceil((new Date(expiresAt).getTime() - Date.now()) / dayMs);
  if (days <= 0) {
    return "today";
  }
  if (days === 1) {
    return "in 1 day";
  }
  return `in ${days} days`;
}

function Trash() {
  const { data: libraries } = useSuspenseQuery(librariesQueryOptions);
  const libraryKeys = useLibraryKeys();
  const queryClient = useQueryClient();
  const unlocked = libraries
    .filter((library) => libraryKeys.keys(library.id))
    .map((library) => library.id)
    .join(",");
  const trash = useInfiniteQuery(trashQueryOptions(libraries, unlocked, libraryKeys.keys));
  const [unlocking, setUnlocking] = useState<{ open: boolean; library?: Library }>({ open: false });
  const [deleting, setDeleting] = useState<{ open: boolean; item?: TrashRowItem }>({ open: false });

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: getTrashListQueryKey() });
    await queryClient.invalidateQueries({ queryKey: ["/api/v1/libraries"] });
  };
  const restore = useMutation({
    mutationFn: (item: TrashItem) => trashRestore(item.id),
    onSuccess: async () => {
      toast.add({ type: "success", description: "Item restored." });
      await refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (item: TrashItem) => trashDelete(item.id),
    onSuccess: async () => {
      toast.add({ type: "success", description: "Item permanently deleted." });
      await refresh();
    },
  });
  const empty = useTrashEmpty({
    mutation: {
      onSuccess: async () => {
        toast.add({ type: "success", description: "Trash emptied." });
        await refresh();
      },
    },
  });

  const items = trash.data?.pages.flatMap((page) => page.items) ?? [];

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="font-heading text-4xl font-bold tracking-tighter">Trash</h1>
          <p className="leading-relaxed text-muted-foreground">
            Restore items, or let the server delete them permanently after 30 days.
          </p>
        </div>
        <EmptyTrashButton
          disabled={items.length === 0}
          pending={empty.isPending}
          onEmpty={() => empty.mutate()}
        />
      </div>
      {empty.error && <FieldError>{empty.error.message}</FieldError>}
      <TrashContents
        trash={trash}
        onRestore={(item) => restore.mutate(item)}
        onDelete={(item) => setDeleting({ open: true, item })}
        onUnlock={(library) => setUnlocking({ open: true, library })}
      />
      {trash.hasNextPage && (
        <Button
          variant="outline"
          disabled={trash.isFetchingNextPage}
          onClick={() => void trash.fetchNextPage()}
        >
          {trash.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
      <UnlockLibraryDialog
        open={unlocking.open}
        library={unlocking.library}
        onOpenChange={(open) => setUnlocking((state) => ({ ...state, open }))}
      />
      <AlertDialog
        open={deleting.open}
        onOpenChange={(open) => {
          setDeleting((state) => ({ ...state, open }));
          if (!open) {
            remove.reset();
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Permanently delete {deleting.item?.displayName ?? "this item"}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              The server deletes the item and its versions. You cannot undo this action.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {remove.error && <FieldError>{remove.error.message}</FieldError>}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={remove.isPending}
              onClick={() =>
                deleting.item &&
                remove.mutate(deleting.item, { onSuccess: () => setDeleting({ open: false }) })
              }
            >
              Delete permanently
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function EmptyTrashButton({
  disabled,
  pending,
  onEmpty,
}: {
  disabled: boolean;
  pending: boolean;
  onEmpty: () => void;
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger render={<Button variant="destructive" disabled={disabled || pending} />}>
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} data-icon="inline-start" />
        Empty trash
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Empty the trash?</AlertDialogTitle>
          <AlertDialogDescription>
            The server permanently deletes all trashed items and their versions. You cannot undo
            this action.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={onEmpty}>
            Empty trash
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function TrashContents({
  trash,
  onRestore,
  onDelete,
  onUnlock,
}: {
  trash: TrashQuery;
  onRestore: (item: TrashRowItem) => void;
  onDelete: (item: TrashRowItem) => void;
  onUnlock: (library: Library) => void;
}) {
  if (trash.isPending) {
    return (
      <div className="flex flex-col gap-2" aria-busy>
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
      </div>
    );
  }
  if (trash.isError) {
    return <FieldError>{trash.error.message}</FieldError>;
  }
  const items = trash.data.pages.flatMap((page) => page.items);
  if (items.length === 0) {
    return (
      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>The trash is empty</EmptyTitle>
          <EmptyDescription>Items that you delete stay here for 30 days.</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <p className="text-sm text-muted-foreground">
            Deleted files and folders appear here. Open Files to delete an item.
          </p>
        </EmptyContent>
      </Empty>
    );
  }

  return (
    <Card className="w-full py-0">
      <Table aria-label="Trashed items">
        <TableHeader>
          <TableRow>
            <TableHead className="pl-4">Name</TableHead>
            <TableHead className="w-40">Library</TableHead>
            <TableHead className="w-44">Deleted</TableHead>
            <TableHead className="w-32">Deletes</TableHead>
            <TableHead className="w-12 pr-2">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={item.id}>
              <TableCell className="max-w-0 pl-4">
                <div className="flex items-center gap-2">
                  <HugeiconsIcon
                    icon={item.kind === "folder" ? Folder01Icon : File01Icon}
                    strokeWidth={2}
                    className="size-4 shrink-0 text-muted-foreground"
                  />
                  <span className="truncate">
                    {item.displayName ?? (
                      <span className="text-muted-foreground italic">Name cannot be decrypted</span>
                    )}
                  </span>
                  {!item.displayName && item.encrypted_name && item.library && (
                    <Button
                      variant="link"
                      size="sm"
                      className="h-auto px-1"
                      onClick={() => item.library && onUnlock(item.library)}
                    >
                      Unlock
                    </Button>
                  )}
                </div>
              </TableCell>
              <TableCell className="text-muted-foreground">
                <span className="flex items-center gap-1.5">
                  <HugeiconsIcon icon={LibraryIcon} strokeWidth={2} className="size-3.5" />
                  <span className="truncate">{item.library_name}</span>
                </span>
              </TableCell>
              <TableCell className="text-muted-foreground">
                <time dateTime={item.trashed_at}>
                  {dateFormat.format(new Date(item.trashed_at))}
                </time>
              </TableCell>
              <TableCell className="text-muted-foreground">
                <span className="flex items-center gap-1.5">
                  <HugeiconsIcon icon={Clock01Icon} strokeWidth={2} className="size-3.5" />
                  {expiryLabel(item.expires_at)}
                </span>
              </TableCell>
              <TableCell className="pr-2 text-right">
                <TrashActions item={item} onRestore={onRestore} onDelete={onDelete} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}

function TrashActions({
  item,
  onRestore,
  onDelete,
}: {
  item: TrashRowItem;
  onRestore: (item: TrashRowItem) => void;
  onDelete: (item: TrashRowItem) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Actions for ${item.displayName ?? "item"}`}
          />
        }
      >
        <HugeiconsIcon icon={MoreVerticalIcon} strokeWidth={2} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-auto min-w-48">
        <DropdownMenuItem onClick={() => onRestore(item)}>
          <HugeiconsIcon icon={DeletePutBackIcon} strokeWidth={2} />
          Restore
        </DropdownMenuItem>
        <DropdownMenuItem variant="destructive" onClick={() => onDelete(item)}>
          <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
          Delete permanently
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
