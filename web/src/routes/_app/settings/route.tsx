import { createFileRoute, Link, Outlet } from "@tanstack/react-router";

export const Route = createFileRoute("/_app/settings")({ component: SettingsLayout });

const linkClass =
  "border-b-2 border-transparent px-1 pb-3 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground data-[status=active]:border-primary data-[status=active]:text-foreground";

function SettingsLayout() {
  return (
    <section className="flex max-w-4xl flex-col gap-8">
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-2">
          <h1 className="font-heading text-4xl font-bold tracking-tighter">Settings</h1>
          <p className="leading-relaxed text-muted-foreground">
            Manage your account details, password, and signed-in devices.
          </p>
        </div>
        <nav aria-label="Settings" className="flex gap-6 border-b">
          <Link to="/settings" activeOptions={{ exact: true }} className={linkClass}>
            Account
          </Link>
          <Link to="/settings/sessions" className={linkClass}>
            Sessions
          </Link>
        </nav>
      </div>
      <Outlet />
    </section>
  );
}
