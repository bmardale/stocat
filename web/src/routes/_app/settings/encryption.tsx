import { queryOptions, useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { z } from "zod";
import { ApiError } from "@/api/fetcher";
import {
  encryptionBundleCreate,
  encryptionBundleGet,
  encryptionBundleUpdate,
  getEncryptionBundleGetQueryKey,
} from "@/api/generated/encryption/encryption";
import type { Bundle } from "@/api/generated/model";
import { useAccountEncryption } from "@/components/account-encryption";
import { useAppForm } from "@/components/form";
import { useRecentAuthentication } from "@/components/recent-authentication";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { FieldError, FieldGroup } from "@/components/ui/field";
import { toast } from "@/components/ui/toast";
import {
  bundleETag,
  changeEncryptionPassword,
  createAccountEncryption,
  IncorrectSecretError,
  recordCheckpoint,
  type RecoveryKey,
  replaceRecoveryKey,
  type UnlockedAccount,
  unlockWithPassword,
  unlockWithRecoveryKey,
} from "@/lib/v2/account-crypto";
import { PasswordRequirementError } from "@/lib/v2/password-kdf";

const bundleQueryOptions = queryOptions({
  queryKey: getEncryptionBundleGetQueryKey(),
  queryFn: ({ signal }) => encryptionBundleGet({ signal }),
});

export const Route = createFileRoute("/_app/settings/encryption")({
  loader: ({ context }) => context.queryClient.ensureQueryData(bundleQueryOptions),
  component: Encryption,
});

const encryptionPasswordSchema = z
  .string()
  .refine((value) => Array.from(value).length >= 15, "The password is too short.")
  .refine((value) => new TextEncoder().encode(value).length <= 1024, "The password is too long.");

const newPasswordSchema = z
  .object({ password: encryptionPasswordSchema, confirm: z.string() })
  .refine((value) => value.password === value.confirm, {
    message: "The passwords do not match.",
    path: ["confirm"],
  });

function useBundleUpdate() {
  const queryClient = useQueryClient();
  return {
    store: (account: UnlockedAccount, bundle: Bundle) => {
      recordCheckpoint(account, bundle);
      queryClient.setQueryData(bundleQueryOptions.queryKey, bundle);
    },
    // A conflict means that another device changed the bundle. Load the current bundle for the next attempt.
    reloadOnConflict: (error: Error) => {
      if (error instanceof ApiError && error.status === 409) {
        void queryClient.invalidateQueries({ queryKey: bundleQueryOptions.queryKey });
      }
    },
  };
}

function Encryption() {
  const { data: bundle } = useSuspenseQuery(bundleQueryOptions);
  const { account } = useAccountEncryption();

  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-2xl font-semibold tracking-tight">Encryption</h2>
        <p className="text-sm text-muted-foreground">
          Your encryption password and recovery key protect your encryption keys. The server never
          receives them. Use a password that differs from your account password.
        </p>
      </div>
      {bundle.state === "legacy" && (
        <Card>
          <CardHeader>
            <CardTitle>Earlier encryption format</CardTitle>
            <CardDescription>
              This account uses an earlier encryption format. Convert it before you use encrypted
              sharing.
            </CardDescription>
          </CardHeader>
        </Card>
      )}
      {bundle.state === "absent" && <SetupEncryption bundle={bundle} />}
      {bundle.state === "configured" && !account && <UnlockEncryption bundle={bundle} />}
      {bundle.state === "configured" && account && (
        <>
          <IdentityCard account={account} />
          <ChangePasswordCard account={account} bundle={bundle} />
          <RecoveryKeyCard account={account} bundle={bundle} />
        </>
      )}
    </section>
  );
}

