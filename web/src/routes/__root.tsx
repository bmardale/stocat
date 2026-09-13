import { createRootRoute, Link, Outlet } from "@tanstack/react-router";
import { lazy, Suspense } from "react";

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
    <>
      <header className="site-header">
        <Link to="/" className="brand">
          stocat
        </Link>
      </header>
      <main id="main">
        <Outlet />
      </main>
      <Suspense>
        <RouterDevtools />
      </Suspense>
    </>
  );
}

function NotFound() {
  return (
    <section>
      <h1>Page not found</h1>
      <p>This page does not exist.</p>
      <Link to="/">Go home</Link>
    </section>
  );
}
