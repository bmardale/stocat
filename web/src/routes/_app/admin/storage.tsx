import {
  Add01Icon,
  AlertCircleIcon,
  CheckmarkCircle02Icon,
  CloudServerIcon,
  DatabaseIcon,
  Delete02Icon,
  HardDriveIcon,
  MoreHorizontalIcon,
  PencilEdit01Icon,
  PlugSocketIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import type { Backend, ConnectionCheck } from "@/api/generated/model";
import {
  getStorageBackendsListQueryKey,
  storageBackendsList,
  useStorageBackendsCheck,
} from "@/api/generated/storage/storage";
import {
  backendTypeLabel,
  DeleteStorageBackendDialog,
  StorageBackendDialog,
} from "@/components/storage-backend-dialogs";
import { Badge } from "@/components/ui/badge";
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

const backendsQueryOptions = queryOptions({
  queryKey: getStorageBackendsListQueryKey(),
  queryFn: ({ signal }) => storageBackendsList({ signal }),
});

export const Route = createFileRoute("/_app/admin/storage")({
  loader: ({ context }) => context.queryClient.ensureQueryData(backendsQueryOptions),
  component: StorageBackends,
});

// The dialogs keep the backend while they close, so the content does not change during the animation.
type DialogState = { open: boolean; backend?: Backend };

function location(backend: Backend) {
  if (backend.local) {
    return backend.local.root;
  }
  if (backend.s3) {
    const path = [backend.s3.bucket, backend.s3.prefix].filter(Boolean).join("/");
    const host = backend.s3.endpoint ? new URL(backend.s3.endpoint).host : "AWS";
    return `s3://${path} · ${host}`;
  }
  return "";
}

function ConnectionStatus({ check }: { check?: ConnectionCheck | "pending" }) {
  if (check === undefined) {
    return <span className="text-muted-foreground">Not tested</span>;
  }
  if (check === "pending") {
    return <span className="text-muted-foreground">Testing…</span>;
  }
  return (
    <span
      className={`flex max-w-56 items-center gap-1.5 ${check.ok ? "" : "text-destructive"}`}
      title={check.message}
    >
      <HugeiconsIcon
        icon={check.ok ? CheckmarkCircle02Icon : AlertCircleIcon}
        strokeWidth={2}
        className="size-4 shrink-0"
      />
      <span className="truncate">{check.ok ? "Works" : check.message}</span>
    </span>
  );
}

function StorageBackends() {
  const { data: backends } = useSuspenseQuery(backendsQueryOptions);
  const [editor, setEditor] = useState<DialogState>({ open: false });
  const [deletion, setDeletion] = useState<DialogState>({ open: false });
  // Results stay in memory only. A reload clears them.
  const [checks, setChecks] = useState<Record<string, ConnectionCheck | "pending">>({});
  const check = useStorageBackendsCheck();
  const setCheck = (id: string, result?: ConnectionCheck | "pending") =>
    setChecks((current) => {
      const next = { ...current };
      if (result) {
        next[id] = result;
      } else {
        delete next[id];
      }
      return next;
    });
  const testConnection = (backend: Backend) => {
    setCheck(backend.id, "pending");
    check.mutateAsync({ id: backend.id }).then(
      (result) => setCheck(backend.id, result),
      (error: Error) => setCheck(backend.id, { ok: false, message: error.message }),
    );
  };

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="font-heading text-4xl font-bold tracking-tighter">Storage</h1>
          <p className="leading-relaxed text-muted-foreground">
            Storage backends keep the contents of files. Each library uses one backend.
          </p>
        </div>
        <Button onClick={() => setEditor({ open: true })}>
          <HugeiconsIcon icon={Add01Icon} strokeWidth={2} data-icon="inline-start" />
          Add backend
        </Button>
      </div>
      {backends.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={DatabaseIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>No storage backends</EmptyTitle>
            <EmptyDescription>
              Add a local directory or an S3 bucket to store files.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table aria-label="Storage backends">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Location</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Connection</TableHead>
                <TableHead className="w-12 pr-4">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {backends.map((backend) => (
                <TableRow key={backend.id}>
                  <TableCell className="pl-4 font-medium">{backend.name}</TableCell>
                  <TableCell>
                    <span className="flex items-center gap-2">
                      <HugeiconsIcon
                        icon={backend.type === "s3" ? CloudServerIcon : HardDriveIcon}
                        strokeWidth={2}
                        className="size-4 text-muted-foreground"
                      />
                      {backendTypeLabel(backend.type)}
                    </span>
                  </TableCell>
                  <TableCell
                    className="max-w-72 truncate font-mono text-xs"
                    title={location(backend)}
                  >
                    {location(backend)}
                  </TableCell>
                  <TableCell>
                    <Badge variant={backend.enabled ? "secondary" : "outline"}>
                      {backend.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <ConnectionStatus check={checks[backend.id]} />
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger
                        render={
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Actions for ${backend.name}`}
                          />
                        }
                      >
                        <HugeiconsIcon icon={MoreHorizontalIcon} strokeWidth={2} />
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => testConnection(backend)}>
                          <HugeiconsIcon icon={PlugSocketIcon} strokeWidth={2} />
                          Test connection
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={() => {
                            setCheck(backend.id);
                            setEditor({ open: true, backend });
                          }}
                        >
                          <HugeiconsIcon icon={PencilEdit01Icon} strokeWidth={2} />
                          Edit
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() => setDeletion({ open: true, backend })}
                        >
                          <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
                          Delete
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <StorageBackendDialog
        open={editor.open}
        backend={editor.backend}
        onOpenChange={(open) => setEditor((state) => ({ ...state, open }))}
      />
      <DeleteStorageBackendDialog
        open={deletion.open}
        backend={deletion.backend}
        onOpenChange={(open) => setDeletion((state) => ({ ...state, open }))}
      />
    </section>
  );
}