function SetupEncryption({ bundle }: { bundle: Bundle }) {
  const { unlock } = useAccountEncryption();
  const { store } = useBundleUpdate();
  const { run, dialog } = useRecentAuthentication();
  const [created, setCreated] = useState<Awaited<ReturnType<typeof createAccountEncryption>>>();
  const create = useMutation({
    mutationFn: (password: string) => createAccountEncryption(bundle, password),
    onSuccess: setCreated,
  });
  const finish = useMutation({
    mutationFn: (setup: NonNullable<typeof created>) =>
      run(() => encryptionBundleCreate(setup.request)),
    onSuccess: (next, setup) => {
      store(setup.account, next);
      unlock(setup.account);
      toast.add({ type: "success", description: "Encryption is set up." });
    },
  });
  const form = useAppForm({
    defaultValues: { password: "", confirm: "" },
    validators: { onSubmit: newPasswordSchema },
    onSubmit: ({ value }) => create.mutate(value.password),
  });

  if (created) {
    return (
      <Card>
        {dialog}
        <CardHeader>
          <CardTitle>Save your recovery key</CardTitle>
          <CardDescription>
            The recovery key is the only way to open your encrypted files if you forget the
            encryption password. Stocat shows it only one time.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <RecoveryKeyConfirmation
            recoveryKey={created.recoveryKey}
            action="Finish setup"
            pending={finish.isPending}
            error={finish.error}
            onConfirm={() => finish.mutate(created)}
            onCancel={() => {
              finish.reset();
              setCreated(undefined);
            }}
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Set up encryption</CardTitle>
        <CardDescription>
          Create your encryption keys in this browser. You need encryption before you create an
          encrypted library or receive a shared item.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          noValidate
          className="max-w-xl"
          onSubmit={(event) => {
            event.preventDefault();
            void form.handleSubmit();
          }}
        >
          <FieldGroup>
            <form.AppField name="password">
              {(field) => (
                <field.TextField
                  label="Encryption password"
                  type="password"
                  autoComplete="new-password"
                  description="Use at least 15 characters."
                />
              )}
            </form.AppField>
            <form.AppField name="confirm">
              {(field) => (
                <field.TextField
                  label="Confirm encryption password"
                  type="password"
                  autoComplete="new-password"
                />
              )}
            </form.AppField>
            {create.error && <FieldError>{create.error.message}</FieldError>}
            <Button type="submit" className="self-start" disabled={create.isPending}>
              {create.isPending ? "Creating keys…" : "Create keys"}
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}

function RecoveryKeyConfirmation({
  recoveryKey,
  action,
  pending,
  error,
  onConfirm,
  onCancel,
}: {
  recoveryKey: RecoveryKey;
  action: string;
  pending: boolean;
  error: Error | null;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const form = useAppForm({
    defaultValues: { key: "" },
    validators: {
      onSubmit: z.object({
        key: z
          .string()
          .refine(
            (value) => value.replace(/\s+/g, "") === recoveryKey.key,
            "The recovery key does not match.",
          ),
      }),
    },
    onSubmit: onConfirm,
  });
  const copy = () =>
    navigator.clipboard
      ?.writeText(recoveryKey.key)
      .then(() => toast.add({ type: "success", description: "Recovery key copied." }))
      .catch(() => toast.add({ type: "error", description: "Copy the recovery key by hand." }));

  return (
    <div className="flex max-w-xl flex-col gap-4">
      <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-4">
        <code aria-label="Recovery key" className="font-mono text-sm break-all select-all">
          {recoveryKey.key}
        </code>
        <p className="text-sm text-muted-foreground">
          Checksum <span className="font-mono">{recoveryKey.checksum}</span>
        </p>
        <Button type="button" variant="outline" className="self-start" onClick={() => void copy()}>
          Copy recovery key
        </Button>
      </div>
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void form.handleSubmit();
        }}
      >
        <FieldGroup>
          <form.AppField name="key">
            {(field) => (
              <field.TextField
                label="Enter the recovery key"
                autoComplete="off"
                spellCheck={false}
                description="Enter the key again to confirm that you saved it."
              />
            )}
          </form.AppField>
          {error && <FieldError>{error.message}</FieldError>}
          <div className="flex gap-2">
            <Button type="submit" disabled={pending}>
              {action}
            </Button>
            <Button type="button" variant="outline" disabled={pending} onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </FieldGroup>
      </form>
    </div>
  );
}

function UnlockEncryption({ bundle }: { bundle: Bundle }) {
  const [recovery, setRecovery] = useState(false);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{recovery ? "Recover encryption" : "Unlock encryption"}</CardTitle>
        <CardDescription>
          {recovery
            ? "Enter your recovery key and choose a new encryption password."
            : "Enter your encryption password. Encryption stays unlocked until you lock it, sign out, or reload the page."}
        </CardDescription>
        <CardAction>
          <Button variant="outline" onClick={() => setRecovery((current) => !current)}>
            {recovery ? "Use the encryption password" : "Use the recovery key"}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {recovery ? <RecoveryUnlockForm bundle={bundle} /> : <PasswordUnlockForm bundle={bundle} />}
      </CardContent>
    </Card>
  );
}

