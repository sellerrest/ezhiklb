import { LogOut, ShieldCheck } from "lucide-react"

import { SidebarMenu, SidebarMenuButton, SidebarMenuItem, useSidebar } from "@/components/ui/sidebar"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

// Drastically simplified from PasarGuard's nav-user.tsx: that version shows
// per-admin traffic/user-limit usage and a role badge, none of which exist
// in EzhikLB's single-admin-token model. This keeps only what still applies:
// a status line and a logout action.
export function NavUser({ subtitle, onLogout }: { subtitle: string; onLogout: () => void }) {
  const { state, isMobile } = useSidebar()

  if (state === "collapsed" && !isMobile) {
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <Tooltip>
            <TooltipTrigger asChild>
              <SidebarMenuButton onClick={onLogout} className="justify-center">
                <LogOut />
              </SidebarMenuButton>
            </TooltipTrigger>
            <TooltipContent side="right">Выйти</TooltipContent>
          </Tooltip>
        </SidebarMenuItem>
      </SidebarMenu>
    )
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <div className="border-sidebar-border bg-sidebar-accent/40 mx-0.5 mb-1 flex items-center gap-2.5 rounded-lg border px-2.5 py-2">
          <ShieldCheck className="text-success h-4 w-4 shrink-0" />
          <div className="min-w-0 flex-1">
            <div className="truncate text-xs font-semibold">EzhikLB</div>
            <div className="text-muted-foreground truncate text-[11px]">{subtitle}</div>
          </div>
        </div>
      </SidebarMenuItem>
      <SidebarMenuItem>
        <SidebarMenuButton onClick={onLogout} className="text-muted-foreground hover:text-destructive">
          <LogOut />
          <span>Выйти</span>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
