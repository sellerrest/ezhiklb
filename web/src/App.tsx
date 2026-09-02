import { useCallback, useEffect, useState } from "react"
import { BrowserRouter, Navigate, Route, Routes } from "react-router"

import DashboardLayout from "@/components/layout/dashboard-layout"
import { Toaster } from "@/components/ui/sonner"
import { ApiError, api } from "@/lib/api"
import CoreEditorPage from "@/pages/core-editor"
import CoresPage from "@/pages/cores"
import EventsPage from "@/pages/events"
import LoginPage from "@/pages/login"
import NodesPage from "@/pages/nodes"
import SettingsPage from "@/pages/settings"
import StatisticsPage from "@/pages/statistics"
import type { BackendHealth, Core, NodeInfo, OutboundStatusEntry, ServiceStat, Status, SystemSettings } from "@/types"

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [cores, setCores] = useState<Core[]>([])
  const [nodes, setNodes] = useState<NodeInfo[]>([])
  const [health, setHealth] = useState<BackendHealth[]>([])
  const [stats, setStats] = useState<ServiceStat[]>([])
  const [outbounds, setOutbounds] = useState<OutboundStatusEntry[]>([])
  const [settings, setSettings] = useState<SystemSettings>({ panel_port: Number(location.port) || 80, db_backend: "sqlite" })
  const [error, setError] = useState("")

  const load = useCallback(async () => {
    try {
      const [nextStatus, nextCores, nextNodes, nextHealth, nextStats, nextOutbounds, nextSettings] = await Promise.all([
        api.status(), api.cores(), api.nodes(), api.health(), api.stats(), api.outbounds(), api.settings(),
      ])
      setStatus(nextStatus)
      setCores(nextCores)
      setNodes(nextNodes)
      setHealth(nextHealth)
      setStats(nextStats)
      setOutbounds(nextOutbounds)
      setSettings(nextSettings)
      setAuthenticated(true)
      setError("")
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 401) setAuthenticated(false)
      else setError(reason instanceof Error ? reason.message : "Не удалось загрузить данные")
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])
  useEffect(() => {
    if (!authenticated) return
    const timer = window.setInterval(() => void load(), 5000)
    return () => window.clearInterval(timer)
  }, [authenticated, load])

  const logout = async () => {
    await api.logout()
    setAuthenticated(false)
  }

  if (authenticated === null) {
    return (
      <div className="flex min-h-svh items-center justify-center gap-3 text-sm font-semibold">
        <span className="bg-accent flex h-9 w-9 items-center justify-center rounded-lg">◈</span>
        <span>EzhikLB</span>
      </div>
    )
  }
  if (!authenticated) return <LoginPage onSuccess={load} />

  const subtitle = `${status?.version ?? ""} · ${settings.db_backend}`

  return (
    <BrowserRouter>
      <Toaster />
      {error && (
        <div className="border-destructive/30 bg-destructive/10 text-destructive fixed top-2 right-2 left-2 z-[100] flex items-center justify-between gap-3 rounded-lg border px-4 py-2 text-sm">
          <span>{error}</span>
          <button onClick={() => setError("")} aria-label="Закрыть">
            ×
          </button>
        </div>
      )}
      <Routes>
        <Route element={<DashboardLayout subtitle={subtitle} version={status?.version ?? ""} onLogout={() => void logout()} />}>
          <Route path="/" element={<StatisticsPage status={status} nodes={nodes} cores={cores} stats={stats} health={health} outbounds={outbounds} />} />
          <Route path="/nodes" element={<NodesPage nodes={nodes} cores={cores} stats={stats} health={health} onChanged={load} />} />
          <Route path="/cores" element={<CoresPage cores={cores} nodes={nodes} onChanged={load} />} />
          <Route path="/cores/new" element={<CoreEditorPage cores={cores} nodes={nodes} onChanged={load} />} />
          <Route path="/cores/:id" element={<CoreEditorPage cores={cores} nodes={nodes} onChanged={load} />} />
          <Route path="/events" element={<EventsPage nodes={nodes} cores={cores} />} />
          <Route path="/settings" element={<SettingsPage current={settings} status={status} nodes={nodes} />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
