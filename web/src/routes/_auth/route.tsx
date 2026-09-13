import { createFileRoute, Link, Outlet, redirect } from "@tanstack/react-router";
import logo from "@/assets/logo.svg";
import { Card } from "@/components/ui/card";
import { safeRedirectPath } from "@/lib/redirect";

export const Route = createFileRoute("/_auth")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: safeRedirectPath(search.redirect),
  }),
  beforeLoad: ({ context, search }) => {
    if (context.user) {
      throw redirect({ href: search.redirect ?? "/" });
    }
  },
  component: AuthLayout,
});

function AuthLayout() {
  return (
    <main id="main" className="flex min-h-svh items-center justify-center px-4 py-12">
      <Card className="w-full max-w-sm [--card-spacing:--spacing(6)]">
        <Link
          to="/"
          className="mx-auto rounded-sm focus-visible:outline-2 focus-visible:outline-offset-4"
        >
          <img src={logo} alt="Stocat" className="h-10 w-auto" />
        </Link>
        <Outlet />
      </Card>
    </main>
  );
}
