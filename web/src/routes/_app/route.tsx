import { createFileRoute, Outlet } from "@tanstack/react-router";
import { SiteLayout } from "@/components/site-layout";

export const Route = createFileRoute("/_app")({ component: AppLayout });

function AppLayout() {
  return (
    <SiteLayout>
      <Outlet />
    </SiteLayout>
  );
}
