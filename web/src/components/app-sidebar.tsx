import { Home01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { Link, useMatchRoute } from "@tanstack/react-router";
import logoMark from "@/assets/logo-mark.svg";
import logo from "@/assets/logo.svg";
import { useAuth } from "@/components/auth-provider";
import { NavUser } from "@/components/nav-user";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar";

export function AppSidebar() {
  const { user } = useAuth();
  const { setOpenMobile } = useSidebar();
  const matchRoute = useMatchRoute();

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              aria-label="Stocat home"
              render={<Link to="/" onClick={() => setOpenMobile(false)} />}
            >
              <img src={logo} alt="" className="h-7 w-auto group-data-[collapsible=icon]:hidden" />
              <img
                src={logoMark}
                alt=""
                className="hidden size-8 object-contain group-data-[collapsible=icon]:block"
              />
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Home"
                isActive={Boolean(matchRoute({ to: "/" }))}
                render={<Link to="/" onClick={() => setOpenMobile(false)} />}
              >
                <HugeiconsIcon icon={Home01Icon} strokeWidth={2} />
                <span>Home</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>{user && <NavUser user={user} />}</SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
