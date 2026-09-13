import { createFileRoute } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { z } from "zod";
import {
  getAuthSessionsListQueryKey,
  useAuthAccountUpdate,
  useAuthPasswordChange,
} from "@/api/generated/auth/auth";
import { useAuth } from "@/components/auth-provider";
import { useAppForm } from "@/components/form";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldError, FieldGroup } from "@/components/ui/field";

export const Route = createFileRoute("/_app/settings/")({ component: Account });

const accountSchema = z.object({
  name: z.string().trim().min(1, "Enter your name.").max(200, "Use at most 200 characters."),
  email: z
    .string()
    .trim()
    .max(254, "Use at most 254 characters.")
    .pipe(z.email("Enter a valid email address.")),
});

const newPasswordSchema = z
  .string()
  .refine((value) => Array.from(value).length >= 15, "The password is too short.")
  .refine((value) => new TextEncoder().encode(value).length <= 1024, "The password is too long.");

const passwordSchema = z
  .object({
    current_password: z.string().min(1, "Enter your current password."),
    new_password: newPasswordSchema,
    confirm_password: z.string(),
  })
  .refine((value) => value.new_password === value.confirm_password, {
    message: "The passwords do not match.",
    path: ["confirm_password"],
  });

function Account() {
  const { user } = useAuth();

  if (!user) {
    return null;
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h2 className="font-heading text-2xl font-semibold tracking-tight">Account</h2>
        <p className="text-sm text-muted-foreground">Update your personal and sign-in details.</p>
      </div>
      <ProfileForm name={user.name} email={user.email} />
      <PasswordForm />
    </div>
  );
}

function ProfileForm({ name, email }: { name: string; email: string }) {
  const { setUser } = useAuth();
  const [saved, setSaved] = useState(false);
  const update = useAuthAccountUpdate({
    mutation: {
      onSuccess: (user) => {
        setUser(user);
        form.reset({ name: user.name, email: user.email });
        setSaved(true);
      },
    },
  });
  const form = useAppForm({
    defaultValues: { name, email },
    validators: { onSubmit: accountSchema },
    onSubmit: ({ value }) => {
      setSaved(false);
      update.mutate({ data: { name: value.name.trim(), email: value.email.trim() } });
    },
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile</CardTitle>
        <CardDescription>Change the name and email address on your account.</CardDescription>
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
            <form.AppField name="name">
              {(field) => <field.TextField label="Name" autoComplete="name" />}
            </form.AppField>
            <form.AppField name="email">
              {(field) => <field.TextField label="Email" type="email" autoComplete="email" />}
            </form.AppField>
            {update.error && <FieldError>{update.error.message}</FieldError>}
            {saved && (
              <p role="status" className="text-sm text-muted-foreground">
                Account details saved.
              </p>
            )}
            <Button type="submit" className="self-start" disabled={update.isPending}>
              Save changes
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}

function PasswordForm() {
  const queryClient = useQueryClient();
  const [saved, setSaved] = useState(false);
  const change = useAuthPasswordChange({
    mutation: {
      onSuccess: () => {
        form.reset();
        setSaved(true);
        void queryClient.invalidateQueries({ queryKey: getAuthSessionsListQueryKey() });
      },
    },
  });
  const form = useAppForm({
    defaultValues: { current_password: "", new_password: "", confirm_password: "" },
    validators: { onSubmit: passwordSchema },
    onSubmit: ({ value }) => {
      setSaved(false);
      change.mutate({
        data: { current_password: value.current_password, new_password: value.new_password },
      });
    },
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Password</CardTitle>
        <CardDescription>
          Choose a new password. This change signs out your other devices.
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
            <form.AppField name="current_password">
              {(field) => (
                <field.TextField
                  label="Current password"
                  type="password"
                  autoComplete="current-password"
                />
              )}
            </form.AppField>
            <form.AppField name="new_password">
              {(field) => (
                <field.TextField
                  label="New password"
                  type="password"
                  autoComplete="new-password"
                  description="Use at least 15 characters."
                />
              )}
            </form.AppField>
            <form.AppField name="confirm_password">
              {(field) => (
                <field.TextField
                  label="Confirm new password"
                  type="password"
                  autoComplete="new-password"
                />
              )}
            </form.AppField>
            {change.error && <FieldError>{change.error.message}</FieldError>}
            {saved && (
              <p role="status" className="text-sm text-muted-foreground">
                Password changed. Other devices are signed out.
              </p>
            )}
            <Button type="submit" className="self-start" disabled={change.isPending}>
              Change password
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  );
}
