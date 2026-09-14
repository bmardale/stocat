import { Add01Icon, Delete02Icon, Settings01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import type { InviteCode, RegistrationSettings } from "@/api/generated/model";
import {
  adminConfigGet,
  adminInviteCodesList,
  getAdminConfigGetQueryKey,
  getAdminInviteCodesListQueryKey,
  useAdminConfigSet,
  useAdminInviteCodesCreate,
  useAdminInviteCodesDelete,
} from "@/api/generated/admin/admin";
import { getAuthConfigQueryKey } from "@/api/generated/auth/auth";
import { useAppForm } from "@/components/form";
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
import { FieldError } from "@/components/ui/field";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { toast } from "@/components/ui/toast";

const settingsQueryOptions = queryOptions({
  queryKey: getAdminConfigGetQueryKey(),
  queryFn: ({ signal }) => adminConfigGet({ signal }),
});

const inviteCodesQueryOptions = queryOptions({
  queryKey: getAdminInviteCodesListQueryKey(),
  queryFn: ({ signal }) => adminInviteCodesList({ signal }),
});

export const Route = createFileRoute("/_app/admin/settings")({
  loader: ({ context }) =>
    Promise.all([
      context.queryClient.ensureQueryData(settingsQueryOptions),
      context.queryClient.ensureQueryData(inviteCodesQueryOptions),
    ]),
  component: Settings,
});

function Settings() {
  const { data: settings } = useSuspenseQuery(settingsQueryOptions);
  const { data: inviteCodes } = useSuspenseQuery(inviteCodesQueryOptions);

  return (
    <section className="flex max-w-4xl flex-col gap-6">
      <div className="flex flex-col gap-2">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Registration</h1>
        <p className="leading-relaxed text-muted-foreground">
          Control who can create accounts and manage invitation codes.
        </p>
      </div>
      <RegistrationSettingsForm settings={settings} />
      <InviteCodes codes={inviteCodes} />
    </section>
  );
}

function RegistrationSettingsForm({ settings }: { settings: RegistrationSettings }) {
  const queryClient = useQueryClient();
  const update = useAdminConfigSet({
    mutation: {
      onSuccess: (next) => {
        form.reset({ inviteOnly: next.invite_only });
        void Promise.all([
          queryClient.invalidateQueries({ queryKey: getAdminConfigGetQueryKey() }),
          queryClient.invalidateQueries({ queryKey: getAuthConfigQueryKey() }),
        ]);
        toast.add({ type: "success", description: "Registration settings saved." });
      },
    },
  });
  const form = useAppForm({
    defaultValues: { inviteOnly: settings.invite_only },
    onSubmit: ({ value }) => update.mutate({ data: { invite_only: value.inviteOnly } }),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Registration access</CardTitle>
        <CardDescription>
          Require a one-time invite code before a visitor can create an account.
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
          <form.AppField name="inviteOnly">
            {(field) => (
              <field.SwitchField
                label="Invite-only registration"
                description="New registrations need an unused invite code."
              />
            )}
          </form.AppField>
          {update.error && <FieldError>{update.error.message}</FieldError>}
          <Button type="submit" disabled={update.isPending}>
            {update.isPending ? "Saving…" : "Save registration settings"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function InviteCodes({ codes }: { codes: InviteCode[] }) {
  const queryClient = useQueryClient();
  const [newCode, setNewCode] = useState<string>();
  const create = useAdminInviteCodesCreate({
    mutation: {
      onSuccess: async (code) => {
        setNewCode(code.code);
        await queryClient.invalidateQueries({ queryKey: getAdminInviteCodesListQueryKey() });
        toast.add({ type: "success", description: "Invite code created." });
      },
    },
  });
  const revoke = useAdminInviteCodesDelete({
    mutation: {
      onSuccess: async () => {
        await queryClient.invalidateQueries({ queryKey: getAdminInviteCodesListQueryKey() });
        toast.add({ type: "success", description: "Invite code revoked." });
      },
    },
  });

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1">
            <CardTitle>Invite codes</CardTitle>
            <CardDescription>
              Each code works once. Share a new code with the person you want to invite.
            </CardDescription>
          </div>
          <Button className="shrink-0" disabled={create.isPending} onClick={() => create.mutate()}>
            <HugeiconsIcon icon={Add01Icon} strokeWidth={2} />
            {create.isPending ? "Creating…" : "Create code"}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {create.error && <FieldError>{create.error.message}</FieldError>}
        {newCode && (
          <div className="flex flex-col gap-1 rounded-lg border bg-muted/50 p-3">
            <span className="text-sm font-medium">New invite code</span>
            <code className="font-mono text-lg tracking-widest">{newCode}</code>
            <span className="text-xs text-muted-foreground">
              Copy this code and share it with the new user.
            </span>
          </div>
        )}
        {codes.length === 0 ? (
          <Empty className="border">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <HugeiconsIcon icon={Settings01Icon} strokeWidth={2} />
              </EmptyMedia>
              <EmptyTitle>No invite codes</EmptyTitle>
              <EmptyDescription>Create a code to invite a new user.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <Card className="py-0">
            <Table aria-label="Invite codes">
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Code</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="w-12 pr-4">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {codes.map((code) => (
                  <InviteCodeRow key={code.id} code={code} revoke={revoke} />
                ))}
              </TableBody>
            </Table>
          </Card>
        )}
      </CardContent>
    </Card>
  );
}

function InviteCodeRow({
  code,
  revoke,
}: {
  code: InviteCode;
  revoke: ReturnType<typeof useAdminInviteCodesDelete>;
}) {
  const used = code.used_at !== undefined;
  return (
    <TableRow>
      <TableCell className="pl-4 font-mono font-medium tracking-wider">{code.code}</TableCell>
      <TableCell className="text-muted-foreground">
        <time dateTime={code.created_at}>{dateFormat.format(new Date(code.created_at))}</time>
      </TableCell>
      <TableCell>
        <Badge variant={used ? "outline" : "secondary"}>{used ? "Used" : "Available"}</Badge>
      </TableCell>
      <TableCell className="pr-4 text-right">
        {!used && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Revoke invite code ${code.code}`}
            disabled={revoke.isPending}
            onClick={() => revoke.mutate({ id: code.id })}
          >
            <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}
