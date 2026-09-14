import {
  Audit01Icon,
  DatabaseIcon,
  Folder01Icon,
  LibraryIcon,
  RestoreBinIcon,
  Settings01Icon,
  UserAccountIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { Link, useMatchRoute } from "@tanstack/react-router";
import logoMark from "@/assets/logo-mark.svg";
import logo from "@/assets/logo.svg";
import { useAuth } from "@/components/auth-provider";
import { NavUser } from "@/components/nav-user";
import { StorageUsage } from "@/components/storage-usage";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
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
                tooltip="Files"
                isActive={Boolean(matchRoute({ to: "/" }))}
                render={<Link to="/" onClick={() => setOpenMobile(false)} />}
              >
                <HugeiconsIcon icon={Folder01Icon} strokeWidth={2} />
                <span>Files</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Trash"
                isActive={Boolean(matchRoute({ to: "/trash" }))}
                render={<Link to="/trash" onClick={() => setOpenMobile(false)} />}
              >
                <HugeiconsIcon icon={RestoreBinIcon} strokeWidth={2} />
                <span>Trash</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Libraries"
                isActive={Boolean(matchRoute({ to: "/libraries", fuzzy: true }))}
                render={<Link to="/libraries" onClick={() => setOpenMobile(false)} />}
              >
                <HugeiconsIcon icon={LibraryIcon} strokeWidth={2} />
                <span>Libraries</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>
        {user?.is_admin && (
          <SidebarGroup>
            <SidebarGroupLabel>Administration</SidebarGroupLabel>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton
                  tooltip="Storage"
                  isActive={Boolean(matchRoute({ to: "/admin/storage" }))}
                  render={<Link to="/admin/storage" onClick={() => setOpenMobile(false)} />}
                >
                  <HugeiconsIcon icon={DatabaseIcon} strokeWidth={2} />
                  <span>Storage</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton
                  tooltip="Users"
                  isActive={Boolean(matchRoute({ to: "/admin/users" }))}
                  render={<Link to="/admin/users" onClick={() => setOpenMobile(false)} />}
                >
                  <HugeiconsIcon icon={UserAccountIcon} strokeWidth={2} />
                  <span>Users</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton
                  tooltip="Registration"
                  isActive={Boolean(matchRoute({ to: "/admin/settings" }))}
                  render={<Link to="/admin/settings" onClick={() => setOpenMobile(false)} />}
                >
                  <HugeiconsIcon icon={Settings01Icon} strokeWidth={2} />
                  <span>Registration</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton
                  tooltip="Audit log"
                  isActive={Boolean(matchRoute({ to: "/admin/audit" }))}
                  render={<Link to="/admin/audit" onClick={() => setOpenMobile(false)} />}
                >
                  <HugeiconsIcon icon={Audit01Icon} strokeWidth={2} />
                  <span>Audit log</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroup>
        )}
        <StorageUsage />
      </SidebarContent>
      <SidebarFooter>{user && <NavUser user={user} />}</SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