function PasswordUnlockForm({ bundle }: { bundle: Bundle }) {
  const { unlock } = useAccountEncryption();
  const open = useMutation({
    mutationFn: async (password: string) => {
      try {
        return await unlockWithPassword(bundle, password);
      } catch (error) {
        throw error instanceof PasswordRequirementError ? new IncorrectSecretError() : error;
      }
    },
    onSuccess: (account) => {
      unlock(account);
      toast.add({ type: "success", description: "Encryption unlocked." });
    },
  });
  const form = useAppForm({
    defaultValues: { password: "" },
    validators: {
      onSubmit: z.object({ password: z.string().min(1, "Enter the encryption password.") }),
    },
    onSubmit: ({ value }) => open.mutate(value.password),
  });

  return (
    <form
      noValidate
      className="max-w-xl"
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <FieldGroup>
        <form.AppField name="password">
          {(field) => (
            <field.TextField
              label="Encryption password"
              type="password"
              autoComplete="current-password"
            />
          )}
        </form.AppField>
        {open.error && <FieldError>{open.error.message}</FieldError>}
        <Button type="submit" className="self-start" disabled={open.isPending}>
          {open.isPending ? "Unlocking…" : "Unlock"}
        </Button>
      </FieldGroup>
    </form>
  );
}

function RecoveryUnlockForm({ bundle }: { bundle: Bundle }) {
  const { unlock } = useAccountEncryption();
  const { store, reloadOnConflict } = useBundleUpdate();
  const { run, dialog } = useRecentAuthentication();
  const recover = useMutation({
    mutationFn: async ({ key, password }: { key: string; password: string }) => {
      const account = await unlockWithRecoveryKey(bundle, key);
      const request = await changeEncryptionPassword(account, bundle, password);
      const headers = { "If-Match": bundleETag(bundle) };
      return { account, next: await run(() => encryptionBundleUpdate(request, { headers })) };
    },
    onSuccess: ({ account, next }) => {
      store(account, next);
      unlock(account);
      toast.add({ type: "success", description: "Encryption password changed." });
    },
    onError: reloadOnConflict,
  });
  const form = useAppForm({
    defaultValues: { key: "", password: "", confirm: "" },
    validators: {
      onSubmit: z
        .object({
          key: z.string().trim().min(1, "Enter the recovery key."),
          password: encryptionPasswordSchema,
          confirm: z.string(),
        })
        .refine((value) => value.password === value.confirm, {
          message: "The passwords do not match.",
          path: ["confirm"],
        }),
    },
    onSubmit: ({ value }) => recover.mutate({ key: value.key, password: value.password }),
  });

  return (
    <form
      noValidate
      className="max-w-xl"
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      {dialog}
      <FieldGroup>
        <form.AppField name="key">
          {(field) => (
            <field.TextField label="Your recovery key" autoComplete="off" spellCheck={false} />
          )}
        </form.AppField>
        <form.AppField name="password">
          {(field) => (
            <field.TextField
              label="New encryption password"
              type="password"
              autoComplete="new-password"
              description="Use at least 15 characters."
            />
          )}
        </form.AppField>
        <form.AppField name="confirm">
          {(field) => (
            <field.TextField
              label="Confirm new encryption password"
              type="password"
              autoComplete="new-password"
            />
          )}
        </form.AppField>
        {recover.error && <FieldError>{recover.error.message}</FieldError>}
        <Button type="submit" className="self-start" disabled={recover.isPending}>
          {recover.isPending ? "Recovering…" : "Recover access"}
        </Button>
      </FieldGroup>
    </form>
  );
}

