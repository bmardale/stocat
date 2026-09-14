import {
  ArrowDown01Icon,
  Delete02Icon,
  Download04Icon,
  File01Icon,
  FilterIcon,
  Folder01Icon,
  FolderAddIcon,
  FolderTransferIcon,
  LibraryIcon,
  MoreVerticalIcon,
  PencilEdit02Icon,
  Settings01Icon,
  SquareLock02Icon,
  TagsIcon,
  Upload01Icon,
  ViewIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import type { InfiniteData } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Fragment, useEffect, useState } from "react";
import { z } from "zod";
import { librariesQueryOptions } from "@/api/libraries";
import { type DisplayTag, tagsQueryOptions, withDisplayTag } from "@/api/tags";
import {
  getNodesListQueryKey,
  getTagsListQueryKey,
  nodesList,
} from "@/api/generated/libraries/libraries";
import type { Library, Node, NodesPage } from "@/api/generated/model";
import { CreateFolderDialog, UnlockLibraryDialog } from "@/components/library-dialogs";
import {
  UploadDropOverlay,
  UploadInput,
  UploadQueue,
  useFileUploads,
} from "@/components/file-uploads";
import {
  FilePreviewDialog,
  useFileDownload,
  type FileTarget,
  type PreviewState,
} from "@/components/file-preview";
import {
  DeleteFileDialog,
  MoveFileDialog,
  RenameFileDialog,
  type FileDialogState,
} from "@/components/file-dialogs";
import {
  FileTagsDialog,
  ManageTagsDialog,
  TagBadge,
  TagsButton,
  type TaggedFileTarget,
  type TagFileDialogState,
} from "@/components/file-tags";
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
  validateSearch: z.object({
    library: z.string().optional(),
    folder: z.string().optional(),
    tag: z.string().optional(),
  }),
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
          key={library.id}
          library={library}
          libraries={libraries}
          // A folder belongs to the library in the URL. Ignore it for a fallback library.
          folder={search.library === library.id ? search.folder : undefined}
          tag={search.library === library.id ? search.tag : undefined}
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

