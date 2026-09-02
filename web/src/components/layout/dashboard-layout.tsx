import { Outlet } from "react-router"

import { AppSidebar } from "@/components/layout/app-sidebar"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"

export default function DashboardLayout({ subtitle, version, onLogout }: { subtitle: string; version: string; onLogout: () => void }) {
  return (
    <SidebarProvider>
      <div className="flex w-full flex-col lg:flex-row">
        <AppSidebar subtitle={subtitle} version={version} onLogout={onLogout} />
        <SidebarInset className="dashboard-scroll scroll-smooth">
          <Outlet />
        </SidebarInset>
      </div>
    </SidebarProvider>
  )
}
