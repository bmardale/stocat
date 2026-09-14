import { createFileRoute, Link, useRouter } from "@tanstack/react-router";
import { z } from "zod";
import { queryOptions, useSuspenseQuery } from "@tanstack/react-query";
import { authConfig, getAuthConfigQueryKey, useAuthRegister } from "@/api/generated/auth/auth";
import { useAuth } from "@/components/auth-provider";
import { useAppForm } from "@/components/form";
import { Button } from "@/components/ui/button";
import {
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { FieldError, FieldGroup } from "@/components/ui/field";

const authConfigQueryOptions = queryOptions({
  queryKey: getAuthConfigQueryKey(),
  queryFn: ({ signal }) => authConfig({ signal }),
});

export const Route = createFileRoute("/_auth/register")({
  loader: ({ context }) => context.queryClient.ensureQueryData(authConfigQueryOptions),
  component: Register,
});

// Match the server rules: it counts characters for the minimum and bytes for the maximum.
const nameSchema = z
  .string()
  .trim()
  .min(1, "Enter your name.")
  .max(200, "Use at most 200 characters.");
const emailSchema = z.string().trim().pipe(z.email("Enter a valid email address."));
const passwordSchema = z
  .string()
  .refine((value) => Array.from(value).length >= 15, "The password is too short.")
  .refine((value) => new TextEncoder().encode(value).length <= 1024, "The password is too long.");

function registerSchema(inviteOnly: boolean) {
  return z.object({
    name: nameSchema,
    email: emailSchema,
    password: passwordSchema,
    invite_code: inviteOnly ? z.string().trim().min(1, "Enter your invite code.") : z.string(),
  });
}

function Register() {
  const { redirect } = Route.useSearch();
  const { data: config } = useSuspenseQuery(authConfigQueryOptions);
  const router = useRouter();
  const { setUser } = useAuth();
  const register = useAuthRegister({
    mutation: {
      onSuccess: (user) => {
        setUser(user);
        router.history.push(redirect ?? "/");
      },
    },
  });
  const form = useAppForm({
    defaultValues: { name: "", email: "", password: "", invite_code: "" },
    validators: { onSubmit: registerSchema(config.invite_only) },
    onSubmit: ({ value }) =>
      register.mutate({
        data: {
          name: value.name,
          email: value.email,
          password: value.password,
          invite_code: config.invite_only ? value.invite_code.trim() : undefined,
        },
      }),
  });

  return (
    <>
      <CardHeader className="text-center">
        <CardTitle className="text-xl">Create an account</CardTitle>
        <CardDescription>
          Enter your details{config.invite_only ? " and invite code" : ""} to get started.
        </CardDescription>
      </CardHeader>
      <CardContent>
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
            {config.invite_only && (
              <form.AppField name="invite_code">
                {(field) => (
                  <field.TextField
                    label="Invite code"
                    autoComplete="one-time-code"
                    description="Ask an administrator for an invite code."
                  />
                )}
              </form.AppField>
            )}
            <form.AppField name="password">
              {(field) => (
                <field.TextField
                  label="Password"
                  type="password"
                  autoComplete="new-password"
                  description="Use at least 15 characters."
                />
              )}
            </form.AppField>
            {register.error && <FieldError>{register.error.message}</FieldError>}
            <Button type="submit" size="lg" disabled={register.isPending}>
              Create account
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
      <CardFooter className="justify-center gap-1 text-muted-foreground">
        Already have an account?
        <Link to="/login" search={{ redirect }} className="text-primary hover:underline">
          Sign in
        </Link>
      </CardFooter>
    </>
  );
}
