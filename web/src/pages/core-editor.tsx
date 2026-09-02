import { ArrowLeft, MoreVertical, Pencil, Plus, Save, Search, Trash2 } from "lucide-react"
import { useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import { BindingDialog } from "@/components/binding-dialog"
import { ConfirmDialog } from "@/components/common/confirm-dialog"
import { InboundDialog } from "@/components/inbound-dialog"
import { OutboundDialog } from "@/components/outbound-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { bindingStrip } from "@/lib/binding-summary"
import { cn } from "@/lib/utils"
import type { Binding, Core, CoreConfig, CoreRevision, Inbound, NodeInfo, Outbound } from "@/types"

const emptyConfig = (): CoreConfig => ({ schema_version: 1, inbounds: [], outbounds: [], bindings: [] })

const makeID = (prefix: string) => {
  const bytes = new Uint8Array(8)
  if (globalThis.crypto?.getRandomValues) globalThis.crypto.getRandomValues(bytes)
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256)
  return `${prefix}_${Array.from(bytes, (v) => v.toString(16).padStart(2, "0")).join("")}`
}

const newInbound = (): Inbound => ({ id: makeID("in"), name: "Новое входящее", enabled: true, listen_address: "0.0.0.0", listen_port: 8000, mode: "tcp", tcp: true, udp: false })
const newOutbound = (): Outbound => ({
  id: makeID("out"),
  name: "Новое исходящее",
  address: "",
  port: 8080,
  enabled: true,
  health_check: { enabled: true, interval_seconds: 10, timeout_millis: 1000, failure_threshold: 3, recovery_threshold: 2 },
})
const newBinding = (): Binding => ({ id: makeID("bnd"), name: "", enabled: true, inbound_id: "", affinity_seconds: 0, selection_strategy: "least", groups: [], targets: [] })

type Tab = "inbounds" | "bindings" | "outbounds"
const TABS: Tab[] = ["inbounds", "bindings", "outbounds"]

const tabMeta: Record<Tab, { title: string; description: string; addLabel: string; searchPlaceholder: string }> = {
  inbounds: { title: "Входящие", description: "Хост, порт и режим разбора трафика на этой ноде.", addLabel: "Добавить входящее", searchPlaceholder: "Поиск по входящим" },
  bindings: { title: "Связующее", description: "Связь входящих с исходящими и правила маршрутизации трафика между ними.", addLabel: "Добавить связующее", searchPlaceholder: "Поиск по связующим" },
  outbounds: { title: "Исходящие", description: "Серверы, куда пересылается трафик, и проверка их доступности.", addLabel: "Добавить исходящее", searchPlaceholder: "Поиск по исходящим" },
}