function IdentityCard({ account }: { account: UnlockedAccount }) {
  const { lock } = useAccountEncryption();

  return (
    <Card>
      <CardHeader>
        <CardTitle>Encryption identity</CardTitle>
        <CardDescription>
          Before a contact trusts your identity, compare this fingerprint with them through another
          channel.
        </CardDescription>
        <CardAction>
          <Button variant="outline" onClick={lock}>
            Lock encryption
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <code
          aria-label="Identity fingerprint"
          className="block max-w-md font-mono text-sm leading-relaxed"
        >
          {account.fingerprint}
        </code>
        <p className="text-sm text-muted-foreground">
          Identity generation {account.generation.toString()}
        </p>
      </CardContent>
    </Card>
  );
}

function ChangePasswordCard({ account, bundle }: { account: UnlockedAccount; bundle: Bundle }) {
  const { store, reloadOnConflict } = useBundleUpdate();
  const { run, dialog } = useRecentAuthentication();
  const change = useMutation({
    mutationFn: async (password: string) => {
      const request = await changeEncryptionPassword(account, bundle, password);
      const headers = { "If-Match": bundleETag(bundle) };
      return run(() => encryptionBundleUpdate(request, { headers }));
    },
    onSuccess: (next) => {
      store(account, next);
      form.reset();
      toast.add({ type: "success", description: "Encryption password changed." });
    },
    onError: reloadOnConflict,
  });
  const form = useAppForm({
    defaultValues: { password: "", confirm: "" },
    validators: { onSubmit: newPasswordSchema },
    onSubmit: ({ value }) => change.mutate(value.password),
  });

  return (
    <Card>
      {dialog}
      <CardHeader>
        <CardTitle>Encryption password</CardTitle>
        <CardDescription>
          A new password does not change your files or your recovery key.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          noValidate
          className="max-w-xl"
          onSubmit={(event) => {
            event.preventDefault();
            void form.handleSubmit();
          }}
        >
          <FieldGroup>
            <form.AppField name="password">
              {(field) => (
                <field.TextField
                  label="New encryption password"
                  type="password"
                  autoComplete="new-password"
                  description="Use at least 15 characters."
                />
              )}
            </form.AppField>
            <form.AppField name="confirm">
              {(field) => (
                <field.TextField
                  label="Confirm new encryption password"
                  type="password"
                  autoComplete="new-password"
                />
              )}
            </form.AppField>
            {change.error && <FieldError>{change.error.message}</FieldError>}
            <Button type="submit" className="self-start" disabled={change.isPending}>
              {change.isPending ? "Changing password…" : "Change encryption password"}
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}

function RecoveryKeyCard({ account, bundle }: { account: UnlockedAccount; bundle: Bundle }) {
  const { store, reloadOnConflict } = useBundleUpdate();
  const { run, dialog } = useRecentAuthentication();
  const [replacement, setReplacement] = useState<Awaited<ReturnType<typeof replaceRecoveryKey>>>();
  const prepare = useMutation({
    mutationFn: () => replaceRecoveryKey(account, bundle),
    onSuccess: setReplacement,
  });
  const commit = useMutation({
    mutationFn: (next: NonNullable<typeof replacement>) => {
      const headers = { "If-Match": bundleETag(bundle) };
      return run(() => encryptionBundleUpdate(next.request, { headers }));
    },
    onSuccess: (next) => {
      store(account, next);
      setReplacement(undefined);
      toast.add({
        type: "success",
        description: "Recovery key replaced. The previous recovery key no longer works.",
      });
    },
    onError: reloadOnConflict,
  });

  return (
    <Card>
      {dialog}
      <CardHeader>
        <CardTitle>Recovery key</CardTitle>
        <CardDescription>
          Replace the recovery key if you lost it or another person saw it. The previous key stops
          working.
        </CardDescription>
        {!replacement && (
          <CardAction>
            <Button variant="outline" disabled={prepare.isPending} onClick={() => prepare.mutate()}>
              Replace recovery key
            </Button>
          </CardAction>
        )}
      </CardHeader>
      {(replacement || prepare.error) && (
        <CardContent>
          {prepare.error && <FieldError>{prepare.error.message}</FieldError>}
          {replacement && (
            <RecoveryKeyConfirmation
              recoveryKey={replacement.recoveryKey}
              action="Save new recovery key"
              pending={commit.isPending}
              error={commit.error}
              onConfirm={() => commit.mutate(replacement)}
              onCancel={() => {
                commit.reset();
                setReplacement(undefined);
              }}
            />
          )}
        </CardContent>
      )}
    </Card>
  );
}
