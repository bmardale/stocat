import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Link, Outlet, useRouter } from "@tanstack/react-router";
import type { ErrorComponentProps } from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { currentUserQueryOptions } from "@/api/current-user";
import { AuthProvider } from "@/components/auth-provider";
import { SiteLayout } from "@/components/site-layout";
import { ThemeProvider } from "@/components/theme-provider";
import { Button, buttonVariants } from "@/components/ui/button";
import { NOT_FOUND_COPY, pageError } from "@/lib/errors";

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
  const view = pageError(error);

  return (
    <main id="main" className="flex min-h-svh items-center justify-center px-6">
      <section className="flex max-w-md flex-col items-start gap-4">
        {view.status && <StatusBadge>{view.status}</StatusBadge>}
        <h1 className="font-heading text-4xl font-bold tracking-tighter">{view.title}</h1>
        <p className="leading-relaxed text-muted-foreground">{view.description}</p>
        {view.detail && (
          <p className="font-mono text-xs break-all text-muted-foreground/70">{view.detail}</p>
        )}
        <Button onClick={() => void router.invalidate()}>Try again</Button>
      </section>
    </main>
  );
}

function StatusBadge({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded-full border px-2.5 py-0.5 font-mono text-xs text-muted-foreground">
      {children}
    </span>
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
        <StatusBadge>404</StatusBadge>
        <h1 className="font-heading text-4xl font-bold tracking-tighter sm:text-6xl">
          {NOT_FOUND_COPY.title}
        </h1>
        <p className="leading-relaxed text-muted-foreground">{NOT_FOUND_COPY.description}</p>
        <Link to="/" className={buttonVariants()}>
          Go home
        </Link>
      </section>
    </SiteLayout>
  );
}
