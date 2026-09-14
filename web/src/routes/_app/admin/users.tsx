import { PencilEdit01Icon, UserAccountIcon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
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
import { toast } from "@/components/ui/toast";
import { UserQuotaDialog } from "@/components/user-quota-dialog";
import { quotaFromValue, quotaLabel, quotaValue, quotaValueSchema } from "@/lib/quota";

const usersQueryOptions = queryOptions({
  queryKey: getAdminUsersListQueryKey(),
  queryFn: ({ signal }) => adminUsersList({ signal }),
});

const quotaSettingsQueryOptions = queryOptions({
  queryKey: getAdminQuotaGetQueryKey(),
  queryFn: ({ signal }) => adminQuotaGet({ signal }),
});

const backendsQueryOptions = queryOptions({
  queryKey: getStorageBackendsListQueryKey(),
  queryFn: ({ signal }) => storageBackendsList({ signal }),
});

export const Route = createFileRoute("/_app/admin/users")({
  loader: ({ context }) =>
    Promise.all([
      context.queryClient.ensureQueryData(usersQueryOptions),
      context.queryClient.ensureQueryData(quotaSettingsQueryOptions),
      context.queryClient.ensureQueryData(backendsQueryOptions),
    ]),
  component: Users,
});

function Users() {
  const { data: users } = useSuspenseQuery(usersQueryOptions);
  const { data: settings } = useSuspenseQuery(quotaSettingsQueryOptions);
  const { data: backends } = useSuspenseQuery(backendsQueryOptions);
  const [editing, setEditing] = useState<AdminUser>();
  const globalLabel = `Global (${quotaLabel(settings.default_quota, "")})`;

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Users</h1>
        <p className="leading-relaxed text-muted-foreground">
          Set the storage quota of each user. A quota applies to each storage backend separately.
        </p>
      </div>
      <GlobalQuotaForm settings={settings} />
      {users.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <HugeiconsIcon icon={UserAccountIcon} strokeWidth={2} />
            </EmptyMedia>
            <EmptyTitle>No users</EmptyTitle>
            <EmptyDescription>Users appear here after they register.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table aria-label="Users">
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Default quota</TableHead>
                <TableHead>Backend overrides</TableHead>
                <TableHead className="w-12 pr-4">
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
                  <TableCell>{quotaLabel(user.default_quota, globalLabel)}</TableCell>
                  <TableCell>
                    <OverrideList user={user} backends={backends} />
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Edit quotas for ${user.name}`}
                      onClick={() => setEditing(user)}
                    >
                      <HugeiconsIcon icon={PencilEdit01Icon} strokeWidth={2} />
                    </Button>
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
    </section>
  );
}

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
