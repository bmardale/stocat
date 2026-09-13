import { useMutation } from "@tanstack/react-query";
import { createFileRoute, Link, useRouter } from "@tanstack/react-router";
import { z } from "zod";
import { useAuthLogin } from "@/api/generated/auth/auth";
import type { User } from "@/api/generated/model";
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
import { FieldError, FieldGroup, FieldSeparator } from "@/components/ui/field";
import { passkeysSupported, signInWithPasskey } from "@/lib/passkeys";

export const Route = createFileRoute("/_auth/login")({ component: Login });

const loginSchema = z.object({
  email: z.string().trim().pipe(z.email("Enter a valid email address.")),
  password: z.string().min(1, "Enter your password."),
});

function Login() {
  const { redirect } = Route.useSearch();
  const router = useRouter();
  const { setUser } = useAuth();
  const onSuccess = (user: User) => {
    setUser(user);
    router.history.push(redirect ?? "/");
  };
  const login = useAuthLogin({ mutation: { onSuccess } });
  const passkeyLogin = useMutation({ mutationFn: signInWithPasskey, onSuccess });
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
            <Button type="submit" size="lg" disabled={login.isPending || passkeyLogin.isPending}>
              Sign in
            </Button>
            {passkeysSupported() && (
              <>
                <FieldSeparator>Or</FieldSeparator>
                {passkeyLogin.error && <FieldError>{passkeyLogin.error.message}</FieldError>}
                <Button
                  type="button"
                  variant="outline"
                  size="lg"
                  disabled={login.isPending || passkeyLogin.isPending}
                  onClick={() => passkeyLogin.mutate()}
                >
                  Sign in with a passkey
                </Button>
              </>
            )}
          </FieldGroup>
        </form>
      </CardContent>
      <CardFooter className="justify-center gap-1 text-muted-foreground">
        No account?
        <Link to="/register" search={{ redirect }} className="text-primary hover:underline">
          Create one
        </Link>
      </CardFooter>
      <AppVersion className="text-center" />
    </>
  );
}