const tabButtonClass = (active: boolean) =>
  cn("-mb-px flex items-center gap-2 border-b-2 px-1 py-2 text-sm font-medium transition-colors", active ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground")

export default function CoreEditorPage({ cores, nodes, onChanged }: { cores: Core[]; nodes: NodeInfo[]; onChanged: () => Promise<void> }) {
  const { id } = useParams()
  const navigate = useNavigate()
  const isNew = !id || id === "new"
  const knownCore = !isNew ? cores.find((c) => c.id === id) : undefined

  const [loaded, setLoaded] = useState<{ core: Core; revision: CoreRevision } | null>(null)
  const [loading, setLoading] = useState(!isNew)
  const [name, setName] = useState(isNew ? "Новое ядро" : (knownCore?.name ?? ""))
  const [config, setConfig] = useState<CoreConfig>(emptyConfig())
  const [tab, setTab] = useState<Tab>("inbounds")
  const [query, setQuery] = useState("")
  const [resetConnections, setResetConnections] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  const [editingInbound, setEditingInbound] = useState<{ inbound: Inbound; index: number | null } | null>(null)
  const [removingInboundIndex, setRemovingInboundIndex] = useState<number | null>(null)
  const [editingOutbound, setEditingOutbound] = useState<{ outbound: Outbound; index: number | null } | null>(null)
  const [removingOutboundIndex, setRemovingOutboundIndex] = useState<number | null>(null)
  const [editingBinding, setEditingBinding] = useState<{ binding: Binding; index: number | null } | null>(null)
  const [removingBindingIndex, setRemovingBindingIndex] = useState<number | null>(null)

  useEffect(() => {
    if (isNew || !id) return
    setLoading(true)
    void api
      .core(id)
      .then((data) => {
        setLoaded(data)
        setName(data.core.name)
        setConfig(data.revision.config)
        setLoading(false)
      })
      .catch((reason) => {
        toast.error(reason instanceof Error ? reason.message : "Не удалось открыть ядро")
        navigate("/cores")
      })
  }, [id, isNew, navigate])

  if (loading) return <div className="text-muted-foreground flex items-center justify-center py-16 text-sm">Загрузка…</div>

  const coreNodes = loaded ? nodes.filter((node) => node.core_id === loaded.core.id) : []

  const changeTab = (next: Tab) => {
    setTab(next)
    setQuery("")
  }

  const saveInbound = (inbound: Inbound) => {
    const inbounds = [...config.inbounds]
    if (editingInbound?.index == null) inbounds.push(inbound)
    else inbounds[editingInbound.index] = inbound
    setConfig({ ...config, inbounds })
    setEditingInbound(null)
  }
  const removeInboundAt = (index: number) => {
    const removedId = config.inbounds[index].id
    setConfig({ ...config, inbounds: config.inbounds.filter((_, i) => i !== index), bindings: config.bindings.filter((b) => b.inbound_id !== removedId) })
    setRemovingInboundIndex(null)
  }

  const saveOutbound = (outbound: Outbound) => {
    const outbounds = [...config.outbounds]
    if (editingOutbound?.index == null) outbounds.push(outbound)
    else outbounds[editingOutbound.index] = outbound
    setConfig({ ...config, outbounds })
    setEditingOutbound(null)
  }
  const removeOutboundAt = (index: number) => {
    const removedId = config.outbounds[index].id
    setConfig({
      ...config,
      outbounds: config.outbounds.filter((_, i) => i !== index),
      bindings: config.bindings.map((b) => ({ ...b, targets: b.targets.filter((t) => t.outbound_id !== removedId) })),
    })
    setRemovingOutboundIndex(null)
  }

  const saveBinding = (binding: Binding) => {
    const bindings = [...config.bindings]
    if (editingBinding?.index == null) bindings.push(binding)
    else bindings[editingBinding.index] = binding
    setConfig({ ...config, bindings })
    setEditingBinding(null)
  }
  const removeBindingAt = (index: number) => {
    setConfig({ ...config, bindings: config.bindings.filter((_, i) => i !== index) })
    setRemovingBindingIndex(null)
  }

  const addForCurrentTab = () => {
    if (tab === "inbounds") setEditingInbound({ inbound: newInbound(), index: null })
    else if (tab === "outbounds") setEditingOutbound({ outbound: newOutbound(), index: null })
    else if (config.inbounds.length > 0 && config.outbounds.length > 0) setEditingBinding({ binding: newBinding(), index: null })
  }
  const addDisabled = tab === "bindings" && (config.inbounds.length === 0 || config.outbounds.length === 0)

  const save = async () => {
    setBusy(true)
    setError("")
    try {
      if (loaded) await api.publishCore(loaded.core.id, name, "", config, resetConnections)
      else await api.createCore(name, "", config)
      await onChanged()
      navigate("/cores")
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Не удалось сохранить ядро")
    } finally {
      setBusy(false)
    }
  }

  const q = query.trim().toLowerCase()
  const filteredInbounds = config.inbounds.filter((i) => i.name.toLowerCase().includes(q))
  const filteredOutbounds = config.outbounds.filter((o) => o.name.toLowerCase().includes(q))
  const filteredBindings = config.bindings.filter((b) => (b.name || bindingStrip(b, config.inbounds, config.outbounds)).toLowerCase().includes(q))

  return (
    <div className="flex flex-col">
      <div className="bg-background sticky top-0 z-10 flex items-center gap-3 border-b px-4 py-3">
        <Button variant="ghost" size="icon" onClick={() => navigate("/cores")}>
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <Input className="h-10 max-w-sm rounded-full" value={name} onChange={(e) => setName(e.target.value)} />
      </div>

      <div className="px-4 pt-4">
        <div className="flex items-center justify-between gap-3">
          <h1 className="text-xl font-semibold">{tabMeta[tab].title}</h1>
          <Button size="sm" disabled={addDisabled} onClick={addForCurrentTab}>
            <Plus className="h-4 w-4" /> {tabMeta[tab].addLabel}
          </Button>
        </div>
        <p className="text-muted-foreground mt-1 text-sm">{tabMeta[tab].description}</p>
        {tab === "bindings" && config.inbounds.length > 0 && config.outbounds.length === 0 && <p className="text-muted-foreground mt-1 text-xs">Сначала добавьте хотя бы одно исходящее.</p>}
      </div>

      <div className="bg-background sticky top-[57px] z-10 mt-3 flex items-center gap-6 border-b px-4">
        {TABS.map((t) => (
          <button key={t} type="button" className={tabButtonClass(tab === t)} onClick={() => changeTab(t)}>
            {tabMeta[t].title}
            <Badge variant="secondary">{config[t].length}</Badge>
          </button>
        ))}
      </div>

      <div className="p-4 pb-24">
        <div className="relative mb-3 max-w-sm">
          <Search className="text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2" />
          <Input className="pl-9" placeholder={tabMeta[tab].searchPlaceholder} value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>

        {tab === "inbounds" &&
          (filteredInbounds.length === 0 ? (
            <div className="text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm">{query ? "Ничего не найдено" : "Добавьте хост:порт, которые нода будет слушать."}</div>
          ) : (
            <Card className="overflow-hidden p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Название</TableHead>
                    <TableHead>Хост:порт</TableHead>
                    <TableHead>Режим</TableHead>
                    <TableHead>Протокол</TableHead>
                    <TableHead className="w-10" />
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredInbounds.map((inbound) => {
                    const index = config.inbounds.indexOf(inbound)
                    return (
                      <TableRow key={inbound.id} className={cn("cursor-pointer", !inbound.enabled && "opacity-50")} onClick={() => setEditingInbound({ inbound: structuredClone(inbound), index })}>
                        <TableCell className="font-medium">{inbound.name}</TableCell>
                        <TableCell className="text-muted-foreground font-mono">
                          {inbound.listen_address}:{inbound.listen_port}
                        </TableCell>
                        <TableCell>
                          <Badge variant="secondary">{inbound.mode.toUpperCase()}</Badge>
                        </TableCell>
                        <TableCell>
                          <div className="flex gap-1">
                            {inbound.tcp && <Badge variant="secondary">TCP</Badge>}
                            {inbound.udp && <Badge variant="secondary">UDP</Badge>}
                          </div>
                        </TableCell>
                        <TableCell onClick={(e) => e.stopPropagation()}>
                          <Switch checked={inbound.enabled} onCheckedChange={(enabled) => setConfig({ ...config, inbounds: config.inbounds.map((v, i) => (i === index ? { ...v, enabled } : v)) })} />
                        </TableCell>
                        <TableCell onClick={(e) => e.stopPropagation()}>
                          <RowMenu onEdit={() => setEditingInbound({ inbound: structuredClone(inbound), index })} onDelete={() => setRemovingInboundIndex(index)} />
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </Card>
          ))}

        {tab === "bindings" &&
          (filteredBindings.length === 0 ? (
            <div className="text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm">{query ? "Ничего не найдено" : "Свяжите входящий с исходящим, чтобы начать пересылку трафика."}</div>
          ) : (
            <div className="flex flex-col gap-2">
              {filteredBindings.map((binding) => {
                const index = config.bindings.indexOf(binding)
                return (
                  <Card
                    key={binding.id}
                    className={cn("flex cursor-pointer items-center gap-3 p-3 transition-colors hover:bg-accent/40", !binding.enabled && "opacity-50")}
                    onClick={() => setEditingBinding({ binding: structuredClone(binding), index })}
                  >
                    <span className={cn("h-2 w-2 shrink-0 rounded-full", binding.enabled ? "bg-success" : "bg-muted-foreground")} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{binding.name || bindingStrip(binding, config.inbounds, config.outbounds)}</div>
                      {binding.name && <div className="text-muted-foreground truncate font-mono text-xs">{bindingStrip(binding, config.inbounds, config.outbounds)}</div>}
                    </div>
                    <div onClick={(e) => e.stopPropagation()}>
                      <RowMenu onEdit={() => setEditingBinding({ binding: structuredClone(binding), index })} onDelete={() => setRemovingBindingIndex(index)} />
                    </div>
                  </Card>
                )
              })}
            </div>
          ))}

        {tab === "outbounds" &&
          (filteredOutbounds.length === 0 ? (
            <div className="text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm">{query ? "Ничего не найдено" : "Добавьте хотя бы один сервер-исходящее."}</div>
          ) : (
            <Card className="overflow-hidden p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Название</TableHead>
                    <TableHead>Адрес:порт</TableHead>
                    <TableHead>Проверка жизни</TableHead>
                    <TableHead className="w-10" />
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredOutbounds.map((outbound) => {
                    const index = config.outbounds.indexOf(outbound)
                    return (
                      <TableRow key={outbound.id} className={cn("cursor-pointer", !outbound.enabled && "opacity-50")} onClick={() => setEditingOutbound({ outbound: structuredClone(outbound), index })}>
                        <TableCell className="font-medium">{outbound.name}</TableCell>
                        <TableCell className="text-muted-foreground font-mono">
                          {outbound.address}:{outbound.port}
                        </TableCell>
                        <TableCell>
                          <Badge variant={outbound.health_check.enabled ? "green" : "secondary"}>{outbound.health_check.enabled ? `каждые ${outbound.health_check.interval_seconds}с` : "выключена"}</Badge>
                        </TableCell>
                        <TableCell onClick={(e) => e.stopPropagation()}>
                          <Switch checked={outbound.enabled} onCheckedChange={(enabled) => setConfig({ ...config, outbounds: config.outbounds.map((v, i) => (i === index ? { ...v, enabled } : v)) })} />
                        </TableCell>
                        <TableCell onClick={(e) => e.stopPropagation()}>
                          <RowMenu onEdit={() => setEditingOutbound({ outbound: structuredClone(outbound), index })} onDelete={() => setRemovingOutboundIndex(index)} />
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </Card>
          ))}
      </div>

      <div className="bg-background/95 sticky bottom-0 flex flex-col gap-2 border-t px-4 py-3 backdrop-blur">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <Checkbox id="restart-nodes" checked={resetConnections} disabled={coreNodes.length === 0} onCheckedChange={(checked) => setResetConnections(checked === true)} />
            <Label htmlFor="restart-nodes" className="cursor-pointer text-sm font-normal">
              {coreNodes.length === 1 ? "Перезапустить узел" : "Перезапустить узлы"}
              {coreNodes.length > 0 && <span className="text-muted-foreground"> ({coreNodes.length})</span>}
            </Label>
          </div>
          <div className="flex items-center gap-2">
            {error && <span className="text-destructive text-sm">{error}</span>}
            <Button variant="outline" onClick={() => navigate("/cores")}>
              Отмена
            </Button>
            <Button disabled={busy || !name.trim()} onClick={() => void save()}>
              <Save className="h-4 w-4" />
              {busy ? "Сохраняю…" : "Сохранить"}
            </Button>
          </div>
        </div>
      </div>

      {editingInbound && <InboundDialog initial={editingInbound.inbound} others={config.inbounds.filter((_, i) => i !== editingInbound.index)} onSave={saveInbound} onClose={() => setEditingInbound(null)} />}
      {removingInboundIndex != null && (
        <ConfirmDialog
          title="Удалить входящее?"
          description={`Входящее «${config.inbounds[removingInboundIndex].name}» и связанные с ним связующие будут удалены.`}
          confirmLabel="Удалить входящее"
          danger
          onCancel={() => setRemovingInboundIndex(null)}
          onConfirm={() => removeInboundAt(removingInboundIndex)}
        />
      )}

      {editingOutbound && <OutboundDialog initial={editingOutbound.outbound} others={config.outbounds.filter((_, i) => i !== editingOutbound.index)} onSave={saveOutbound} onClose={() => setEditingOutbound(null)} />}
      {removingOutboundIndex != null && (
        <ConfirmDialog
          title="Удалить исходящее?"
          description={`Исходящее «${config.outbounds[removingOutboundIndex].name}» будет убрано из всех связующих, где оно указано.`}
          confirmLabel="Удалить исходящее"
          danger
          onCancel={() => setRemovingOutboundIndex(null)}
          onConfirm={() => removeOutboundAt(removingOutboundIndex)}
        />
      )}

      {editingBinding && <BindingDialog initial={editingBinding.binding} inbounds={config.inbounds} outbounds={config.outbounds} onSave={saveBinding} onClose={() => setEditingBinding(null)} />}
      {removingBindingIndex != null && (
        <ConfirmDialog title="Удалить связующее?" description="Это связующее будет удалено из ядра." confirmLabel="Удалить связующее" danger onCancel={() => setRemovingBindingIndex(null)} onConfirm={() => removeBindingAt(removingBindingIndex)} />
      )}
    </div>
  )
}

function RowMenu({ onEdit, onDelete }: { onEdit: () => void; onDelete: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="h-8 w-8">
          <MoreVertical className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={onEdit}>
          <Pencil className="h-4 w-4" /> Редактировать
        </DropdownMenuItem>
        <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={onDelete}>
          <Trash2 className="h-4 w-4" /> Удалить
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
