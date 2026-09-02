import { CircleAlert, Download, MemoryStick, MoreVertical, Pencil, Plug, Plus, Power, RefreshCw, RotateCw, Search, Trash2 } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/common/confirm-dialog"
import PageHeader from "@/components/layout/page-header"
import { NodeDetailsDialog, NodeEditDialog, NodeEnrollDialog } from "@/components/node-dialogs"
import { NodeStatsLine } from "@/components/node-stats-line"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { formatDuration, formatPercent, formatRelative, isOlderVersion } from "@/lib/format"
import { applyStateLabel, forceDeleteEligible, nodeVisualState, releaseVersion, updateStageInfo } from "@/lib/node-status"
import type { BackendHealth, Core, NodeInfo, ServiceStat } from "@/types"

function NodeMetricsStrip({ node }: { node: NodeInfo }) {
  const metrics = node.metrics
  if (!metrics) return null
  return (
    <div className="text-muted-foreground mt-1 flex flex-wrap items-center gap-3 text-xs">
      <span className="flex items-center gap-1" title={`RAM ${formatPercent(metrics.ram_used_percent)}`}>
        <MemoryStick className="h-3 w-3" />
        {formatPercent(metrics.ram_used_percent)}
      </span>
      <span title={`CPU; load ${metrics.load_1.toFixed(2)}`}>CPU {formatPercent(metrics.cpu_used_percent)}</span>
    </div>
  )
}

