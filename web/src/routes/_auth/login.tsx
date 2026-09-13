import { createFileRoute, Link, useRouter } from "@tanstack/react-router";
import { z } from "zod";
import { useAuthLogin } from "@/api/generated/auth/auth";
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

export const Route = createFileRoute("/_auth/login")({ component: Login });

const loginSchema = z.object({
  email: z.string().trim().pipe(z.email("Enter a valid email address.")),
  password: z.string().min(1, "Enter your password."),
});

function Login() {
  const { redirect } = Route.useSearch();
  const router = useRouter();
  const { setUser } = useAuth();
  const login = useAuthLogin({
    mutation: {
      onSuccess: (user) => {
        setUser(user);
        router.history.push(redirect ?? "/");
      },
    },
  });
  const form = useAppForm({
    defaultValues: { email: "", password: "" },
    validators: { onSubmit: loginSchema },
    onSubmit: ({ value }) => login.mutate({ data: value }),
  });

  return (
    <>
      <CardHeader className="text-center">
        <CardTitle className="text-xl">Sign in</CardTitle>
        <CardDescription>Enter your email and password.</CardDescription>
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
            <form.AppField name="email">
              {(field) => <field.TextField label="Email" type="email" autoComplete="email" />}
            </form.AppField>
            <form.AppField name="password">
              {(field) => (
                <field.TextField label="Password" type="password" autoComplete="current-password" />
              )}
            </form.AppField>
            {login.error && <FieldError>{login.error.message}</FieldError>}
            <Button type="submit" size="lg" disabled={login.isPending}>
              Sign in
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
      <CardFooter className="justify-center gap-1 text-muted-foreground">
        No account?
        <Link to="/register" search={{ redirect }} className="text-primary hover:underline">
          Create one
        </Link>
      </CardFooter>
    </>
  );
}
