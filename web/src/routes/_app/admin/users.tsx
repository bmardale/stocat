import {
  Add01Icon,
  Delete02Icon,
  PencilEdit01Icon,
  SecurityKeyUsbIcon,
  Settings01Icon,
  UserAccountIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import {
  useInfiniteQuery,
  queryOptions,
  useQueryClient,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { z } from "zod";
import type { AdminUser, Backend, QuotaSettings } from "@/api/generated/model";
import {
  adminQuotaGet,
  adminUsersList,
  getAdminQuotaGetQueryKey,
  getAdminUsersListQueryKey,
  useAdminQuotaSet,
} from "@/api/generated/admin/admin";
import { getStorageUsageListQueryKey } from "@/api/generated/quota/quota";
import {
  getStorageBackendsListQueryKey,
  storageBackendsList,
} from "@/api/generated/storage/storage";
import { useAppForm } from "@/components/form";
import { useAuth } from "@/components/auth-provider";
import {
  AdminUserDeleteDialog,
  AdminUserDialog,
  AdminUserSecurityDialog,
} from "@/components/admin-user-dialogs";
import { QuotaPicker } from "@/components/quota-picker";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { UserQuotaDialog } from "@/components/user-quota-dialog";
import { quotaFromValue, quotaLabel, quotaValue, quotaValueSchema } from "@/lib/quota";
import { formatBytes } from "@/lib/utils";

const userPageSize = 50;

function usersQueryOptions(search?: string) {
  const params = { limit: userPageSize, ...(search ? { search } : {}) };
  return {
    queryKey: getAdminUsersListQueryKey(params),
    queryFn: ({ pageParam, signal }: { pageParam: string | undefined; signal: AbortSignal }) =>
      adminUsersList({ ...params, ...(pageParam ? { cursor: pageParam } : {}) }, { signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page: { next_cursor?: string }) => page.next_cursor,
  };
}

const quotaSettingsQueryOptions = queryOptions({
  queryKey: getAdminQuotaGetQueryKey(),
  queryFn: ({ signal }) => adminQuotaGet({ signal }),
});

const backendsQueryOptions = queryOptions({
  queryKey: getStorageBackendsListQueryKey(),
  queryFn: ({ signal }) => storageBackendsList({ signal }),
});

export const Route = createFileRoute("/_app/admin/users")({
  validateSearch: z.object({ search: z.string().max(200).optional().catch(undefined) }),
  loaderDeps: ({ search }) => ({ search: search.search }),
  loader: ({ context, deps }) =>
    Promise.all([
      context.queryClient.ensureInfiniteQueryData(usersQueryOptions(deps.search)),
      context.queryClient.ensureQueryData(quotaSettingsQueryOptions),
      context.queryClient.ensureQueryData(backendsQueryOptions),
    ]),
  component: Users,
});

function Users() {
  const { user: currentUser } = useAuth();
  const search = Route.useSearch();
  const navigate = useNavigate();
  const usersQuery = useInfiniteQuery(usersQueryOptions(search.search));
  const users = usersQuery.data?.pages.flatMap((page) => page.items) ?? [];
  const { data: settings } = useSuspenseQuery(quotaSettingsQueryOptions);
  const { data: backends } = useSuspenseQuery(backendsQueryOptions);
  const [editing, setEditing] = useState<AdminUser>();
  const [userEditing, setUserEditing] = useState<AdminUser>();
  const [securityEditing, setSecurityEditing] = useState<AdminUser>();
  const [deleting, setDeleting] = useState<AdminUser>();
  const [creating, setCreating] = useState(false);
  const globalLabel = `Global (${quotaLabel(settings.default_quota, "")})`;

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex flex-col gap-2">
          <h1 className="font-heading text-4xl font-bold tracking-tighter">Users</h1>
          <p className="leading-relaxed text-muted-foreground">
            Manage accounts and set storage quotas. A quota applies to each storage backend
            separately.
          </p>
        </div>
        <Button onClick={() => setCreating(true)}>
          <HugeiconsIcon icon={Add01Icon} strokeWidth={2} />
          New user
        </Button>
      </div>
      <div className="flex max-w-xl flex-col gap-2">
        <label htmlFor="user-search" className="text-sm font-medium">
          Search users
        </label>
        <Input
          id="user-search"
          value={search.search ?? ""}
          placeholder="Name, email, or user ID"
          onChange={(event) =>
            void navigate({
              to: "/admin/users",
              search: { search: event.target.value || undefined },
            })
          }
        />
      </div>
      <GlobalQuotaForm settings={settings} />
      {users.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={UserAccountIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>{search.search ? "No matching users" : "No users"}</EmptyTitle>
            <EmptyDescription>
              {search.search ? "Try a different search." : "Users appear here after they register."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="overflow-hidden py-0">
          <Table aria-label="Users">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Storage</TableHead>
                <TableHead>Last active</TableHead>
                <TableHead>Default quota</TableHead>
                <TableHead>Backend overrides</TableHead>
                <TableHead className="w-36 pr-4">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((user) => (
                <TableRow key={user.id}>
                  <TableCell className="pl-4 font-medium">{user.name}</TableCell>
                  <TableCell className="text-muted-foreground">{user.email}</TableCell>
                  <TableCell>
                    <Badge variant={user.is_admin ? "secondary" : "outline"}>
                      {user.is_admin ? "Administrator" : "User"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={user.is_disabled ? "destructive" : "outline"}>
                      {user.is_disabled ? "Suspended" : "Active"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div>{formatBytes(user.storage_used_bytes)}</div>
                    {user.storage_reserved_bytes > 0 && (
                      <div className="text-xs text-muted-foreground">
                        + {formatBytes(user.storage_reserved_bytes)} pending
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {user.last_active_at ? (
                      <time dateTime={user.last_active_at}>
                        {dateFormat.format(new Date(user.last_active_at))}
                      </time>
                    ) : (
                      "Never"
                    )}
                  </TableCell>
                  <TableCell>{quotaLabel(user.default_quota, globalLabel)}</TableCell>
                  <TableCell>
                    <OverrideList user={user} backends={backends} />
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Edit user ${user.name}`}
                        onClick={() => setUserEditing(user)}
                      >
                        <HugeiconsIcon icon={PencilEdit01Icon} strokeWidth={2} />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Edit quotas for ${user.name}`}
                        onClick={() => setEditing(user)}
                      >
                        <HugeiconsIcon icon={Settings01Icon} strokeWidth={2} />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Manage security for ${user.name}`}
                        onClick={() => setSecurityEditing(user)}
                      >
                        <HugeiconsIcon icon={SecurityKeyUsbIcon} strokeWidth={2} />
                      </Button>
                      {user.id !== currentUser?.id && (
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-destructive hover:text-destructive"
                          aria-label={`Delete user ${user.name}`}
                          onClick={() => setDeleting(user)}
                        >
                          <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <UserQuotaDialog
        open={editing !== undefined}
        user={editing}
        backends={backends}
        globalQuota={settings.default_quota}
        onOpenChange={(open) => {
          if (!open) {
            setEditing(undefined);
          }
        }}
      />
      <AdminUserDialog
        open={creating || userEditing !== undefined}
        user={userEditing}
        currentUserID={currentUser?.id}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false);
            setUserEditing(undefined);
          }
        }}
      />
      <AdminUserSecurityDialog
        open={securityEditing !== undefined}
        user={securityEditing}
        onOpenChange={(open) => {
          if (!open) setSecurityEditing(undefined);
        }}
      />
      <AdminUserDeleteDialog
        open={deleting !== undefined}
        user={deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(undefined);
        }}
      />
    </section>
  );
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function OverrideList({ user, backends }: { user: AdminUser; backends: Backend[] }) {
  if (user.backend_quotas.length === 0) {
    return <span className="text-muted-foreground">None</span>;
  }
  return (
    <ul className="flex flex-col gap-0.5">
      {user.backend_quotas.map((quota) => (
        <li key={quota.backend_id}>
          <span className="text-muted-foreground">
            {backends.find((backend) => backend.id === quota.backend_id)?.name ?? quota.backend_id}:
          </span>{" "}
          {quotaLabel(quota, "")}
        </li>
      ))}
    </ul>
  );
}

const globalQuotaSchema = z.object({ defaultQuota: quotaValueSchema });

function GlobalQuotaForm({ settings }: { settings: QuotaSettings }) {
  const queryClient = useQueryClient();
  const setQuota = useAdminQuotaSet();
  const form = useAppForm({
    defaultValues: { defaultQuota: quotaValue(settings.default_quota) },
    validators: { onSubmit: globalQuotaSchema },
    onSubmit: async ({ value }) => {
      try {
        await setQuota.mutateAsync({ data: { default_quota: quotaFromValue(value.defaultQuota) } });
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: getAdminQuotaGetQueryKey() }),
          queryClient.invalidateQueries({ queryKey: getStorageUsageListQueryKey() }),
        ]);
        toast.add({ type: "success", description: "Global default quota saved." });
      } catch (cause) {
        toast.add({
          type: "error",
          description: cause instanceof Error ? cause.message : "The quota was not saved.",
        });
      }
    },
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Global default quota</CardTitle>
        <CardDescription>
          Users without a default quota of their own get this quota on each storage backend.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          noValidate
          className="flex flex-col items-start gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void form.handleSubmit();
          }}
        >
          <form.Field name="defaultQuota">
            {(field) => (
              <QuotaPicker
                id="global-quota"
                label="Default quota"
                value={field.state.value}
                onChange={field.handleChange}
                errors={field.state.meta.errors}
              />
            )}
          </form.Field>
          <Button type="submit" disabled={setQuota.isPending}>
            {setQuota.isPending ? "Saving…" : "Save default quota"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
