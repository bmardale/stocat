import { ComputerIcon, SmartPhone01Icon, Tablet01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import {
  authSessionsList,
  getAuthSessionsListQueryKey,
  useAuthSessionsRevoke,
  useAuthSessionsRevokeOthers,
} from "@/api/generated/auth/auth";
import type { Session } from "@/api/generated/model";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { FieldError } from "@/components/ui/field";
import { type DeviceType, parseUserAgent } from "@/lib/user-agent";

const sessionsQueryOptions = queryOptions({
  queryKey: getAuthSessionsListQueryKey(),
  queryFn: ({ signal }) => authSessionsList({ signal }),
});

export const Route = createFileRoute("/_app/settings/sessions")({
  loader: ({ context }) => context.queryClient.ensureQueryData(sessionsQueryOptions),
  component: Sessions,
});

const deviceIcons: Record<DeviceType, typeof ComputerIcon> = {
  desktop: ComputerIcon,
  mobile: SmartPhone01Icon,
  tablet: Tablet01Icon,
  unknown: ComputerIcon,
};

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function Sessions() {
  const queryClient = useQueryClient();
  const { data: sessions } = useSuspenseQuery(sessionsQueryOptions);
  // Keep the mutation pending until the list shows the result.
  const refresh = () => queryClient.invalidateQueries({ queryKey: sessionsQueryOptions.queryKey });
  const revoke = useAuthSessionsRevoke({ mutation: { onSuccess: refresh } });
  const revokeOthers = useAuthSessionsRevokeOthers({ mutation: { onSuccess: refresh } });
  const otherCount = sessions.filter((session) => !session.current).length;
  const error = revoke.error ?? revokeOthers.error;

  return (
    <section className="flex max-w-3xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Sessions</h1>
        <p className="leading-relaxed text-muted-foreground">
          These devices are signed in to your account. Revoke a session that you do not recognize.
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Active sessions</CardTitle>
          <CardDescription>
            {sessions.length === 1 ? "1 session" : `${sessions.length} sessions`}
          </CardDescription>
          <CardAction>
            <Button
              variant="outline"
              disabled={otherCount === 0 || revokeOthers.isPending}
              onClick={() => revokeOthers.mutate()}
            >
              Sign out other sessions
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          <ul aria-label="Active sessions" className="divide-y">
            {sessions.map((session) => (
              <SessionItem
                key={session.id}
                session={session}
                pending={revoke.isPending && revoke.variables?.id === session.id}
                onRevoke={() => revoke.mutate({ id: session.id })}
              />
            ))}
          </ul>
        </CardContent>
      </Card>
      {error && <FieldError>{error.message}</FieldError>}
    </section>
  );
}

function SessionItem({
  session,
  pending,
  onRevoke,
}: {
  session: Session;
  pending: boolean;
  onRevoke: () => void;
}) {
  const { device, label } = parseUserAgent(session.user_agent);

  return (
    <li className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
      <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
        <HugeiconsIcon icon={deviceIcons[device]} strokeWidth={2} />
      </div>
      <div className="grid min-w-0 flex-1 gap-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-medium" title={session.user_agent}>
            {label}
          </span>
          {session.current && (
            <span className="shrink-0 rounded-md bg-primary/10 px-1.5 py-0.5 text-xs font-medium text-primary">
              This device
            </span>
          )}
        </div>
        <span className="truncate text-xs text-muted-foreground">
          {session.ip_address && `${session.ip_address} · `}Signed in{" "}
          <time dateTime={session.created_at}>
            {dateFormat.format(new Date(session.created_at))}
          </time>
        </span>
      </div>
      {!session.current && (
        <Button
          variant="ghost"
          size="sm"
          aria-label={`Revoke ${label}`}
          disabled={pending}
          onClick={onRevoke}
        >
          Revoke
        </Button>
      )}
    </li>
  );
}
