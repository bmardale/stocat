import { Audit01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useInfiniteQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { z } from "zod";
import { adminUsersList, getAdminUsersListQueryKey } from "@/api/generated/admin/admin";
import { auditEventsList, getAuditEventsListQueryKey } from "@/api/generated/audit/audit";
import { AuditEventAction, type AuditEvent } from "@/api/generated/model";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { auditActionLabels, auditActorName, auditEventSummary } from "@/lib/audit";
import { parseUserAgent } from "@/lib/user-agent";

const usersQueryOptions = queryOptions({
  queryKey: getAdminUsersListQueryKey({ limit: 200 }),
  queryFn: ({ signal }) => adminUsersList({ limit: 200 }, { signal }),
});

export const Route = createFileRoute("/_app/admin/audit")({
  validateSearch: z.object({
    action: z.enum(AuditEventAction).optional().catch(undefined),
    user: z.string().optional(),
    from: z.string().optional(),
    to: z.string().optional(),
  }),
  loader: ({ context }) => context.queryClient.ensureQueryData(usersQueryOptions),
  component: AuditLog,
});

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

const selectClass =
  "h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

type Filters = { action?: AuditEventAction; user?: string; from?: string; to?: string };

function AuditLog() {
  const search = Route.useSearch();
  const navigate = useNavigate();
  const { data: usersPage } = useSuspenseQuery(usersQueryOptions);
  const users = usersPage.items;
  const filters: Filters = {
    action: search.action,
    user: search.user,
    from: search.from,
    to: search.to,
  };
  const audit = useInfiniteQuery({
    queryKey: getAuditEventsListQueryKey(filters),
    queryFn: ({ pageParam, signal }) =>
      auditEventsList({ ...filters, cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor,
  });
  const events = audit.data?.pages.flatMap((page) => page.items) ?? [];
  const setFilters = (next: Filters) =>
    void navigate({ to: "/admin/audit", search: { ...filters, ...next } });
  const hasFilters = Object.values(filters).some(Boolean);

  return (
    <section className="flex max-w-6xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Audit log</h1>
        <p className="leading-relaxed text-muted-foreground">
          Review important actions of all users and administrators. The server keeps events for 90
          days.
        </p>
      </div>
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-wrap gap-4">
          <Field className="w-auto min-w-56">
            <FieldLabel htmlFor="audit-action">Action</FieldLabel>
            <select
              id="audit-action"
              className={selectClass}
              value={search.action ?? ""}
              onChange={(event) =>
                setFilters({ action: (event.target.value || undefined) as AuditEventAction })
              }
            >
              <option value="">All actions</option>
              {Object.entries(auditActionLabels).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </Field>
          <Field className="w-auto min-w-56">
            <FieldLabel htmlFor="audit-user">User</FieldLabel>
            <select
              id="audit-user"
              className={selectClass}
              value={search.user ?? ""}
              onChange={(event) => setFilters({ user: event.target.value || undefined })}
            >
              <option value="">All users</option>
              {users.map((user) => (
                <option key={user.id} value={user.id}>
                  {user.name} ({user.email})
                </option>
              ))}
            </select>
          </Field>
          <Field className="w-40">
            <FieldLabel htmlFor="audit-from">From</FieldLabel>
            <Input
              id="audit-from"
              type="date"
              value={dateInputValue(search.from)}
              onChange={(event) => setFilters({ from: startOfDay(event.target.value) })}
            />
          </Field>
          <Field className="w-40">
            <FieldLabel htmlFor="audit-to">To</FieldLabel>
            <Input
              id="audit-to"
              type="date"
              value={dateInputValue(search.to, true)}
              onChange={(event) => setFilters({ to: endOfDay(event.target.value) })}
            />
          </Field>
        </div>
        <div className="flex gap-2">
          {hasFilters && (
            <Button
              variant="ghost"
              onClick={() => void navigate({ to: "/admin/audit", search: {} })}
            >
              Clear filters
            </Button>
          )}
          <Button
            variant="outline"
            render={<a href={auditExportUrl(filters)} download="audit-log.csv" />}
          >
            Export CSV
          </Button>
        </div>
      </div>
      {audit.isPending ? (
        <div className="flex flex-col gap-2" aria-busy>
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
        </div>
      ) : audit.isError ? (
        <FieldError>{audit.error.message}</FieldError>
      ) : events.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={Audit01Icon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>No audit events</EmptyTitle>
            <EmptyDescription>
              {hasFilters
                ? "No events match the filters."
                : "Events appear here after users make changes."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table aria-label="Audit events">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Time</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Details</TableHead>
                <TableHead className="pr-4">Client</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.map((event) => (
                <AuditRow key={event.id} event={event} />
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      {audit.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          disabled={audit.isFetchingNextPage}
          onClick={() => void audit.fetchNextPage()}
        >
          {audit.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
    </section>
  );
}

function dateInputValue(value: string | undefined, exclusive = false) {
  if (!value) return "";
  const date = new Date(value);
  if (exclusive) date.setUTCDate(date.getUTCDate() - 1);
  return date.toISOString().slice(0, 10);
}

function startOfDay(value: string) {
  return value ? `${value}T00:00:00.000Z` : undefined;
}

function endOfDay(value: string) {
  if (!value) return undefined;
  const date = new Date(`${value}T00:00:00.000Z`);
  date.setUTCDate(date.getUTCDate() + 1);
  return date.toISOString();
}

function auditExportUrl(filters: Filters) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value) params.set(key, value);
  }
  const query = params.toString();
  return `/api/v1/admin/audit-events/export${query ? `?${query}` : ""}`;
}

function AuditRow({ event }: { event: AuditEvent }) {
  const client = [event.ip_address, event.user_agent && parseUserAgent(event.user_agent).label]
    .filter(Boolean)
    .join(" · ");

  return (
    <TableRow>
      <TableCell className="pl-4 align-top">
        <time dateTime={event.created_at}>{dateFormat.format(new Date(event.created_at))}</time>
      </TableCell>
      <TableCell className="align-top">
        <div className="font-medium">{auditActorName(event)}</div>
        {event.actor?.email && (
          <div className="text-xs text-muted-foreground">{event.actor.email}</div>
        )}
      </TableCell>
      <TableCell className="align-top">
        <div>{auditActionLabels[event.action] ?? event.action}</div>
        {event.subject && event.subject.id !== event.actor?.id && (
          <div className="text-xs text-muted-foreground">For {event.subject.name}</div>
        )}
      </TableCell>
      <TableCell className="min-w-64 align-top whitespace-normal text-muted-foreground">
        {auditEventSummary(event) || "—"}
      </TableCell>
      <TableCell className="pr-4 align-top text-muted-foreground" title={event.user_agent}>
        {client || "—"}
      </TableCell>
    </TableRow>
  );
}
