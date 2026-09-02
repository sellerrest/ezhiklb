import { ChevronRight, type LucideIcon } from "lucide-react"
import { NavLink, useLocation } from "react-router"

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from "@/components/ui/sidebar"

export interface NavSubItem {
  title: string
  url: string
  icon: LucideIcon
  /** Highlight for paths under `url` (e.g. /nodes/cores/123), not just an exact match. */
  matchPrefix?: boolean
}

export interface NavItem {
  title: string
  url: string
  icon: LucideIcon
  isActive?: boolean
  items?: NavSubItem[]
}

// Adapted from PasarGuard's components/layout/nav-main.tsx: same structure
// (a "Платформа"-labelled group of collapsible items), with react-i18next
// and RBAC gating removed — this fork has one fixed nav list and no
// per-admin permissions.
export function NavMain({ items, label = "Платформа" }: { items: NavItem[]; label?: string }) {
  const location = useLocation()
  const { setOpenMobile } = useSidebar()
  const handleNavigation = () => setOpenMobile(false)

  return (
    <SidebarGroup>
      <SidebarGroupLabel>{label}</SidebarGroupLabel>
      <SidebarMenu>
        {items.map((item) => (
          <Collapsible key={item.title} defaultOpen={item.isActive || location.pathname.startsWith(item.url)}>
            <SidebarMenuItem>
              <CollapsibleTrigger asChild>
                <NavLink to={item.url} onClick={handleNavigation} end={!item.items?.length}>
                  {({ isActive }) => (
                    <SidebarMenuButton tooltip={item.title} isActive={isActive}>
                      <item.icon />
                      <span>{item.title}</span>
                    </SidebarMenuButton>
                  )}
                </NavLink>
              </CollapsibleTrigger>
              {item.items?.length ? (
                <>
                  <CollapsibleTrigger asChild>
                    <SidebarMenuAction className="data-[state=open]:rotate-90">
                      <ChevronRight />
                      <span className="sr-only">Toggle</span>
                    </SidebarMenuAction>
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <SidebarMenuSub>
                      {item.items.map((subItem) => {
                        const base = subItem.url.replace(/\/$/, "")
                        const subActive = location.pathname === subItem.url || (subItem.matchPrefix && (location.pathname === base || location.pathname.startsWith(`${base}/`)))
                        return (
                          <SidebarMenuSubItem key={subItem.title}>
                            <SidebarMenuSubButton asChild className="flex h-8 items-center gap-2" isActive={subActive}>
                              <NavLink to={subItem.url} end={!subItem.matchPrefix} onClick={handleNavigation}>
                                <subItem.icon />
                                <span>{subItem.title}</span>
                              </NavLink>
                            </SidebarMenuSubButton>
                          </SidebarMenuSubItem>
                        )
                      })}
                    </SidebarMenuSub>
                  </CollapsibleContent>
                </>
              ) : null}
            </SidebarMenuItem>
          </Collapsible>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  )
}
