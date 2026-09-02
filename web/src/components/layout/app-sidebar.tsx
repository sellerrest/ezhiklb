import { Boxes, Hexagon, LayoutDashboard, ScrollText, Server, Settings } from "lucide-react"

import { ThemeToggle } from "@/components/common/theme-toggle"
import { NavMain, type NavItem } from "@/components/layout/nav-main"
import { NavUser } from "@/components/layout/nav-user"
import { VersionBadge } from "@/components/layout/version-badge"
import { Sidebar, SidebarContent, SidebarFooter, SidebarHeader, SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarRail, useSidebar } from "@/components/ui/sidebar"
import { REPO_URL } from "@/hooks/use-version-check"

const nodesGroupItems = [
  { title: "Узлы", url: "/nodes", icon: Server },
  { title: "Ядра", url: "/cores", icon: Boxes },
  { title: "Журналы", url: "/events", icon: ScrollText },
]

const navItems: NavItem[] = [
  { title: "Статистика", url: "/", icon: LayoutDashboard },
  { title: "Узлы", url: nodesGroupItems[0].url, icon: Server, items: nodesGroupItems },
  { title: "Настройки", url: "/settings", icon: Settings },
]

export function AppSidebar({ subtitle, version, onLogout, ...props }: React.ComponentProps<typeof Sidebar> & { subtitle: string; version: string; onLogout: () => void }) {
  const { state } = useSidebar()
  const collapsed = state === "collapsed"

  return (
    <Sidebar variant="sidebar" collapsible="icon" {...props} className="border-sidebar-border p-0">
      <SidebarRail />
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild className="!gap-2">
              <a href={REPO_URL} target="_blank" rel="noreferrer" className="relative">
                <div className="bg-sidebar-accent relative flex h-8 w-8 shrink-0 items-center justify-center rounded-lg">
                  <Hexagon className="h-5 w-5" />
                  {collapsed && (
                    <span className="absolute -right-0.5 -bottom-0.5">
                      <VersionBadge currentVersion={version || null} />
                    </span>
                  )}
                </div>
                {!collapsed && (
                  <div className="flex min-w-0 flex-col overflow-hidden">
                    <span className="truncate text-sm leading-tight font-semibold">EzhikLB</span>
                    <div className="flex min-w-0 items-center gap-1 leading-none">
                      <span className="text-muted-foreground truncate text-xs leading-none">{version ? `v${version}` : "load balancer"}</span>
                      <VersionBadge currentVersion={version || null} />
                    </div>
                  </div>
                )}
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <NavMain items={navItems} />
        <div className="mt-auto flex items-center justify-center px-2 pb-1">{state !== "collapsed" && <ThemeToggle />}</div>
      </SidebarContent>
      <SidebarFooter>
        <NavUser subtitle={subtitle} onLogout={onLogout} />
      </SidebarFooter>
    </Sidebar>
  )
}
