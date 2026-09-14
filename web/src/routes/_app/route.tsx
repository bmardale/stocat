import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { AccountEncryptionProvider } from "@/components/account-encryption";
import { AppSidebar } from "@/components/app-sidebar";
import { LibraryKeysProvider } from "@/components/library-keys";
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";

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
    <TooltipProvider>
      <LibraryKeysProvider>
        <AccountEncryptionProvider>
          <SidebarProvider>
            <AppSidebar />
            <SidebarInset id="main">
              <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
                <SidebarTrigger />
              </header>
              <div className="flex-1 p-6 sm:p-10">
                <Outlet />
              </div>
            </SidebarInset>
          </SidebarProvider>
        </AccountEncryptionProvider>
      </LibraryKeysProvider>
    </TooltipProvider>
  );
}
