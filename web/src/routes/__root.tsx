import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Link, Outlet, useRouter } from "@tanstack/react-router";
import type { ErrorComponentProps } from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { currentUserQueryOptions } from "@/api/current-user";
import { AuthProvider } from "@/components/auth-provider";
import { SiteLayout } from "@/components/site-layout";
import { ThemeProvider } from "@/components/theme-provider";
import { Button, buttonVariants } from "@/components/ui/button";

const RouterDevtools = import.meta.env.DEV
  ? lazy(() =>
      import("@tanstack/react-router-devtools").then((module) => ({
        default: module.TanStackRouterDevtools,
      })),
    )
  : () => null;

export type RouterContext = {
  queryClient: QueryClient;
};

export const Route = createRootRouteWithContext<RouterContext>()({
  beforeLoad: async ({ context }) => ({
    user: await context.queryClient.ensureQueryData(currentUserQueryOptions),
  }),
  component: RootLayout,
  errorComponent: RootError,
  notFoundComponent: NotFound,
});

// The auth context is not available here because loading the current user can fail.
function RootError({ error }: ErrorComponentProps) {
  const router = useRouter();

  return (
    <main id="main" className="flex min-h-svh items-center justify-center px-6">
      <section className="flex max-w-md flex-col items-start gap-4">
        <h1 className="font-heading text-4xl font-bold tracking-tighter">Something went wrong</h1>
        <p className="leading-relaxed text-muted-foreground">
          {error instanceof Error ? error.message : "The application could not load."}
        </p>
        <Button onClick={() => void router.invalidate()}>Try again</Button>
      </section>
    </main>
  );
}

function RootLayout() {
  return (
    <ThemeProvider>
      <AuthProvider>
        <Outlet />
      </AuthProvider>
      <Suspense>
        <RouterDevtools />
      </Suspense>
    </ThemeProvider>
  );
}

function NotFound() {
  return (
    <SiteLayout>
      <section className="flex flex-col items-start gap-4">
        <h1 className="font-heading text-4xl font-bold tracking-tighter sm:text-6xl">
          Page not found
        </h1>
        <p className="leading-relaxed text-muted-foreground">This page does not exist.</p>
        <Link to="/" className={buttonVariants()}>
          Go home
        </Link>
      </section>
    </SiteLayout>
  );
}
