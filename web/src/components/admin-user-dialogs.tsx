import { useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import type { AdminUser } from "@/api/generated/model";
import {
  getAdminUsersListQueryKey,
  useAdminUsersCreate,
  useAdminUsersDelete,
  useAdminUsersUpdate,
} from "@/api/generated/admin/admin";
import { useAppForm } from "@/components/form";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FieldError, FieldGroup } from "@/components/ui/field";
import { toast } from "@/components/ui/toast";

const passwordSchema = z
  .string()
  .refine((value) => value === "" || Array.from(value).length >= 15, "The password is too short.")
  .refine(
    (value) => value === "" || new TextEncoder().encode(value).length <= 1024,
    "The password is too long.",
  );

const userSchema = z.object({
  name: z.string().trim().min(1, "Enter a name.").max(200, "Use at most 200 characters."),
  email: z
    .string()
    .trim()
    .max(254, "Use at most 254 characters.")
    .pipe(z.email("Enter a valid email address.")),
  password: passwordSchema,
  isAdmin: z.boolean(),
});
const createUserSchema = userSchema.extend({
  password: passwordSchema.refine((value) => value !== "", "Enter a password."),
});

export function AdminUserDialog({
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
      <DialogContent className="sm:max-w-lg">
        <UserForm key={user?.id ?? "new"} user={user} onOpenChange={onOpenChange} />
      </DialogContent>
    </Dialog>
  );
}

function UserForm({
  user,
  onOpenChange,
}: {
  user?: AdminUser;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const create = useAdminUsersCreate();
  const update = useAdminUsersUpdate();
  const form = useAppForm({
    defaultValues: {
      name: user?.name ?? "",
      email: user?.email ?? "",
      password: "",
      isAdmin: user?.is_admin ?? false,
    },
    validators: { onSubmit: user ? userSchema : createUserSchema },
    onSubmit: async ({ value }) => {
      const data = {
        name: value.name.trim(),
        email: value.email.trim(),
        is_admin: value.isAdmin,
        ...(value.password ? { password: value.password } : {}),
      };
      if (user) {
        await update.mutateAsync({ id: user.id, data });
      } else {
        await create.mutateAsync({ data: { ...data, password: value.password } });
      }
      await queryClient.invalidateQueries({ queryKey: getAdminUsersListQueryKey() });
      toast.add({ type: "success", description: user ? "User details saved." : "User created." });
      onOpenChange(false);
    },
  });
  const mutation = user ? update : create;

  return (
    <>
      <DialogHeader>
        <DialogTitle>{user ? "Edit user" : "New user"}</DialogTitle>
        <DialogDescription>
          {user
            ? "Change the account details and permissions."
            : "Create an account without an invite."}
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
          <form.AppField name="name">
            {(field) => <field.TextField label="Name" autoComplete="name" />}
          </form.AppField>
          <form.AppField name="email">
            {(field) => <field.TextField label="Email" type="email" autoComplete="email" />}
          </form.AppField>
          <form.AppField name="password">
            {(field) => (
              <field.TextField
                label={user ? "New password" : "Password"}
                type="password"
                autoComplete="new-password"
                description={
                  user ? "Leave empty to keep the current password." : "Use at least 15 characters."
                }
              />
            )}
          </form.AppField>
          <form.AppField name="isAdmin">
            {(field) => (
              <field.SwitchField
                label="Administrator"
                description="Administrators can manage users, storage, quotas, and registration."
              />
            )}
          </form.AppField>
          {mutation.error && <FieldError>{mutation.error.message}</FieldError>}
        </FieldGroup>
        <DialogFooter className="mt-5">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? "Saving…" : user ? "Save user" : "Create user"}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}

export function AdminUserDeleteDialog({
  open,
  user,
  onOpenChange,
}: {
  open: boolean;
  user?: AdminUser;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const remove = useAdminUsersDelete({
    mutation: {
      onSuccess: async () => {
        await queryClient.invalidateQueries({ queryKey: getAdminUsersListQueryKey() });
        toast.add({ type: "success", description: "User deleted." });
        onOpenChange(false);
      },
    },
  });

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {user?.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            This permanently deletes the account, libraries, files, sessions, and passkeys.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {remove.error && <FieldError>{remove.error.message}</FieldError>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button
            variant="destructive"
            disabled={!user || remove.isPending}
            onClick={() => user && remove.mutate({ id: user.id })}
          >
            {remove.isPending ? "Deleting…" : "Delete user"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
