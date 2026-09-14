import { useCallback, useId, useState } from "react";
import { z } from "zod";
import { ApiError } from "@/api/fetcher";
import { useAuthReauthenticate } from "@/api/generated/auth/auth";
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
import { FieldError, FieldGroup } from "@/components/ui/field";

type PendingConfirmation = { resolve: () => void; reject: (reason: Error) => void };

// run repeats an action once after the user confirms the account password.
// The server returns status 403 when a sensitive action needs a recent sign-in.
export function useRecentAuthentication() {
  const [pending, setPending] = useState<PendingConfirmation>();

  const run = useCallback(async <T,>(action: () => Promise<T>) => {
    try {
      return await action();
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 403) throw error;
      await new Promise<void>((resolve, reject) => setPending({ resolve, reject }));
      return action();
    }
  }, []);

  const dialog = (
    <Dialog
      open={pending !== undefined}
      onOpenChange={(open) => {
        if (open) return;
        pending?.reject(new Error("Confirm your account password to continue."));
        setPending(undefined);
      }}
    >
      <DialogContent className="sm:max-w-md">
        {pending && (
          <ConfirmPasswordForm
            onConfirmed={() => {
              pending.resolve();
              setPending(undefined);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );

  return { run, dialog };
}

function ConfirmPasswordForm({ onConfirmed }: { onConfirmed: () => void }) {
  const formId = useId();
  const confirm = useAuthReauthenticate({ mutation: { onSuccess: onConfirmed } });
  const form = useAppForm({
    defaultValues: { password: "" },
    validators: {
      onSubmit: z.object({ password: z.string().min(1, "Enter your account password.") }),
    },
    onSubmit: ({ value }) => confirm.mutate({ data: { password: value.password } }),
  });

  return (
    <>
      <DialogHeader>
        <DialogTitle>Confirm your password</DialogTitle>
        <DialogDescription>
          Enter the password that you use to sign in. Stocat asks for it before a change to your
          encryption keys.
        </DialogDescription>
      </DialogHeader>
      <form
        id={formId}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void form.handleSubmit();
        }}
      >
        <FieldGroup>
          <form.AppField name="password">
            {(field) => (
              <field.TextField
                label="Account password"
                type="password"
                autoComplete="current-password"
                autoFocus
              />
            )}
          </form.AppField>
          {confirm.error && <FieldError>{confirm.error.message}</FieldError>}
        </FieldGroup>
      </form>
      <DialogFooter>
        <DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
        <Button type="submit" form={formId} disabled={confirm.isPending}>
          Confirm password
        </Button>
      </DialogFooter>
    </>
  );
}
