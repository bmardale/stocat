import {
  Add01Icon,
  ArrowRight01Icon,
  Delete02Icon,
  LibraryIcon,
  MoreVerticalIcon,
  PencilEdit02Icon,
  SquareLock02Icon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { librariesQueryOptions, libraryBackendsQueryOptions } from "@/api/libraries";
import { replicationsQueryOptions } from "@/api/replications";
import {
  BackendIcon,
  CreateLibraryDialog,
  DeleteLibraryDialog,
  type LibraryDialogState,
  RenameLibraryDialog,
} from "@/components/library-dialogs";
import { CreateReplicationDialog, ReplicationActions } from "@/components/replication-dialogs";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export const Route = createFileRoute("/_app/libraries/")({
  loader: ({ context }) =>
    Promise.all([
      context.queryClient.ensureQueryData(librariesQueryOptions),
      context.queryClient.ensureQueryData(libraryBackendsQueryOptions),
      context.queryClient.ensureQueryData(replicationsQueryOptions),
    ]),
  component: Libraries,
});

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

function Libraries() {
  const { data: libraries } = useSuspenseQuery(librariesQueryOptions);
  const { data: backends } = useSuspenseQuery(libraryBackendsQueryOptions);
  const { data: replications } = useSuspenseQuery(replicationsQueryOptions);
  const [creating, setCreating] = useState(false);
  const [creatingReplication, setCreatingReplication] = useState(false);
  const [renaming, setRenaming] = useState<LibraryDialogState>({ open: false });
  const [deleting, setDeleting] = useState<LibraryDialogState>({ open: false });
  const newLibraryButton = (
    <Button onClick={() => setCreating(true)}>
      <HugeiconsIcon icon={Add01Icon} strokeWidth={2} data-icon="inline-start" />
      New library
    </Button>
  );

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="font-heading text-4xl font-bold tracking-tighter">Libraries</h1>
          <p className="leading-relaxed text-muted-foreground">
            A library keeps files and folders in one storage backend. Only you can open your
            libraries. Browse their contents on the Files page.
          </p>
        </div>
        {libraries.length > 0 && newLibraryButton}
      </div>
      {libraries.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={LibraryIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>No libraries</EmptyTitle>
            <EmptyDescription>Create a library to store files and folders.</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>{newLibraryButton}</EmptyContent>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table aria-label="Libraries">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Storage backend</TableHead>
                <TableHead>Encryption</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-28 pr-4">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {libraries.map((library) => (
                <TableRow key={library.id}>
                  <TableCell className="max-w-64 truncate pl-4 font-medium">
                    {library.name}
                  </TableCell>
                  <TableCell>
                    <span className="flex items-center gap-2">
                      <BackendIcon
                        type={library.backend.type}
                        className="size-4 text-muted-foreground"
                      />
                      {library.backend.name}
                    </span>
                  </TableCell>
                  <TableCell>
                    {library.encryption_mode === "e2ee" ? (
                      <Badge variant="secondary">
                        <HugeiconsIcon icon={SquareLock02Icon} strokeWidth={2} />
                        End-to-end encrypted
                      </Badge>
                    ) : (
                      <span className="text-muted-foreground">Not encrypted</span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    <time dateTime={library.created_at}>
                      {dateFormat.format(new Date(library.created_at))}
                    </time>
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    <div className="flex justify-end gap-1">
                      <Link
                        to="/"
                        search={{ library: library.id }}
                        aria-label={`Open ${library.name} in Files`}
                        className={buttonVariants({ variant: "ghost", size: "sm" })}
                      >
                        Open
                      </Link>
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={`Actions for ${library.name}`}
                            />
                          }
                        >
                          <HugeiconsIcon icon={MoreVerticalIcon} strokeWidth={2} />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-auto min-w-40">
                          <DropdownMenuItem onClick={() => setRenaming({ open: true, library })}>
                            <HugeiconsIcon icon={PencilEdit02Icon} strokeWidth={2} />
                            Rename
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            variant="destructive"
                            onClick={() => setDeleting({ open: true, library })}
                          >
                            <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
                            Delete
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <CreateLibraryDialog open={creating} backends={backends} onOpenChange={setCreating} />
      <RenameLibraryDialog
        state={renaming}
        onOpenChange={(open) => setRenaming((state) => ({ ...state, open }))}
      />
      <DeleteLibraryDialog
        state={deleting}
        onOpenChange={(open) => setDeleting((state) => ({ ...state, open }))}
      />
      {libraries.length > 1 && (
        <section className="flex flex-col gap-4 pt-2">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div className="space-y-1">
              <h2 className="font-heading text-2xl font-semibold tracking-tight">Replication</h2>
              <p className="text-sm leading-relaxed text-muted-foreground">
                Keep a read-only copy of one library in another storage backend.
              </p>
            </div>
            <Button variant="outline" onClick={() => setCreatingReplication(true)}>
              <HugeiconsIcon icon={Add01Icon} strokeWidth={2} data-icon="inline-start" />
              Set up replication
            </Button>
          </div>
          {replications.length === 0 ? (
            <Card className="flex-row items-center gap-3 px-4 py-4 text-sm text-muted-foreground">
              <HugeiconsIcon icon={ArrowRight01Icon} strokeWidth={2} className="size-4 shrink-0" />
              No library replication is active.
            </Card>
          ) : (
            <Card className="py-0">
              <Table aria-label="Library replications">
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">Source</TableHead>
                    <TableHead>Destination</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Last sync</TableHead>
                    <TableHead className="w-48 pr-4">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {replications.map((replication) => (
                    <TableRow key={replication.id}>
                      <TableCell className="pl-4 font-medium">{replication.source.name}</TableCell>
                      <TableCell>
                        <span className="flex items-center gap-2">
                          <HugeiconsIcon
                            icon={ArrowRight01Icon}
                            strokeWidth={2}
                            className="size-3.5 text-muted-foreground"
                          />
                          {replication.destination.name}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={replication.state === "failed" ? "destructive" : "secondary"}
                        >
                          {replication.state === "ready"
                            ? "Up to date"
                            : replication.state === "syncing"
                              ? "Syncing"
                              : replication.state === "failed"
                                ? "Needs attention"
                                : "Queued"}
                        </Badge>
                        {replication.last_error && (
                          <p className="mt-1 max-w-64 text-xs text-destructive">
                            {replication.last_error}
                          </p>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {replication.last_synced_at
                          ? dateFormat.format(new Date(replication.last_synced_at))
                          : "Not yet"}
                      </TableCell>
                      <TableCell className="pr-4">
                        <ReplicationActions replication={replication} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </Card>
          )}
        </section>
      )}
      <CreateReplicationDialog
        open={creatingReplication}
        libraries={libraries}
        replications={replications}
        onOpenChange={setCreatingReplication}
      />
    </section>
  );
}
