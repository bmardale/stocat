import {
  ArrowDown01Icon,
  File01Icon,
  Folder01Icon,
  FolderAddIcon,
  LibraryIcon,
  Settings01Icon,
  SquareLock02Icon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import type { InfiniteData } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Fragment, useEffect, useState } from "react";
import { z } from "zod";
import { librariesQueryOptions } from "@/api/libraries";
import { getNodesListQueryKey, nodesList } from "@/api/generated/libraries/libraries";
import type { Library, Node, NodesPage } from "@/api/generated/model";
import { CreateFolderDialog, UnlockLibraryDialog } from "@/components/library-dialogs";
import { useLibraryKeys } from "@/components/library-keys";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
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
import { decryptName, type LibraryKeys } from "@/lib/library-crypto";

export const Route = createFileRoute("/_app/")({
  validateSearch: z.object({ library: z.string().optional(), folder: z.string().optional() }),
  loader: ({ context }) => context.queryClient.ensureQueryData(librariesQueryOptions),
  component: Files,
});

const lastLibraryKey = "stocat.last-library";

// The dialog keeps the library while it closes, so the content does not change during the animation.
type UnlockState = { open: boolean; library?: Library };

function Files() {
  const search = Route.useSearch();
  const navigate = useNavigate();
  const { data: libraries } = useSuspenseQuery(librariesQueryOptions);
  const libraryKeys = useLibraryKeys();
  const [unlocking, setUnlocking] = useState<UnlockState>({ open: false });
  const library =
    libraries.find((item) => item.id === search.library) ??
    libraries.find((item) => item.id === localStorage.getItem(lastLibraryKey)) ??
    libraries[0];
  const libraryId = library?.id;

  useEffect(() => {
    if (libraryId) {
      localStorage.setItem(lastLibraryKey, libraryId);
    }
  }, [libraryId]);

  const selectLibrary = (next: Library) => {
    void navigate({ to: "/", search: { library: next.id } });
    if (next.encryption_mode === "e2ee" && !libraryKeys.keys(next.id)) {
      setUnlocking({ open: true, library: next });
    }
  };

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="font-heading text-4xl font-bold tracking-tighter">Files</h1>
          <p className="leading-relaxed text-muted-foreground">
            Browse the files and folders in your libraries.
          </p>
        </div>
        {library && (
          <LibrarySwitcher libraries={libraries} library={library} onSelect={selectLibrary} />
        )}
      </div>
      {library ? (
        <FileBrowser
          library={library}
          // A folder belongs to the library in the URL. Ignore it for a fallback library.
          folder={search.library === library.id ? search.folder : undefined}
          onUnlock={() => setUnlocking({ open: true, library })}
        />
      ) : (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={LibraryIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>No libraries</EmptyTitle>
            <EmptyDescription>Set up a library to store files and folders.</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Link to="/libraries" className={buttonVariants()}>
              Set up a library
            </Link>
          </EmptyContent>
        </Empty>
      )}
      <UnlockLibraryDialog
        open={unlocking.open}
        library={unlocking.library}
        onOpenChange={(open) => setUnlocking((state) => ({ ...state, open }))}
      />
    </section>
  );
}

