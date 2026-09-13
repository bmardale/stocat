import { createFileRoute, Link, useRouter } from "@tanstack/react-router";
import { z } from "zod";
import { useAuthRegister } from "@/api/generated/auth/auth";
import { useAuth } from "@/components/auth-provider";
import { AppVersion } from "@/components/app-version";
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

export const Route = createFileRoute("/_auth/register")({ component: Register });

// Match the server rules: it counts characters for the minimum and bytes for the maximum.
const registerSchema = z.object({
  name: z.string().trim().min(1, "Enter your name.").max(200, "Use at most 200 characters."),
  email: z.string().trim().pipe(z.email("Enter a valid email address.")),
  password: z
    .string()
    .refine((value) => Array.from(value).length >= 15, "The password is too short.")
    .refine((value) => new TextEncoder().encode(value).length <= 1024, "The password is too long."),
});

function Register() {
  const { redirect } = Route.useSearch();
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
    defaultValues: { name: "", email: "", password: "" },
    validators: { onSubmit: registerSchema },
    onSubmit: ({ value }) => register.mutate({ data: value }),
  });

  return (
    <>
      <CardHeader className="text-center">
        <CardTitle className="text-xl">Create an account</CardTitle>
        <CardDescription>Enter your details to get started.</CardDescription>
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
      <AppVersion className="text-center" />
    </>
  );
}
