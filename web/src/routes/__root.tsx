import { createRootRoute, Link, Outlet } from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { ModeToggle } from "@/components/mode-toggle";
import { ThemeProvider } from "@/components/theme-provider";
import { buttonVariants } from "@/components/ui/button";

const RouterDevtools = import.meta.env.DEV
  ? lazy(() =>
      import("@tanstack/react-router-devtools").then((module) => ({
        default: module.TanStackRouterDevtools,
      })),
    )
  : () => null;

export const Route = createRootRoute({
  component: RootLayout,
  notFoundComponent: NotFound,
});

function RootLayout() {
  return (
    <ThemeProvider>
      <header className="flex items-center justify-between border-b px-6 py-6 sm:px-12 lg:px-24">
        <Link
          to="/"
          className="rounded-sm font-heading text-2xl font-bold tracking-tighter focus-visible:outline-2 focus-visible:outline-offset-4"
        >
          stocat
        </Link>
        <ModeToggle />
      </header>
      <main id="main" className="mx-auto max-w-5xl px-6 py-16 sm:py-24 lg:py-40">
        <Outlet />
      </main>
      <Suspense>
        <RouterDevtools />
      </Suspense>
    </ThemeProvider>
  );
}

function NotFound() {
  return (
    <section className="flex flex-col items-start gap-4">
      <h1 className="font-heading text-4xl font-bold tracking-tighter sm:text-6xl">
        Page not found
      </h1>
      <p className="leading-relaxed text-muted-foreground">This page does not exist.</p>
      <Link to="/" className={buttonVariants()}>
        Go home
      </Link>
    </section>
  );
}