export default function NodesPage({ nodes, cores, stats, health, onChanged }: { nodes: NodeInfo[]; cores: Core[]; stats: ServiceStat[]; health: BackendHealth[]; onChanged: () => Promise<void> }) {
  const [adding, setAdding] = useState(false)
  const [editingNode, setEditingNode] = useState<NodeInfo | null>(null)
  const [selectedNode, setSelectedNode] = useState<NodeInfo | null>(null)
  const [confirmNode, setConfirmNode] = useState<{ node: NodeInfo; action: "disable" | "delete" | "update" | "force-delete" } | null>(null)
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [query, setQuery] = useState("")
  const previousUpdateState = useRef<Record<string, string>>({})
  const filteredNodes = nodes.filter((node) => node.name.toLowerCase().includes(query.toLowerCase()) || node.control_address.toLowerCase().includes(query.toLowerCase()))

  useEffect(() => {
    for (const node of nodes) {
      const previous = previousUpdateState.current[node.id]
      const current = node.update_state ?? "idle"
      if (previous && previous !== current) {
        if (current === "completed") toast.success(`Нода «${node.name}» обновлена до ${node.agent_version || releaseVersion}`)
        else if (current === "error") toast.error(`Не удалось обновить «${node.name}»: ${node.update_error || "ошибка обновления"}`)
      }
      previousUpdateState.current[node.id] = current
    }
  }, [nodes])

  const changeEnabled = async (node: NodeInfo) => {
    if (node.enabled) {
      setConfirmNode({ node, action: "disable" })
      return
    }
    await api.setNodeEnabled(node.id, true)
    await onChanged()
  }

  const syncNode = async (node: NodeInfo) => {
    try {
      await api.syncNode(node.id)
      toast.success(`Синхронизация «${node.name}» запущена`)
      await onChanged()
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Не удалось синхронизировать узел")
    }
  }

  const reconnectNode = async (node: NodeInfo) => {
    try {
      await api.checkNodeConnectivity(node.id)
      toast.success(`«${node.name}» переподключена`)
      await onChanged()
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "Не удалось переподключить узел")
    }
  }

  const toggleSelected = useCallback((id: string) => setSelected((current) => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  }), [])
  const allSelected = filteredNodes.length > 0 && filteredNodes.every((n) => selected.has(n.id))
  const bulkEnable = async (enabled: boolean) => {
    setBusy(true)
    try {
      await Promise.all([...selected].map((id) => api.setNodeEnabled(id, enabled)))
      setSelected(new Set())
      await onChanged()
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 pb-8">
      <PageHeader title="Узлы" description="Подключение, состояние и применение ядер на всех серверах." buttonText="Добавить узел" buttonIcon={Plus} onButtonClick={() => setAdding(true)} />

      <div className="mx-4 flex items-center gap-3">
        <div className="relative max-w-sm flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2" />
          <Input className="pl-9" placeholder="Поиск по названию или адресу" value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
      </div>
      <Separator className="mx-4 w-auto" />

      {filteredNodes.length === 0 && (
        <div className="text-muted-foreground mx-4 py-10 text-center text-sm">{query ? "Ничего не найдено" : "Добавьте первый узел, чтобы начать балансировку."}</div>
      )}

      {filteredNodes.length > 0 && (
        <div className="mx-4">
          <Card className="overflow-hidden p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <Checkbox checked={allSelected} onCheckedChange={(checked) => setSelected(checked ? new Set(filteredNodes.map((n) => n.id)) : new Set())} aria-label="Выбрать все" />
                  </TableHead>
                  <TableHead>Узел</TableHead>
                  <TableHead>Ядро</TableHead>
                  <TableHead>Состояние</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredNodes.map((node) => {
                  const locked = node.status === "deleting"
                  const updateStage = node.update_state ? updateStageInfo[node.update_state] : undefined
                  const updating = Boolean(updateStage)
                  const dotClass = { online: "bg-success", offline: "bg-muted-foreground", applying: "bg-yellow-500", disabled: "bg-muted-foreground" }[nodeVisualState(node)]
                  return (
                    <TableRow key={node.id} className={locked ? "opacity-60" : !node.enabled ? "opacity-50" : ""}>
                      <TableCell>
                        <Checkbox checked={selected.has(node.id)} onCheckedChange={() => toggleSelected(node.id)} aria-label={`Выбрать ${node.name}`} />
                      </TableCell>
                      <TableCell>
                        <button type="button" className="flex items-start gap-3 text-left" onClick={() => setSelectedNode(node)}>
                          <span className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${dotClass}`} />
                          <div className="min-w-0">
                            <div className="text-sm font-semibold">{node.name}</div>
                            <div className="text-muted-foreground font-mono text-xs">
                              {node.control_address}:{node.control_port} · {node.agent_version || "ожидает агента"}
                            </div>
                            <div className="text-xs">
                              {node.status === "online" && node.online_since ? (
                                <span className="text-success font-medium">Онлайн · {formatDuration(Date.now() - new Date(node.online_since).getTime())}</span>
                              ) : node.status === "deleting" ? (
                                <span className="text-muted-foreground">Ожидаем очистку конфигурации на VPS</span>
                              ) : node.last_seen_at ? (
                                <span className="text-muted-foreground">Не в сети · опрошена {formatRelative(node.last_seen_at)}</span>
                              ) : (
                                <span className="text-muted-foreground">Ещё не опрошена</span>
                              )}
                            </div>
                            <NodeMetricsStrip node={node} />
                            <NodeStatsLine node={node} />
                          </div>
                        </button>
                      </TableCell>
                      <TableCell>
                        {locked ? (
                          <span className="text-muted-foreground text-sm">Удаление…</span>
                        ) : (
                          <Select value={node.core_id} onValueChange={(value) => void api.assignCore(node.id, value).then(onChanged)}>
                            <SelectTrigger className="w-[160px]">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {cores.map((core) => (
                                <SelectItem key={core.id} value={core.id}>
                                  {core.name}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col gap-1">
                          <span className="text-sm">{applyStateLabel(node)}</span>
                          {node.apply_error && (
                            <span className="text-destructive max-w-[220px] truncate text-xs" title={node.apply_error}>
                              {node.apply_error}
                            </span>
                          )}
                          {!updating && isOlderVersion(node.agent_version, releaseVersion) && node.status === "online" && (
                            <Button size="sm" variant="outline" onClick={() => setConfirmNode({ node, action: "update" })}>
                              <RefreshCw className="h-3.5 w-3.5" /> Обновить до {releaseVersion}
                            </Button>
                          )}
                          {locked && forceDeleteEligible(node) && (
                            <Button size="sm" variant="outline" onClick={() => setConfirmNode({ node, action: "force-delete" })}>
                              <CircleAlert className="h-3.5 w-3.5" /> Удалить принудительно
                            </Button>
                          )}
                          {updateStage && (
                            <div className="mt-1">
                              <div className="bg-muted h-1.5 w-32 overflow-hidden rounded-full">
                                <div className="bg-primary h-full transition-all" style={{ width: `${updateStage.percent}%` }} />
                              </div>
                              <span className="text-muted-foreground text-[11px]">{updateStage.label}</span>
                            </div>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>
                        {!locked && (
                          <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                              <Button variant="ghost" size="icon" className="h-8 w-8">
                                <MoreVertical className="h-4 w-4" />
                              </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                              <DropdownMenuItem onClick={() => setEditingNode(node)}>
                                <Pencil className="h-4 w-4" /> Изменить
                              </DropdownMenuItem>
                              <DropdownMenuItem onClick={() => void syncNode(node)}>
                                <RotateCw className="h-4 w-4" /> Синхронизировать
                              </DropdownMenuItem>
                              <DropdownMenuItem onClick={() => void reconnectNode(node)}>
                                <Plug className="h-4 w-4" /> Переподключить
                              </DropdownMenuItem>
                              <DropdownMenuItem onClick={() => setConfirmNode({ node, action: "update" })}>
                                <Download className="h-4 w-4" /> Обновить
                              </DropdownMenuItem>
                              <DropdownMenuItem onClick={() => void changeEnabled(node)}>
                                <Power className="h-4 w-4" /> {node.enabled ? "Выключить" : "Включить"}
                              </DropdownMenuItem>
                              <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={() => setConfirmNode({ node, action: "delete" })}>
                                <Trash2 className="h-4 w-4" /> Удалить
                              </DropdownMenuItem>
                            </DropdownMenuContent>
                          </DropdownMenu>
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </Card>
          {selected.size > 0 && (
            <div className="mt-3 flex items-center gap-2 text-sm">
              <span className="text-muted-foreground">{selected.size} выбрано</span>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulkEnable(true)}>
                Включить
              </Button>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulkEnable(false)}>
                Выключить
              </Button>
            </div>
          )}
        </div>
      )}

      {adding && <NodeEnrollDialog cores={cores} onClose={() => setAdding(false)} onSaved={async () => { setAdding(false); await onChanged() }} />}
      {editingNode && <NodeEditDialog node={editingNode} cores={cores} onClose={() => setEditingNode(null)} onSaved={async () => { setEditingNode(null); await onChanged() }} />}
      {selectedNode && (
        <NodeDetailsDialog
          node={nodes.find((n) => n.id === selectedNode.id) ?? selectedNode}
          core={cores.find((c) => c.id === selectedNode.core_id)}
          stats={stats.filter((item) => item.node_id === selectedNode.id)}
          health={health.filter((item) => item.node_id === selectedNode.id)}
          onClose={() => setSelectedNode(null)}
        />
      )}
      {confirmNode && (
        <ConfirmDialog
          title={confirmNode.action === "delete" ? "Удалить ноду?" : confirmNode.action === "force-delete" ? "Удалить принудительно?" : confirmNode.action === "update" ? "Обновить агент ноды?" : "Выключить ноду?"}
          description={
            confirmNode.action === "delete"
              ? `EzhikLB очистит свои маршруты на «${confirmNode.node.name}», остановит агент и уберёт ноду из панели.`
              : confirmNode.action === "force-delete"
                ? `Нода «${confirmNode.node.name}» не подтвердила удаление и не отвечает уже больше минуты. Панель уберёт запись без подтверждения от неё.`
                : confirmNode.action === "update"
                  ? `Нода «${confirmNode.node.name}» проверит официальный релиз ${releaseVersion}, заменит агент и автоматически перезапустит его.`
                  : `Нода «${confirmNode.node.name}» перестанет принимать новые настройки панели. Текущая балансировка продолжит работать.`
          }
          confirmLabel={confirmNode.action === "delete" ? "Удалить ноду" : confirmNode.action === "force-delete" ? "Удалить принудительно" : confirmNode.action === "update" ? "Обновить ноду" : "Выключить"}
          danger={confirmNode.action === "delete" || confirmNode.action === "force-delete"}
          busy={busy}
          onCancel={() => setConfirmNode(null)}
          onConfirm={async () => {
            setBusy(true)
            try {
              if (confirmNode.action === "delete") await api.deleteNode(confirmNode.node.id)
              else if (confirmNode.action === "force-delete") await api.forceDeleteNode(confirmNode.node.id)
              else if (confirmNode.action === "update") await api.requestNodeUpdate(confirmNode.node.id)
              else await api.setNodeEnabled(confirmNode.node.id, false)
              setConfirmNode(null)
              await onChanged()
            } catch (reason) {
              toast.error(reason instanceof Error ? reason.message : "Не удалось выполнить действие")
            } finally {
              setBusy(false)
            }
          }}
        />
      )}
    </div>
  )
}
