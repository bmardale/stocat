import { PencilEdit01Icon, UserAccountIcon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import type { AdminUser } from "@/api/generated/model";
import { adminUsersList, getAdminUsersListQueryKey } from "@/api/generated/admin/admin";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
import { UserQuotaDialog } from "@/components/user-quota-dialog";

const usersQueryOptions = queryOptions({
  queryKey: getAdminUsersListQueryKey(),
  queryFn: ({ signal }) => adminUsersList({ signal }),
});

export const Route = createFileRoute("/_app/admin/users")({
  loader: ({ context }) => context.queryClient.ensureQueryData(usersQueryOptions),
  component: Users,
});

function quotaLabel(megabytes: number | null) {
  if (megabytes === null) {
    return "No limit";
  }
  if (megabytes >= 1024 && megabytes % 1024 === 0) {
    return `${megabytes / 1024} GB`;
  }
  return `${megabytes} MB`;
}

function Users() {
  const { data: users } = useSuspenseQuery(usersQueryOptions);
  const [editing, setEditing] = useState<AdminUser>();

  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Users</h1>
        <p className="leading-relaxed text-muted-foreground">
          Set the default storage quota of each user. Each library can override the default.
        </p>
      </div>
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
                <TableHead>Libraries</TableHead>
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
                  <TableCell>{quotaLabel(user.default_quota_mb)}</TableCell>
                  <TableCell>{user.libraries.length}</TableCell>
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
        onOpenChange={(open) => {
          if (!open) {
            setEditing(undefined);
          }
        }}
      />
    </section>
  );
}
