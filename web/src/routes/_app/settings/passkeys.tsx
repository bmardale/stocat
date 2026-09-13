import { SquareLock02Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { queryOptions, useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { z } from "zod";
import {
  authPasskeysList,
  getAuthPasskeysListQueryKey,
  useAuthPasskeyDelete,
  useAuthPasskeyRename,
} from "@/api/generated/auth/auth";
import type { Passkey } from "@/api/generated/model";
import { useAppForm } from "@/components/form";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FieldError, FieldGroup } from "@/components/ui/field";
import { toast } from "@/components/ui/toast";
import { passkeysSupported, registerPasskey } from "@/lib/passkeys";
import { parseUserAgent } from "@/lib/user-agent";

const passkeysQueryOptions = queryOptions({
  queryKey: getAuthPasskeysListQueryKey(),
  queryFn: ({ signal }) => authPasskeysList({ signal }),
});

export const Route = createFileRoute("/_app/settings/passkeys")({
  loader: ({ context }) => context.queryClient.ensureQueryData(passkeysQueryOptions),
  component: Passkeys,
});

const dateFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

const nameSchema = z
  .string()
  .trim()
  .min(1, "Enter a passkey name.")
  .max(100, "Use at most 100 characters.");

const addSchema = z.object({
  name: nameSchema,
  password: z.string().min(1, "Enter your current password."),
});

const renameSchema = z.object({ name: nameSchema });

function Passkeys() {
  const queryClient = useQueryClient();
  const { data: passkeys } = useSuspenseQuery(passkeysQueryOptions);
  const [adding, setAdding] = useState(false);
  const [renaming, setRenaming] = useState<Passkey>();
  // Keep the mutation pending until the list shows the result.
  const refresh = () => queryClient.invalidateQueries({ queryKey: passkeysQueryOptions.queryKey });
  const remove = useAuthPasskeyDelete({
    mutation: {
      onSuccess: () => {
        toast.add({ type: "success", description: "Passkey deleted." });
        return refresh();
      },
      onError: (error) => toast.add({ type: "error", description: error.message }),
    },
  });
  const supported = passkeysSupported();

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-2xl font-semibold tracking-tight">Passkeys</h2>
        <p className="text-sm text-muted-foreground">
          Sign in with your fingerprint, face, screen lock, or security key instead of your
          password.
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Your passkeys</CardTitle>
          <CardDescription>
            {passkeys.length === 1 ? "1 passkey" : `${passkeys.length} passkeys`}
          </CardDescription>
          <CardAction>
            <Button disabled={!supported} onClick={() => setAdding(true)}>
              Add passkey
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {!supported && (
            <p className="text-sm text-muted-foreground">This browser does not support passkeys.</p>
          )}
          {passkeys.length === 0 ? (
            <p className="text-sm text-muted-foreground">You have no passkeys.</p>
          ) : (
            <ul aria-label="Passkeys" className="divide-y">
              {passkeys.map((passkey) => (
                <PasskeyItem
                  key={passkey.id}
                  passkey={passkey}
                  pending={remove.isPending && remove.variables?.id === passkey.id}
                  onRename={() => setRenaming(passkey)}
                  onDelete={() => remove.mutate({ id: passkey.id })}
                />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="sm:max-w-md">
          <AddPasskeyForm
            onAdded={() => {
              setAdding(false);
              return refresh();
            }}
          />
        </DialogContent>
      </Dialog>
      <Dialog
        open={renaming !== undefined}
        onOpenChange={(open) => !open && setRenaming(undefined)}
      >
        <DialogContent className="sm:max-w-md">
          {renaming && (
            <RenamePasskeyForm
              passkey={renaming}
              onRenamed={() => {
                setRenaming(undefined);
                return refresh();
              }}
            />
          )}
        </DialogContent>
      </Dialog>
    </section>
  );
}

function PasskeyItem({
  passkey,
  pending,
  onRename,
  onDelete,
}: {
  passkey: Passkey;
  pending: boolean;
  onRename: () => void;
  onDelete: () => void;
}) {
  return (
    <li className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
      <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
        <HugeiconsIcon icon={SquareLock02Icon} strokeWidth={2} />
      </div>
      <div className="grid min-w-0 flex-1 gap-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-medium">{passkey.name}</span>
          {passkey.synced && (
            <span className="shrink-0 rounded-md bg-primary/10 px-1.5 py-0.5 text-xs font-medium text-primary">
              Synced
            </span>
          )}
        </div>
        <span className="truncate text-xs text-muted-foreground">
          Added <FormattedDate value={passkey.created_at} />
          {" · "}
          {passkey.last_used_at ? (
            <>
              Last used <FormattedDate value={passkey.last_used_at} />
            </>
          ) : (
            "Never used"
          )}
        </span>
      </div>
      <Button variant="ghost" size="sm" aria-label={`Rename ${passkey.name}`} onClick={onRename}>
        Rename
      </Button>
      <Button
        variant="ghost"
        size="sm"
        aria-label={`Delete ${passkey.name}`}
        disabled={pending}
        onClick={onDelete}
      >
        Delete
      </Button>
    </li>
  );
}

function FormattedDate({ value }: { value: string }) {
  return <time dateTime={value}>{dateFormat.format(new Date(value))}</time>;
}

function AddPasskeyForm({ onAdded }: { onAdded: () => Promise<void> }) {
  const add = useMutation({
    mutationFn: registerPasskey,
    onSuccess: () => {
      toast.add({ type: "success", description: "Passkey added." });
      return onAdded();
    },
  });
  const form = useAppForm({
    defaultValues: { name: parseUserAgent(navigator.userAgent).label, password: "" },
    validators: { onSubmit: addSchema },
    onSubmit: ({ value }) => add.mutate({ name: value.name.trim(), password: value.password }),
  });

  return (
    <form
      noValidate
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <DialogHeader>
        <DialogTitle>Add a passkey</DialogTitle>
        <DialogDescription>
          Confirm your password. Then follow the instructions from your browser.
        </DialogDescription>
      </DialogHeader>
      <FieldGroup>
        <form.AppField name="name">
          {(field) => (
            <field.TextField
              label="Passkey name"
              autoComplete="off"
              description="Use a name that helps you find this passkey later."
            />
          )}
        </form.AppField>
        <form.AppField name="password">
          {(field) => (
            <field.TextField
              label="Current password"
              type="password"
              autoComplete="current-password"
            />
          )}
        </form.AppField>
        {add.error && <FieldError>{add.error.message}</FieldError>}
      </FieldGroup>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" disabled={add.isPending}>
          Continue
        </Button>
      </DialogFooter>
    </form>
  );
}

function RenamePasskeyForm({
  passkey,
  onRenamed,
}: {
  passkey: Passkey;
  onRenamed: () => Promise<void>;
}) {
  const rename = useAuthPasskeyRename({
    mutation: {
      onSuccess: () => {
        toast.add({ type: "success", description: "Passkey renamed." });
        return onRenamed();
      },
    },
  });
  const form = useAppForm({
    defaultValues: { name: passkey.name },
    validators: { onSubmit: renameSchema },
    onSubmit: ({ value }) => rename.mutate({ id: passkey.id, data: { name: value.name.trim() } }),
  });

  return (
    <form
      noValidate
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <DialogHeader>
        <DialogTitle>Rename passkey</DialogTitle>
      </DialogHeader>
      <FieldGroup>
        <form.AppField name="name">
          {(field) => <field.TextField label="Passkey name" autoComplete="off" />}
        </form.AppField>
        {rename.error && <FieldError>{rename.error.message}</FieldError>}
      </FieldGroup>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" disabled={rename.isPending}>
          Save
        </Button>
      </DialogFooter>
    </form>
  );
}
