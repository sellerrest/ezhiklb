import { Activity, LoaderCircle, ScrollText } from "lucide-react"
import { useEffect, useState } from "react"

import PageHeader from "@/components/layout/page-header"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import type { AuditEvent, Core, NodeInfo } from "@/types"

const ACTION_LABELS: Record<string, string> = {
  "core.created": "Ядро создано",
  "core.published": "Ядро опубликовано",
  "core.rolled_back": "Ядро восстановлено",
  "core.deleted": "Ядро удалено",
  "node.created": "Нода добавлена",
  "node.updated": "Нода изменена",
  "node.core_assigned": "Ядро назначено ноде",
  "node.enabled_changed": "Состояние ноды изменено",
  "node.decommission_requested": "Запрошено удаление ноды",
  "node.decommissioned": "Нода очищена и удалена",
  "node.force_deleted": "Нода удалена принудительно (без подтверждения)",
  "node.apply_failed": "Ошибка применения конфигурации",
  "node.apply_recovered": "Конфигурация снова применена",
  "node.update_requested": "Запрошено обновление агента",
  "settings.updated": "Сетевые настройки изменены",
}

function eventLabel(action: string) {
  return ACTION_LABELS[action] ?? action
}

function eventDetails(item: AuditEvent, targetName?: string) {
  try {
    const details = JSON.parse(item.details) as Record<string, unknown>
    const name = typeof details.name === "string" ? details.name : (targetName ?? "")
    const version = typeof details.version === "string" ? details.version : ""
    const error = typeof details.error === "string" ? details.error : ""
    if (error) return name ? `${name} · ${error}` : error
    if (name && version) return `${name} · ${version}`
    if (name || version) return name || version
  } catch {
    // fall through to the stable target ID for legacy events
  }
  return targetName || item.target_id
}

const filters = [
  ["all", "Все"],
  ["nodes", "Ноды"],
  ["profiles", "Ядра"],
  ["errors", "Ошибки"],
] as const

export default function EventsPage({ nodes, cores }: { nodes: NodeInfo[]; cores: Core[] }) {
  const [filter, setFilter] = useState("all")
  const [items, setItems] = useState<AuditEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")

  useEffect(() => {
    setLoading(true)
    setError("")
    void api
      .events(filter)
      .then(setItems)
      .catch((reason) => setError(reason instanceof Error ? reason.message : "Не удалось загрузить журнал"))
      .finally(() => setLoading(false))
  }, [filter])

  const targetName = (item: AuditEvent) => {
    if (item.target_type === "node") return nodes.find((node) => node.id === item.target_id)?.name
    if (item.target_type === "core") return cores.find((core) => core.id === item.target_id)?.name
    return undefined
  }

  return (
    <div className="flex flex-col gap-4 pb-8">
      <PageHeader title="Журналы" description="Действия панели и нод за последние 14 дней." />
      <div className="mx-4 flex gap-2">
        {filters.map(([value, label]) => (
          <Button key={value} variant={filter === value ? "default" : "outline"} size="sm" onClick={() => setFilter(value)}>
            {label}
          </Button>
        ))}
      </div>
      {error && <div className="text-destructive mx-4 text-sm">{error}</div>}
      <div className="mx-4">
        <Card className="p-0">
          {loading ? (
            <div className="text-muted-foreground flex items-center justify-center gap-2 p-10 text-sm">
              <LoaderCircle className="h-4 w-4 animate-spin" /> Загружаем события…
            </div>
          ) : items.length === 0 ? (
            <div className="text-muted-foreground flex flex-col items-center gap-2 p-10 text-center text-sm">
              <ScrollText className="h-8 w-8" />
              <div className="font-medium">Событий пока нет</div>
              <div>Здесь появятся публикации ядер, подключения нод и ошибки применения.</div>
            </div>
          ) : (
            <div className="divide-y">
              {items.map((item) => (
                <div key={item.id} className="flex items-center gap-3 p-3">
                  <div className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${item.action.includes("failed") || item.action.includes("error") ? "bg-destructive/10 text-destructive" : "bg-accent"}`}>
                    <Activity className="h-4 w-4" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold">{eventLabel(item.action)}</div>
                    <div className="text-muted-foreground truncate text-xs">{eventDetails(item, targetName(item))}</div>
                  </div>
                  <time className="text-muted-foreground shrink-0 text-xs" title={new Date(item.created_at).toLocaleString("ru-RU")}>
                    {formatRelative(item.created_at)}
                  </time>
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>
    </div>
  )
}
