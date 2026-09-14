import {
  ArrowDataTransferHorizontalIcon,
  ComputerIcon,
  DatabaseIcon,
  File01Icon,
  Folder01Icon,
  HistoryIcon,
  LibraryIcon,
  SecurityKeyUsbIcon,
  TagsIcon,
  UserAccountIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { activityList, getActivityListQueryKey } from "@/api/generated/activity/activity";
import type { AuditEvent } from "@/api/generated/model";
import { useAuth } from "@/components/auth-provider";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { FieldError } from "@/components/ui/field";
import { Skeleton } from "@/components/ui/skeleton";
import { auditActionLabels, auditActorName, auditEventSummary } from "@/lib/audit";
import { parseUserAgent } from "@/lib/user-agent";

export const Route = createFileRoute("/_app/settings/activity")({ component: Activity });

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

// Actions use the form "resource.verb". The icon shows the resource.
const resourceIcons: Record<string, typeof HistoryIcon> = {
  account: UserAccountIcon,
  session: ComputerIcon,
  passkey: SecurityKeyUsbIcon,
  library: LibraryIcon,
  folder: Folder01Icon,
  file: File01Icon,
  tag: TagsIcon,
  replication: ArrowDataTransferHorizontalIcon,
  storage_backend: DatabaseIcon,
  quota: DatabaseIcon,
};

function Activity() {
  const { user } = useAuth();
  const activity = useInfiniteQuery({
    queryKey: getActivityListQueryKey(),
    queryFn: ({ pageParam, signal }) => activityList({ cursor: pageParam }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor,
  });
  const events = activity.data?.pages.flatMap((page) => page.items) ?? [];

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-2xl font-semibold tracking-tight">Activity</h2>
        <p className="text-sm text-muted-foreground">
          Important changes to your account and files from the last 90 days. If you do not recognize
          an event, change your password and revoke your other sessions.
        </p>
      </div>
      {activity.isPending ? (
        <div className="flex flex-col gap-2" aria-busy>
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
        </div>
      ) : activity.isError ? (
        <FieldError>{activity.error.message}</FieldError>
      ) : events.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={HistoryIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>No activity</EmptyTitle>
            <EmptyDescription>Changes to your account and files appear here.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card>
          <CardContent>
            <ul aria-label="Activity" className="divide-y">
              {events.map((event) => (
                <ActivityItem key={event.id} event={event} userID={user?.id} />
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
      {activity.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          disabled={activity.isFetchingNextPage}
          onClick={() => void activity.fetchNextPage()}
        >
          {activity.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
    </section>
  );
}

function ActivityItem({ event, userID }: { event: AuditEvent; userID?: string }) {
  const summary = auditEventSummary(event);
  const meta = [
    event.actor?.id !== userID && `By ${auditActorName(event)}`,
    event.ip_address,
    event.user_agent && parseUserAgent(event.user_agent).label,
  ].filter(Boolean);

  return (
    <li className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
      <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
        <HugeiconsIcon
          icon={resourceIcons[event.action.split(".")[0]] ?? HistoryIcon}
          strokeWidth={2}
        />
      </div>
      <div className="grid min-w-0 flex-1 gap-0.5">
        <span className="font-medium">{auditActionLabels[event.action] ?? event.action}</span>
        {summary && (
          <span className="truncate text-sm text-muted-foreground" title={summary}>
            {summary}
          </span>
        )}
        <span className="truncate text-xs text-muted-foreground" title={event.user_agent}>
          <time dateTime={event.created_at}>{dateFormat.format(new Date(event.created_at))}</time>
          {meta.length > 0 && ` · ${meta.join(" · ")}`}
        </span>
      </div>
    </li>
  );
}
