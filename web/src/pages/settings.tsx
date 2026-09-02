import { ChevronRight, CircleGauge, Clock3, LoaderCircle, Save, Server, ShieldCheck } from "lucide-react"
import { useEffect, useState } from "react"

import PageHeader from "@/components/layout/page-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api } from "@/lib/api"
import type { NodeInfo, Status, SystemSettings } from "@/types"

export default function SettingsPage({ current, status, nodes }: { current: SystemSettings; status: Status | null; nodes: NodeInfo[] }) {
  const [panelPort, setPanelPort] = useState(current.panel_port)
  const [busy, setBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    if (busy || restarting) return
    setPanelPort(current.panel_port)
  }, [current.panel_port, busy, restarting])

  const errorNodes = nodes.filter((node) => Boolean(node.apply_error) || node.apply_state === "error").length
  const valid = panelPort >= 1024 && panelPort <= 65535
  const save = async () => {
    if (!valid) return
    setBusy(true)
    setError("")
    try {
      await api.updateSettings({ panel_port: panelPort, db_backend: current.db_backend })
      setRestarting(true)
      const target = new URL(location.href)
      target.port = String(panelPort)
      window.setTimeout(() => location.assign(target.toString()), 5000)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Не удалось сохранить настройки")
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 pb-8">
      <PageHeader title="Настройки" description="Сетевые параметры панели и состояние системы." />
      <Card className="mx-4 flex flex-col gap-4 p-5">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold">Порт панели</h2>
            <p className="text-muted-foreground text-sm">Единственный сетевой параметр панели — входящего порта для нод больше не требуется.</p>
          </div>
          <Server className="text-muted-foreground h-5 w-5" />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div className="flex flex-col gap-1.5">
            <Label>Порт web-панели</Label>
            <Input type="number" min={1024} max={65535} value={panelPort} onChange={(e) => setPanelPort(Number(e.target.value))} />
            <span className="text-muted-foreground text-xs">1024–65535; после изменения браузер откроет новый адрес</span>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Интервал опроса и таймаут нод</Label>
            <div className="border-input flex h-9 items-center rounded-lg border px-3 text-sm">
              <span className="text-muted-foreground">Настраивается отдельно на каждом узле</span>
            </div>
          </div>
        </div>
        {!valid && <div className="text-destructive text-sm">Порт — от 1024 до 65535.</div>}
        <div className="bg-accent/40 flex items-center gap-4 rounded-lg border p-3 text-sm">
          <div className="flex items-center gap-2">
            <CircleGauge className="h-4 w-4" />
            <span>Web-панель</span>
            <strong className="font-mono">:{panelPort || "—"}</strong>
          </div>
          <ChevronRight className="text-muted-foreground h-4 w-4" />
          <div className="flex items-center gap-2">
            <Server className="h-4 w-4" />
            <span>База данных</span>
            <strong className="font-mono">{current.db_backend}</strong>
          </div>
        </div>
        <div className="text-muted-foreground flex items-start gap-2 rounded-lg border p-3 text-sm">
          <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0" />
          <span>Панель сама дозванивается на локальный API каждой ноды; входящий порт для нод панели не нужен, поэтому его можно держать за SSH-туннелем или файрволом.</span>
        </div>
        {error && <div className="text-destructive text-sm">{error}</div>}
        <div className="flex items-center gap-3">
          <Button disabled={!valid || busy || restarting} onClick={() => void save()}>
            {restarting ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            {restarting ? "Панель перезапускается…" : busy ? "Сохраняю…" : "Сохранить и перезапустить"}
          </Button>
          {restarting && (
            <span className="text-muted-foreground flex items-center gap-1.5 text-sm">
              <Clock3 className="h-4 w-4" /> Переходим на новый адрес…
            </span>
          )}
        </div>
      </Card>

      <Card className="mx-4 flex flex-col gap-3 p-5">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-muted-foreground text-xs font-semibold uppercase">Система</p>
            <h2 className="text-sm font-semibold">Состояние EzhikLB</h2>
          </div>
          <Badge variant={errorNodes ? "destructive" : "green"}>{errorNodes ? `${errorNodes} ошибок` : "всё работает"}</Badge>
        </div>
        <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <div>
            <div className="text-muted-foreground text-xs">Панель</div>
            <div className="font-semibold">{status?.version || "—"}</div>
          </div>
          <div>
            <div className="text-muted-foreground text-xs">Связь с нодами</div>
            <div className="font-semibold">
              {status?.online_nodes ?? 0} из {status?.nodes ?? 0}
            </div>
          </div>
          <div>
            <div className="text-muted-foreground text-xs">Ядра</div>
            <div className="font-semibold">{status?.cores ?? 0}</div>
          </div>
          <div>
            <div className="text-muted-foreground text-xs">Ошибки применения</div>
            <div className="font-semibold">{errorNodes}</div>
          </div>
        </div>
      </Card>
    </div>
  )
}