function TagFilter({
  library,
  folder,
  tags,
  selected,
  onOpen,
}: {
  library: Library;
  folder?: string;
  tags: DisplayTag[];
  selected?: string;
  onOpen: () => void;
}) {
  const navigate = useNavigate();
  const selectedTag = tags.find((tag) => tag.id === selected);
  return (
    <DropdownMenu onOpenChange={(open) => open && onOpen()}>
      <DropdownMenuTrigger render={<Button variant="outline" aria-label="Filter files by tag" />}>
        <HugeiconsIcon icon={FilterIcon} strokeWidth={2} data-icon="inline-start" />
        {selectedTag ? (selectedTag.displayName ?? "Encrypted tag") : "All tags"}
        <HugeiconsIcon icon={ArrowDown01Icon} strokeWidth={2} data-icon="inline-end" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-auto min-w-44">
        <DropdownMenuRadioGroup
          value={selected ?? "all"}
          onValueChange={(value) =>
            void navigate({
              to: "/",
              search: { library: library.id, folder, tag: value === "all" ? undefined : value },
            })
          }
        >
          <DropdownMenuRadioItem value="all">All tags</DropdownMenuRadioItem>
          {tags.map((tag) => (
            <DropdownMenuRadioItem key={tag.id} value={tag.id}>
              <TagBadge tag={tag} />
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// A missing name means that the name cannot be decrypted.
type LibraryNode = Node & { displayName?: string };
type LibraryNodesPage = Omit<NodesPage, "items"> & { items: LibraryNode[] };

async function withDisplayName(node: Node, keys?: LibraryKeys): Promise<LibraryNode> {
  const tags = await Promise.all(node.tags.map((tag) => withDisplayTag(tag, keys)));
  if (!node.encrypted_name) {
    return { ...node, tags, displayName: node.name };
  }
  if (!keys) {
    return { ...node, tags };
  }
  try {
    return { ...node, tags, displayName: await decryptName(keys, node.encrypted_name) };
  } catch {
    return { ...node, tags };
  }
}

function nodesQueryOptions(library: Library, folder?: string, tag?: string, keys?: LibraryKeys) {
  return infiniteQueryOptions({
    queryKey: getNodesListQueryKey(library.id, { parent_id: folder, tag }),
    queryFn: async ({ pageParam, signal }): Promise<LibraryNodesPage> => {
      const page = await nodesList(
        library.id,
        { parent_id: folder, cursor: pageParam, tag },
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
  libraries,
  folder,
  tag,
  onUnlock,
}: {
  library: Library;
  libraries: Library[];
  folder?: string;
  tag?: string;
  onUnlock: () => void;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const libraryKeys = useLibraryKeys();
  const keys = libraryKeys.keys(library.id);
  const locked = library.encryption_mode === "e2ee" && !keys;
  const path = useFolderPath(library, locked ? undefined : folder);
  const [creating, setCreating] = useState(false);
  const [previewing, setPreviewing] = useState<PreviewState>({ open: false });
  const [renaming, setRenaming] = useState<FileDialogState>({ open: false });
  const [moving, setMoving] = useState<FileDialogState>({ open: false });
  const [deleting, setDeleting] = useState<FileDialogState>({ open: false });
  const [tagging, setTagging] = useState<TagFileDialogState>({ open: false });
  const [managingTags, setManagingTags] = useState(false);
  const [tagFilterOpened, setTagFilterOpened] = useState(false);
  const tagQuery = useQuery(tagsQueryOptions(library, keys, tagFilterOpened || tag !== undefined));
  const tags = tagQuery.data ?? [];
  const downloads = useFileDownload(keys);
  const uploads = useFileUploads({
    library,
    parentId: folder ?? library.root_node_id,
    keys,
  });

  useEffect(() => {
    if (tag && tagQuery.isSuccess && !tags.some((item) => item.id === tag)) {
      void navigate({ to: "/", search: { library: library.id, folder } });
    }
  }, [folder, library.id, navigate, tag, tagQuery.isSuccess, tags]);

  const lock = () => {
    setPreviewing((state) => ({ ...state, open: false }));
    libraryKeys.lock(library.id);
    queryClient.removeQueries({ queryKey: getNodesListQueryKey(library.id) });
    queryClient.removeQueries({ queryKey: getTagsListQueryKey(library.id) });
  };

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <nav aria-label="Folder path" className="min-w-0">
          <ol className="flex flex-wrap items-center gap-1.5 text-sm">
            <li>
              {path.length > 0 ? (
                <Link to="/" search={{ library: library.id, tag }} className={crumbLinkClass}>
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
                      search={{ library: library.id, folder: crumb.id, tag }}
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
            <TagFilter
              library={library}
              folder={folder}
              tags={tags}
              selected={tag}
              onOpen={() => setTagFilterOpened(true)}
            />
            <TagsButton onClick={() => setManagingTags(true)} />
            <Button variant="outline" onClick={uploads.choose}>
              <HugeiconsIcon icon={Upload01Icon} strokeWidth={2} data-icon="inline-start" />
              Upload files
            </Button>
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
        <div className="relative" {...uploads.dropProps}>
          <UploadInput inputRef={uploads.inputRef} onFiles={uploads.add} />
          <UploadDropOverlay visible={uploads.dragging} />
          <FolderContents
            library={library}
            folder={folder}
            tag={tag}
            keys={keys}
            onPreview={(file) => setPreviewing({ open: true, file })}
            onDownload={(file) => void downloads.download(file)}
            onRename={(file) => setRenaming({ open: true, file })}
            onMove={(file) => setMoving({ open: true, file })}
            onTags={(file) => setTagging({ open: true, file })}
            onDelete={(file) => setDeleting({ open: true, file })}
          />
        </div>
      )}
      {!locked && (
        <UploadQueue
          items={uploads.items}
          onCancel={uploads.cancel}
          onClear={uploads.removeFinished}
        />
      )}
      <FilePreviewDialog
        state={previewing}
        onOpenChange={(open) => setPreviewing((state) => ({ ...state, open }))}
        keys={keys}
        downloading={downloads.downloading}
        onDownload={(file) => void downloads.download(file)}
      />
      <CreateFolderDialog
        open={creating}
        onOpenChange={setCreating}
        library={library}
        parentId={folder}
        keys={keys}
      />
      <RenameFileDialog
        state={renaming}
        onOpenChange={(open) => setRenaming((state) => ({ ...state, open }))}
        library={library}
        keys={keys}
      />
      <MoveFileDialog
        state={moving}
        onOpenChange={(open) => setMoving((state) => ({ ...state, open }))}
        library={library}
        libraries={libraries}
      />
      <DeleteFileDialog
        state={deleting}
        onOpenChange={(open) => setDeleting((state) => ({ ...state, open }))}
        library={library}
      />
      <FileTagsDialog
        state={tagging}
        onOpenChange={(open) => setTagging((state) => ({ ...state, open }))}
        library={library}
        keys={keys}
      />
      <ManageTagsDialog
        open={managingTags}
        onOpenChange={setManagingTags}
        library={library}
        keys={keys}
      />
    </>
  );
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function FolderContents({
  library,
  folder,
  tag,
  keys,
  onPreview,
  onDownload,
  onRename,
  onMove,
  onTags,
  onDelete,
}: {
  library: Library;
  folder?: string;
  tag?: string;
  keys?: LibraryKeys;
  onPreview: (file: FileTarget) => void;
  onDownload: (file: FileTarget) => void;
  onRename: (file: FileTarget) => void;
  onMove: (file: FileTarget) => void;
  onTags: (file: TaggedFileTarget) => void;
  onDelete: (file: FileTarget) => void;
}) {
  const nodes = useInfiniteQuery(nodesQueryOptions(library, folder, tag, keys));

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
          <EmptyTitle>{tag ? "No files have this tag" : "This folder is empty"}</EmptyTitle>
          <EmptyDescription>
            {tag
              ? "Clear the filter or add this tag to a file."
              : "Upload files or create a folder to get started."}
          </EmptyDescription>
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
              <TableHead className="w-52">Tags</TableHead>
              <TableHead className="w-48">Modified</TableHead>
              <TableHead className="w-12 pr-2">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((node) => (
              <TableRow key={node.id}>
                <TableCell className="max-w-0 pl-4">
                  <NodeName library={library} node={node} tag={tag} onPreview={onPreview} />
                </TableCell>
                <TableCell>
                  <div className="flex max-w-52 flex-wrap gap-1">
                    {node.tags.map((tag) => (
                      <TagBadge key={tag.id} tag={tag} />
                    ))}
                  </div>
                </TableCell>
                <TableCell className="text-muted-foreground">
                  <time dateTime={node.updated_at}>
                    {dateFormat.format(new Date(node.updated_at))}
                  </time>
                </TableCell>
                <TableCell className="pr-2 text-right">
                  {node.kind === "file" && node.displayName !== undefined && (
                    <FileActions
                      file={{
                        id: node.id,
                        name: node.displayName,
                        parentId: node.parent_id,
                        tags: node.tags,
                      }}
                      onPreview={onPreview}
                      onDownload={onDownload}
                      onRename={onRename}
                      onMove={onMove}
                      onTags={onTags}
                      onDelete={onDelete}
                    />
                  )}
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

function NodeName({
  library,
  node,
  tag,
  onPreview,
}: {
  library: Library;
  node: LibraryNode;
  tag?: string;
  onPreview: (file: FileTarget) => void;
}) {
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
    const name = node.displayName;
    if (name === undefined) {
      return <span className="flex items-center gap-2">{content}</span>;
    }
    return (
      <button
        type="button"
        onClick={() => onPreview({ id: node.id, name })}
        className="flex max-w-full items-center gap-2 text-left hover:underline"
      >
        {content}
      </button>
    );
  }
  return (
    <Link
      to="/"
      search={{ library: library.id, folder: node.id, tag }}
      className="flex items-center gap-2 font-medium hover:underline"
    >
      {content}
    </Link>
  );
}

function FileActions({
  file,
  onPreview,
  onDownload,
  onRename,
  onMove,
  onTags,
  onDelete,
}: {
  file: TaggedFileTarget;
  onPreview: (file: FileTarget) => void;
  onDownload: (file: FileTarget) => void;
  onRename: (file: FileTarget) => void;
  onMove: (file: FileTarget) => void;
  onTags: (file: TaggedFileTarget) => void;
  onDelete: (file: FileTarget) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon-sm" aria-label={`Actions for ${file.name}`} />}
      >
        <HugeiconsIcon icon={MoreVerticalIcon} strokeWidth={2} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-auto min-w-40">
        <DropdownMenuItem onClick={() => onPreview(file)}>
          <HugeiconsIcon icon={ViewIcon} strokeWidth={2} />
          Preview
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onDownload(file)}>
          <HugeiconsIcon icon={Download04Icon} strokeWidth={2} />
          Download
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onRename(file)}>
          <HugeiconsIcon icon={PencilEdit02Icon} strokeWidth={2} />
          Rename
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onMove(file)}>
          <HugeiconsIcon icon={FolderTransferIcon} strokeWidth={2} />
          Move
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => onTags(file)}>
          <HugeiconsIcon icon={TagsIcon} strokeWidth={2} />
          Change tags
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={() => onDelete(file)}>
          <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