function LibrarySwitcher({
  libraries,
  library,
  onSelect,
}: {
  libraries: Library[];
  library: Library;
  onSelect: (library: Library) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="outline"
            size="lg"
            className="max-w-72"
            aria-label={`Library: ${library.name}`}
          />
        }
      >
        <HugeiconsIcon icon={LibraryIcon} strokeWidth={2} data-icon="inline-start" />
        <span className="truncate">{library.name}</span>
        <HugeiconsIcon icon={ArrowDown01Icon} strokeWidth={2} data-icon="inline-end" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-auto min-w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Libraries</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={library.id}
            onValueChange={(id) => {
              const next = libraries.find((item) => item.id === id);
              if (next) {
                onSelect(next);
              }
            }}
          >
            {libraries.map((item) => (
              <DropdownMenuRadioItem key={item.id} value={item.id}>
                <span className="truncate">{item.name}</span>
                {item.encryption_mode === "e2ee" && (
                  <>
                    <HugeiconsIcon
                      icon={SquareLock02Icon}
                      strokeWidth={2}
                      className="text-muted-foreground"
                    />
                    <span className="sr-only">, encrypted</span>
                  </>
                )}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem render={<Link to="/libraries" />}>
          <HugeiconsIcon icon={Settings01Icon} strokeWidth={2} />
          Manage libraries
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// A missing name means that the name cannot be decrypted.
type LibraryNode = Node & { displayName?: string };
type LibraryNodesPage = Omit<NodesPage, "items"> & { items: LibraryNode[] };

async function withDisplayName(node: Node, keys?: LibraryKeys): Promise<LibraryNode> {
  if (!node.encrypted_name) {
    return { ...node, displayName: node.name };
  }
  if (!keys) {
    return node;
  }
  try {
    return { ...node, displayName: await decryptName(keys, node.encrypted_name) };
  } catch {
    return node;
  }
}

function nodesQueryOptions(library: Library, folder?: string, keys?: LibraryKeys) {
  return infiniteQueryOptions({
    queryKey: getNodesListQueryKey(library.id, { parent_id: folder }),
    queryFn: async ({ pageParam, signal }): Promise<LibraryNodesPage> => {
      const page = await nodesList(
        library.id,
        { parent_id: folder, cursor: pageParam },
        { signal },
      );
      const items = await Promise.all(page.items.map((node) => withDisplayName(node, keys)));
      return { ...page, items };
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor,
    enabled: library.encryption_mode === "none" || keys !== undefined,
  });
}

type Crumb = { id: string; name?: string };

// The API has no ancestor lookup. The path uses the folder listings that are in the cache.
// An unknown folder shows as an ellipsis, and the path stops there.
function useFolderPath(library: Library, folder?: string) {
  const queryClient = useQueryClient();
  const known = new Map<string, LibraryNode>();
  for (const [, data] of queryClient.getQueriesData<InfiniteData<LibraryNodesPage>>({
    queryKey: getNodesListQueryKey(library.id),
  })) {
    for (const node of data?.pages.flatMap((page) => page.items) ?? []) {
      known.set(node.id, node);
    }
  }
  const path: Crumb[] = [];
  let id = folder;
  while (id && id !== library.root_node_id && !path.some((crumb) => crumb.id === id)) {
    const node = known.get(id);
    path.unshift({ id, name: node?.displayName });
    id = node?.parent_id;
  }
  return path;
}

const crumbLinkClass = "text-muted-foreground hover:text-foreground";

function FileBrowser({
  library,
  folder,
  onUnlock,
}: {
  library: Library;
  folder?: string;
  onUnlock: () => void;
}) {
  const queryClient = useQueryClient();
  const libraryKeys = useLibraryKeys();
  const keys = libraryKeys.keys(library.id);
  const locked = library.encryption_mode === "e2ee" && !keys;
  const path = useFolderPath(library, locked ? undefined : folder);
  const [creating, setCreating] = useState(false);

  const lock = () => {
    libraryKeys.lock(library.id);
    queryClient.removeQueries({ queryKey: getNodesListQueryKey(library.id) });
  };

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <nav aria-label="Folder path" className="min-w-0">
          <ol className="flex flex-wrap items-center gap-1.5 text-sm">
            <li>
              {path.length > 0 ? (
                <Link to="/" search={{ library: library.id }} className={crumbLinkClass}>
                  {library.name}
                </Link>
              ) : (
                <span aria-current="page" className="font-medium">
                  {library.name}
                </span>
              )}
            </li>
            {path.map((crumb, index) => (
              <Fragment key={crumb.id}>
                <li aria-hidden className="text-muted-foreground">
                  /
                </li>
                <li>
                  {index === path.length - 1 ? (
                    <span aria-current="page" className="font-medium">
                      {crumb.name ?? "…"}
                    </span>
                  ) : (
                    <Link
                      to="/"
                      search={{ library: library.id, folder: crumb.id }}
                      className={crumbLinkClass}
                    >
                      {crumb.name ?? "…"}
                    </Link>
                  )}
                </li>
              </Fragment>
            ))}
          </ol>
        </nav>
        {!locked && (
          <div className="flex gap-2">
            {keys && (
              <Button variant="outline" onClick={lock}>
                <HugeiconsIcon icon={SquareLock02Icon} strokeWidth={2} data-icon="inline-start" />
                Lock
              </Button>
            )}
            <Button onClick={() => setCreating(true)}>
              <HugeiconsIcon icon={FolderAddIcon} strokeWidth={2} data-icon="inline-start" />
              New folder
            </Button>
          </div>
        )}
      </div>
      {locked ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={SquareLock02Icon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>{library.name} is locked</EmptyTitle>
            <EmptyDescription>
              Enter the passphrase of the library to show its files and folders.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={onUnlock}>Unlock</Button>
          </EmptyContent>
        </Empty>
      ) : (
        <FolderContents library={library} folder={folder} keys={keys} />
      )}
      <CreateFolderDialog
        open={creating}
        onOpenChange={setCreating}
        library={library}
        parentId={folder}
        keys={keys}
      />
    </>
  );
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function FolderContents({
  library,
  folder,
  keys,
}: {
  library: Library;
  folder?: string;
  keys?: LibraryKeys;
}) {
  const nodes = useInfiniteQuery(nodesQueryOptions(library, folder, keys));

  if (nodes.isPending) {
    return (
      <div className="flex flex-col gap-2" aria-busy>
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
        <Skeleton className="h-10" />
      </div>
    );
  }
  if (nodes.isError) {
    return <FieldError>{nodes.error.message}</FieldError>;
  }
  const items = nodes.data.pages.flatMap((page) => page.items);
  if (items.length === 0) {
    return (
      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <HugeiconsIcon icon={Folder01Icon} strokeWidth={2} />
          </EmptyMedia>
          <EmptyTitle>This folder is empty</EmptyTitle>
          <EmptyDescription>Create a folder to organize your files.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }

  return (
    <div className="flex flex-col items-center gap-4">
      <Card className="w-full py-0">
        <Table aria-label="Folder contents">
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">Name</TableHead>
              <TableHead className="w-48 pr-4">Modified</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((node) => (
              <TableRow key={node.id}>
                <TableCell className="max-w-0 pl-4">
                  <NodeName library={library} node={node} />
                </TableCell>
                <TableCell className="pr-4 text-muted-foreground">
                  <time dateTime={node.updated_at}>
                    {dateFormat.format(new Date(node.updated_at))}
                  </time>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
      {nodes.hasNextPage && (
        <Button
          variant="outline"
          disabled={nodes.isFetchingNextPage}
          onClick={() => void nodes.fetchNextPage()}
        >
          {nodes.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
    </div>
  );
}

function NodeName({ library, node }: { library: Library; node: LibraryNode }) {
  const label = node.displayName ?? (
    <span className="text-muted-foreground italic">Name cannot be decrypted</span>
  );
  const content = (
    <>
      <HugeiconsIcon
        icon={node.kind === "folder" ? Folder01Icon : File01Icon}
        strokeWidth={2}
        className="size-4 shrink-0 text-muted-foreground"
      />
      <span className="truncate">{label}</span>
    </>
  );

  if (node.kind === "file") {
    return <span className="flex items-center gap-2">{content}</span>;
  }
  return (
    <Link
      to="/"
      search={{ library: library.id, folder: node.id }}
      className="flex items-center gap-2 font-medium hover:underline"
    >
      {content}
    </Link>
  );
}
