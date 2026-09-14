import { useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import type { AdminUser, Backend, BackendQuota, Quota } from "@/api/generated/model";
import { getAdminUsersListQueryKey, useAdminUsersQuotaSet } from "@/api/generated/admin/admin";
import { getStorageUsageListQueryKey } from "@/api/generated/quota/quota";
import { useAppForm } from "@/components/form";
import { QuotaPicker } from "@/components/quota-picker";
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
import { FieldGroup, FieldSeparator } from "@/components/ui/field";
import { toast } from "@/components/ui/toast";
import {
  quotaFromValue,
  quotaLabel,
  quotaValue,
  quotaValueSchema,
  type QuotaValue,
} from "@/lib/quota";

const userQuotaSchema = z.object({
  defaultQuota: quotaValueSchema,
  backends: z.record(z.string(), quotaValueSchema),
});

type UserQuotaValues = { defaultQuota: QuotaValue; backends: Record<string, QuotaValue> };

function formValues(user: AdminUser, backends: Backend[]): UserQuotaValues {
  const values: UserQuotaValues = { defaultQuota: quotaValue(user.default_quota), backends: {} };
  for (const backend of backends) {
    const override = user.backend_quotas.find((quota) => quota.backend_id === backend.id);
    values.backends[backend.id] = override ? quotaValue(override) : { mode: "inherit", gib: "" };
  }
  return values;
}

export function UserQuotaDialog({
  open,
  user,
  backends,
  globalQuota,
  onOpenChange,
}: {
  open: boolean;
  user?: AdminUser;
  backends: Backend[];
  globalQuota: Quota;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        {user && (
          <UserQuotaForm
            key={user.id}
            user={user}
            backends={backends}
            globalQuota={globalQuota}
            onSaved={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function UserQuotaForm({
  user,
  backends,
  globalQuota,
  onSaved,
}: {
  user: AdminUser;
  backends: Backend[];
  globalQuota: Quota;
  onSaved: () => void;
}) {
  const queryClient = useQueryClient();
  const setUserQuota = useAdminUsersQuotaSet();
  const form = useAppForm({
    defaultValues: formValues(user, backends),
    validators: { onSubmit: userQuotaSchema },
    onSubmit: async ({ value }) => {
      const backendQuotas: BackendQuota[] = backends
        .map((backend) => ({
          backend_id: backend.id,
          ...quotaFromValue(value.backends[backend.id] ?? { mode: "inherit", gib: "" }),
        }))
        .filter((quota) => quota.mode !== "inherit");
      try {
        await setUserQuota.mutateAsync({
          id: user.id,
          data: {
            default_quota: quotaFromValue(value.defaultQuota),
            backend_quotas: backendQuotas,
          },
        });
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: getAdminUsersListQueryKey() }),
          queryClient.invalidateQueries({ queryKey: getStorageUsageListQueryKey() }),
        ]);
        toast.add({ type: "success", description: "Quotas saved." });
        onSaved();
      } catch (cause) {
        toast.add({
          type: "error",
          description: cause instanceof Error ? cause.message : "The quotas were not saved.",
        });
      }
    },
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Quotas for {user.name}</DialogTitle>
        <DialogDescription>
          The default quota applies to each storage backend separately. An override replaces the
          default quota on one backend.
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
          <form.Field name="defaultQuota">
            {(field) => (
              <QuotaPicker
                id={`${user.id}-default`}
                label="Default quota"
                inheritLabel={`Global default (${quotaLabel(globalQuota, "")})`}
                value={field.state.value}
                onChange={field.handleChange}
                errors={field.state.meta.errors}
              />
            )}
          </form.Field>
          {backends.length > 0 && <FieldSeparator />}
          {backends.map((backend) => (
            <form.Field key={backend.id} name={`backends.${backend.id}`}>
              {(field) => (
                <QuotaPicker
                  id={`${user.id}-${backend.id}`}
                  label={backend.name}
                  inheritLabel="Use default"
                  value={field.state.value}
                  onChange={field.handleChange}
                  errors={field.state.meta.errors}
                />
              )}
            </form.Field>
          ))}
        </FieldGroup>
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button
          type="button"
          disabled={setUserQuota.isPending}
          onClick={() => void form.handleSubmit()}
        >
          {setUserQuota.isPending ? "Saving…" : "Save quotas"}
        </Button>
      </DialogFooter>
    </>
  );
}
