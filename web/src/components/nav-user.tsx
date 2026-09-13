import {
  ComputerIcon,
  Logout03Icon,
  Moon02Icon,
  PaintBoardIcon,
  SecurityLockIcon,
  Sun03Icon,
  UnfoldMoreIcon,
} from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { Link } from "@tanstack/react-router";
import type { User } from "@/api/generated/model";
import { useAuth } from "@/components/auth-provider";
import { type Theme, useTheme } from "@/components/theme-provider";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar";

function initials(name: string) {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join("");
}

function UserSummary({ user }: { user: User }) {
  return (
    <>
      <Avatar className="size-8 rounded-lg">
        <AvatarFallback className="rounded-lg">{initials(user.name)}</AvatarFallback>
      </Avatar>
      <div className="grid flex-1 text-left text-sm leading-tight">
        <span className="truncate font-medium">{user.name}</span>
        <span className="truncate text-xs text-muted-foreground">{user.email}</span>
      </div>
    </>
  );
}

export function NavUser({ user }: { user: User }) {
  const { logout } = useAuth();
  const { theme, setTheme } = useTheme();
  const { isMobile, setOpenMobile } = useSidebar();

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <SidebarMenuButton
                size="lg"
                className="data-popup-open:bg-sidebar-accent data-popup-open:text-sidebar-accent-foreground"
              />
            }
          >
            <UserSummary user={user} />
            <HugeiconsIcon icon={UnfoldMoreIcon} strokeWidth={2} className="ml-auto" />
          </DropdownMenuTrigger>
          <DropdownMenuContent
            side={isMobile ? "bottom" : "right"}
            align="end"
            sideOffset={4}
            className="w-(--anchor-width) min-w-56"
          >
            <DropdownMenuGroup>
              <DropdownMenuLabel className="flex items-center gap-2 px-1 py-1.5 font-normal text-foreground">
                <UserSummary user={user} />
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <HugeiconsIcon icon={PaintBoardIcon} strokeWidth={2} />
                Theme
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent>
                <DropdownMenuRadioGroup
                  value={theme}
                  onValueChange={(value: Theme) => setTheme(value)}
                >
                  <DropdownMenuRadioItem closeOnClick value="light">
                    <HugeiconsIcon icon={Sun03Icon} strokeWidth={2} />
                    Light
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem closeOnClick value="dark">
                    <HugeiconsIcon icon={Moon02Icon} strokeWidth={2} />
                    Dark
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem closeOnClick value="system">
                    <HugeiconsIcon icon={ComputerIcon} strokeWidth={2} />
                    System
                  </DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuItem
              render={<Link to="/settings/sessions" onClick={() => setOpenMobile(false)} />}
            >
              <HugeiconsIcon icon={SecurityLockIcon} strokeWidth={2} />
              Sessions
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => void logout()}>
              <HugeiconsIcon icon={Logout03Icon} strokeWidth={2} />
              Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
