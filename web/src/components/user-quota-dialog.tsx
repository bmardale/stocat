import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { z } from "zod";
import type { AdminUser } from "@/api/generated/model";
import { getAdminUsersListQueryKey, useAdminUsersQuotaSet } from "@/api/generated/admin/admin";
import { useAppForm } from "@/components/form";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";

const quota = z
  .string()
  .trim()
  .refine((value) => value === "" || /^\d+$/.test(value), "Enter a whole number of megabytes.");

const quotaSchema = z.object({
  defaultQuota: quota,
  libraries: z.record(z.string(), quota),
});

type QuotaValues = z.infer<typeof quotaSchema>;

function formValues(user: AdminUser): QuotaValues {
  const libraries: Record<string, string> = {};
  for (const library of user.libraries) {
    libraries[library.id] = library.quota_mb === null ? "" : String(library.quota_mb);
  }
  return {
    defaultQuota: user.default_quota_mb === null ? "" : String(user.default_quota_mb),
    libraries,
  };
}

function parseQuota(value: string): number | null {
  const trimmed = value.trim();
  return trimmed === "" ? null : Number(trimmed);
}

export function UserQuotaDialog({
  open,
  user,
  onOpenChange,
}: {
  open: boolean;
  user?: AdminUser;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        {user && <UserQuotaForm key={user.id} user={user} onSaved={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  );
}

function UserQuotaForm({ user, onSaved }: { user: AdminUser; onSaved: () => void }) {
  const queryClient = useQueryClient();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const setUserQuota = useAdminUsersQuotaSet();
  const form = useAppForm({
    defaultValues: formValues(user),
    validators: { onSubmit: quotaSchema },
    onSubmit: async ({ value }) => {
      setSaving(true);
      setError(undefined);
      try {
        await setUserQuota.mutateAsync({
          id: user.id,
          data: {
            default_quota_mb: parseQuota(value.defaultQuota),
            libraries: user.libraries.map((library) => ({
              id: library.id,
              quota_mb: parseQuota(value.libraries[library.id] ?? ""),
            })),
          },
        });
        await queryClient.invalidateQueries({ queryKey: getAdminUsersListQueryKey() });
        onSaved();
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : "The quotas were not saved.");
      } finally {
        setSaving(false);
      }
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Quotas for {user.name}</DialogTitle>
        <DialogDescription>
          Set a default quota for this user. A library override replaces the default for that
          library. An empty value means no limit.
        </DialogDescription>
      </DialogHeader>
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void form.handleSubmit();
        }}
      >
        <FieldGroup>
          <form.AppField name="defaultQuota">
            {(field) => (
              <field.TextField
                label="Default quota (MB)"
                type="number"
                min="0"
                step="1"
                inputMode="numeric"
                placeholder="No limit"
                autoComplete="off"
              />
            )}
          </form.AppField>
          {user.libraries.length > 0 && (
            <FieldSet>
              <FieldLegend variant="label">Library overrides</FieldLegend>
              <FieldDescription>
                Leave a library empty to use the default quota of the user.
              </FieldDescription>
              {user.libraries.map((library) => (
                <form.AppField key={library.id} name={`libraries.${library.id}`}>
                  {(field) => (
                    <field.TextField
                      label={library.name}
                      type="number"
                      min="0"
                      step="1"
                      inputMode="numeric"
                      placeholder="Use default"
                      autoComplete="off"
                    />
                  )}
                </form.AppField>
              ))}
            </FieldSet>
          )}
          {error && <FieldError>{error}</FieldError>}
        </FieldGroup>
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="button" disabled={saving} onClick={() => void form.handleSubmit()}>
          {saving ? "Saving…" : "Save quotas"}
        </Button>
      </DialogFooter>
    </>
  );
}
