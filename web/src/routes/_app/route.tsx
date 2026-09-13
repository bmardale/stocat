import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { SiteLayout } from "@/components/site-layout";

export const Route = createFileRoute("/_app")({
  beforeLoad: ({ context, location }) => {
    if (!context.user) {
      throw redirect({
        to: "/login",
        search: { redirect: location.href === "/" ? undefined : location.href },
      });
    }
  },
  component: AppLayout,
});

function AppLayout() {
  return (
    <SiteLayout>
      <Outlet />
    </SiteLayout>
  );
}
