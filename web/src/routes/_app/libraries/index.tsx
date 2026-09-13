import { Add01Icon, LibraryIcon, SquareLock02Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { librariesQueryOptions, libraryBackendsQueryOptions } from "@/api/libraries";
import { BackendIcon, CreateLibraryDialog } from "@/components/library-dialogs";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
    ]),
  component: Libraries,
});

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });

function Libraries() {
  const { data: libraries } = useSuspenseQuery(librariesQueryOptions);
  const { data: backends } = useSuspenseQuery(libraryBackendsQueryOptions);
  const [creating, setCreating] = useState(false);
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
                <TableHead className="w-16 pr-4">
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
                    <Link
                      to="/"
                      search={{ library: library.id }}
                      aria-label={`Open ${library.name} in Files`}
                      className={buttonVariants({ variant: "ghost", size: "sm" })}
                    >
                      Open
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <CreateLibraryDialog open={creating} backends={backends} onOpenChange={setCreating} />
    </section>
  );
}
