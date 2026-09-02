import { ArrowDown, ArrowUp, CheckCircle2, ChevronRight, Cpu, Eye, EyeOff, Fingerprint, MemoryStick, Network, RefreshCw, Save, Users, XCircle } from "lucide-react"
import { useEffect, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { api } from "@/lib/api"
import { formatDuration, formatNumber, formatPercent, formatRelative, formatNetworkRate } from "@/lib/format"
import { applyStateLabel, healthStateLabel, nodeStatusLabel } from "@/lib/node-status"
import type { BackendHealth, Core, NodeBreakdown, NodeInfo, ServiceStat } from "@/types"

export function NodeEnrollDialog({ cores, onClose, onSaved }: { cores: Core[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const [name, setName] = useState("")
  const [ingressAddress, setIngressAddress] = useState("")
  const [controlAddress, setControlAddress] = useState("")
  const [controlPort, setControlPort] = useState(62050)
  const [apiKey, setApiKey] = useState("")
  const [showApiKey, setShowApiKey] = useState(false)
  const [certPem, setCertPem] = useState("")
  const [coreId, setCoreId] = useState(cores[0]?.id ?? "")
  const [pollInterval, setPollInterval] = useState("")
  const [timeoutSeconds, setTimeoutSeconds] = useState("")
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [result, setResult] = useState<{ node: NodeInfo; connected: boolean; connect_error: string } | null>(null)
  const [rechecking, setRechecking] = useState(false)

  const valid = name.trim() && controlAddress.trim() && controlPort >= 1 && controlPort <= 65535 && apiKey.trim() && certPem.trim() && coreId
  const submit = async () => {
    setBusy(true)
    setError("")
    try {
      const created = await api.createNode({
        name: name.trim(), ingress_address: ingressAddress.trim(), control_address: controlAddress.trim(), control_port: controlPort,
        api_key: apiKey.trim(), cert_pem: certPem.trim(), core_id: coreId,
        poll_interval_seconds: pollInterval ? Number(pollInterval) : null, timeout_seconds: timeoutSeconds ? Number(timeoutSeconds) : null,
      })
      setResult(created)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Не удалось добавить ноду")
    } finally {
      setBusy(false)
    }
  }
  const recheck = async () => {
    if (!result) return
    setRechecking(true)
    try {
      await api.checkNodeConnectivity(result.node.id)
      setResult({ ...result, connected: true, connect_error: "" })
    } catch (reason) {
      setResult({ ...result, connected: false, connect_error: reason instanceof Error ? reason.message : "Нода недоступна" })
    } finally {
      setRechecking(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="h-full max-w-full overflow-y-auto sm:max-w-[92vw] lg:h-auto lg:max-w-[1100px]">
        <DialogHeader>
          <DialogTitle>{result ? `Подключение · ${result.node.name}` : "Добавить узел"}</DialogTitle>
          <DialogDescription>{result ? "Проверьте статус подключения — панель сама переопросит ноду в фоне." : "Вставьте данные, которые вывел скрипт установки на ноде."}</DialogDescription>
        </DialogHeader>

        {result ? (
          <>
            <div className={`flex items-center gap-3 rounded-lg border p-4 ${result.connected ? "border-success/30 bg-success/5" : "border-destructive/30 bg-destructive/5"}`}>
              {result.connected ? <CheckCircle2 className="text-success h-5 w-5 shrink-0" /> : <XCircle className="text-destructive h-5 w-5 shrink-0" />}
              <div className="min-w-0">
                <div className="text-sm font-semibold">{result.connected ? "Нода на связи" : "Пока не удалось подключиться"}</div>
                <div className="text-muted-foreground text-xs">{result.connected ? "Панель уже опрашивает её каждые несколько секунд." : result.connect_error || "Панель продолжит пробовать в фоне."}</div>
              </div>
            </div>
            <Button variant="outline" disabled={rechecking} onClick={() => void recheck()}>
              <RefreshCw className={rechecking ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
              {rechecking ? "Проверяю…" : "Проверить статус"}
            </Button>
            <DialogFooter>
              <Button onClick={() => void onSaved()}>Готово</Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
              <div className="flex flex-col gap-4">
                <div className="flex flex-col gap-1.5">
                  <Label>Название узла</Label>
                  <Input value={name} placeholder="server-1" onChange={(e) => setName(e.target.value)} autoFocus />
                  <span className="text-muted-foreground text-xs">Например: server-1</span>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="flex flex-col gap-1.5">
                    <Label>Адрес узла</Label>
                    <Input className="font-mono" value={controlAddress} placeholder="203.0.113.10" onChange={(e) => setControlAddress(e.target.value)} />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label>Порт узла</Label>
                    <Input type="number" min={1} max={65535} value={controlPort} onChange={(e) => setControlPort(Number(e.target.value))} />
                  </div>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label>Конфигурация ядра</Label>
                  <Select value={coreId} onValueChange={setCoreId}>
                    <SelectTrigger>
                      <SelectValue placeholder="Выберите ядро" />
                    </SelectTrigger>
                    <SelectContent>
                      {cores.map((core) => (
                        <SelectItem key={core.id} value={core.id}>
                          {core.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label>API ключ</Label>
                  <div className="relative">
                    <Input className="pr-10 font-mono" type={showApiKey ? "text" : "password"} value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
                    <button type="button" className="text-muted-foreground hover:text-foreground absolute top-0 right-0 flex h-9 w-9 items-center justify-center" onClick={() => setShowApiKey((v) => !v)}>
                      {showApiKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    </button>
                  </div>
                  <span className="text-muted-foreground text-xs">Строка «API ключ» из вывода скрипта установки</span>
                </div>
                <button type="button" className="text-muted-foreground hover:text-foreground flex items-center gap-1 self-start text-xs font-semibold" onClick={() => setShowAdvanced((v) => !v)}>
                  <ChevronRight className={`h-3.5 w-3.5 transition-transform ${showAdvanced ? "rotate-90" : ""}`} />
                  Расширенные настройки
                </button>
                {showAdvanced && (
                  <div className="flex flex-col gap-3">
                    <div className="flex flex-col gap-1.5">
                      <Label>VIP для UDP-входящих</Label>
                      <Input className="font-mono" value={ingressAddress} placeholder="0.0.0.0" onChange={(e) => setIngressAddress(e.target.value)} />
                      <span className="text-muted-foreground text-xs">
                        Нужен только входящим с UDP и хостом 0.0.0.0 — на такой адрес нельзя напрямую привязать IPVS-сервис, нужен конкретный IP. TCP-входящие в этом не нуждаются. Если оставить
                        пустым, нода определит его сама по таблице маршрутизации.
                      </span>
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div className="flex flex-col gap-1.5">
                        <Label>Интервал опроса, с</Label>
                        <Input type="number" min={1} max={3600} placeholder="по умолчанию" value={pollInterval} onChange={(e) => setPollInterval(e.target.value)} />
                      </div>
                      <div className="flex flex-col gap-1.5">
                        <Label>Таймаут по умолчанию, с</Label>
                        <Input type="number" min={1} max={120} placeholder="по умолчанию" value={timeoutSeconds} onChange={(e) => setTimeoutSeconds(e.target.value)} />
                      </div>
                    </div>
                    <span className="text-muted-foreground text-xs">Пусто — используется общее значение панели. Задайте здесь, только если этому узлу нужен свой интервал опроса или таймаут.</span>
                  </div>
                )}
                {error && <div className="text-destructive text-sm">{error}</div>}
              </div>
              <div className="flex flex-col gap-1.5">
                <Label>Сертификат</Label>
                <Textarea className="min-h-[240px] font-mono text-xs" value={certPem} onChange={(e) => setCertPem(e.target.value)} />
                <span className="text-muted-foreground text-xs">Блок «Сертификат» целиком, включая BEGIN/END CERTIFICATE</span>
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>
                Отмена
              </Button>
              <Button disabled={busy || !valid} onClick={() => void submit()}>
                {busy ? "Подключаю…" : "Добавить узел"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function NodeEditDialog({ node, cores, onClose, onSaved }: { node: NodeInfo; cores: Core[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const [name, setName] = useState(node.name)
  const [ingressAddress, setIngressAddress] = useState(node.ingress_address)
  const [controlAddress, setControlAddress] = useState(node.control_address)
  const [controlPort, setControlPort] = useState(node.control_port)
  const [coreId, setCoreId] = useState(node.core_id)
  const [apiKey, setApiKey] = useState("")
  const [showApiKey, setShowApiKey] = useState(false)
  const [certPem, setCertPem] = useState("")
  const [pollInterval, setPollInterval] = useState(node.poll_interval_seconds != null ? String(node.poll_interval_seconds) : "")
  const [timeoutSeconds, setTimeoutSeconds] = useState(node.timeout_seconds != null ? String(node.timeout_seconds) : "")
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const [checkResult, setCheckResult] = useState<{ ok: boolean; message: string } | null>(null)

  const check = async () => {
    setChecking(true)
    setCheckResult(null)
    try {
      await api.checkNodeConnectivity(node.id)
      setCheckResult({ ok: true, message: "Нода отвечает" })
    } catch (reason) {
      setCheckResult({ ok: false, message: reason instanceof Error ? reason.message : "Нода недоступна" })
    } finally {
      setChecking(false)
    }
  }
  const save = async () => {
    setBusy(true)
    try {
      await api.updateNode(
        node.id, name.trim(), ingressAddress.trim(), controlAddress.trim(), controlPort, apiKey.trim(), certPem.trim(),
        pollInterval ? Number(pollInterval) : null, timeoutSeconds ? Number(timeoutSeconds) : null,
      )
      if (coreId !== node.core_id) await api.assignCore(node.id, coreId)
      await onSaved()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="h-full max-w-full overflow-y-auto sm:max-w-[92vw] lg:h-auto lg:max-w-[1100px]">
        <DialogHeader>
          <DialogTitle>Редактировать узел</DialogTitle>
          <DialogDescription>{`Параметры подключения к «${node.name}».`}</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2 border-b pb-4">
          <Badge variant={node.status === "online" ? "green" : "red"}>{node.status === "online" ? "Подключён" : "Не подключён"}</Badge>
          {checkResult && <Badge variant={checkResult.ok ? "green" : "red"}>{checkResult.message}</Badge>}
          <Button variant="outline" size="sm" className="ml-auto" disabled={checking} onClick={() => void check()}>
            <RefreshCw className={checking ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
            {checking ? "Проверяю…" : "Проверить статус"}
          </Button>
        </div>
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <Label>Название узла</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5">
                <Label>Адрес узла</Label>
                <Input className="font-mono" value={controlAddress} onChange={(e) => setControlAddress(e.target.value)} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label>Порт узла</Label>
                <Input type="number" min={1} max={65535} value={controlPort} onChange={(e) => setControlPort(Number(e.target.value))} />
              </div>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>Конфигурация ядра</Label>
              <Select value={coreId} onValueChange={setCoreId}>
                <SelectTrigger>
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
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>Новый API ключ</Label>
              <div className="relative">
                <Input className="pr-10 font-mono" type={showApiKey ? "text" : "password"} value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
                <button type="button" className="text-muted-foreground hover:text-foreground absolute top-0 right-0 flex h-9 w-9 items-center justify-center" onClick={() => setShowApiKey((v) => !v)}>
                  {showApiKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
              <span className="text-muted-foreground text-xs">Заполните только если переустановили ноду</span>
            </div>
            <button type="button" className="text-muted-foreground hover:text-foreground flex items-center gap-1 self-start text-xs font-semibold" onClick={() => setShowAdvanced((v) => !v)}>
              <ChevronRight className={`h-3.5 w-3.5 transition-transform ${showAdvanced ? "rotate-90" : ""}`} />
              Расширенные настройки
            </button>
            {showAdvanced && (
              <div className="flex flex-col gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label>VIP для UDP-входящих</Label>
                  <Input className="font-mono" placeholder="0.0.0.0" value={ingressAddress} onChange={(e) => setIngressAddress(e.target.value)} />
                  <span className="text-muted-foreground text-xs">
                    Нужен только входящим с UDP и хостом 0.0.0.0. TCP-входящие в этом не нуждаются. Пусто — нода определит его сама.
                  </span>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="flex flex-col gap-1.5">
                    <Label>Интервал опроса, с</Label>
                    <Input type="number" min={1} max={3600} placeholder="по умолчанию" value={pollInterval} onChange={(e) => setPollInterval(e.target.value)} />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label>Таймаут по умолчанию, с</Label>
                    <Input type="number" min={1} max={120} placeholder="по умолчанию" value={timeoutSeconds} onChange={(e) => setTimeoutSeconds(e.target.value)} />
                  </div>
                </div>
                <span className="text-muted-foreground text-xs">Пусто — используется общее значение панели.</span>
              </div>
            )}
          </div>
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <Label>Сертификат</Label>
              <Textarea className="min-h-[200px] font-mono text-xs" placeholder="Заполните только вместе с новым API ключом" value={certPem} onChange={(e) => setCertPem(e.target.value)} />
            </div>
            <div className="bg-accent/40 flex items-start gap-2 rounded-lg border p-3">
              <Fingerprint className="text-muted-foreground mt-0.5 h-4 w-4 shrink-0" />
              <div className="min-w-0">
                <div className="text-xs font-semibold">Отпечаток текущего сертификата</div>
                <div className="text-muted-foreground truncate font-mono text-[11px]">{node.cert_fingerprint || "—"}</div>
              </div>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button disabled={busy || !name.trim() || !controlAddress.trim()} onClick={() => void save()}>
            <Save className="h-4 w-4" />
            {busy ? "Сохраняю…" : "Изменить"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function NodeDetailsDialog({ node, core, stats, health, onClose }: { node: NodeInfo; core?: Core; stats: ServiceStat[]; health: BackendHealth[]; onClose: () => void }) {
  const services = stats.filter((item) => !item.backend_address)
  const [breakdown, setBreakdown] = useState<NodeBreakdown | null>(null)

  useEffect(() => {
    let active = true
    void api
      .nodeBreakdown(node.id)
      .then((data) => {
        if (active) setBreakdown(data)
      })
      .catch(() => {
        if (active) setBreakdown(null)
      })
    return () => {
      active = false
    }
  }, [node.id])

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="h-full max-w-full overflow-y-auto sm:max-w-[92vw] lg:h-auto lg:max-w-[1100px]">
        <DialogHeader>
          <DialogTitle>{node.name}</DialogTitle>
          <DialogDescription>Текущее состояние агента, конфигурации и маршрутов ноды.</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          {[
            ["Состояние", nodeStatusLabel(node.status)],
            ["Адрес узла", `${node.control_address}:${node.control_port}`],
            ["Uptime связи", node.status === "online" && node.online_since ? formatDuration(Date.now() - new Date(node.online_since).getTime()) : "—"],
            ["Версия агента", node.agent_version || "—"],
          ].map(([label, value]) => (
            <div key={label} className="rounded-lg border p-3">
              <div className="text-muted-foreground text-xs">{label}</div>
              <div className="truncate font-mono text-sm font-semibold">{value}</div>
            </div>
          ))}
        </div>
        {node.metrics && (
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <div className="rounded-lg border p-3">
              <Users className="text-muted-foreground h-4 w-4" />
              <div className="text-muted-foreground text-xs">Активные IP</div>
              <div className="text-sm font-semibold">{formatNumber(node.metrics.active_ips)}</div>
            </div>
            <div className="rounded-lg border p-3">
              <MemoryStick className="text-muted-foreground h-4 w-4" />
              <div className="text-muted-foreground text-xs">RAM</div>
              <div className="text-sm font-semibold">{formatPercent(node.metrics.ram_used_percent)}</div>
            </div>
            <div className="rounded-lg border p-3">
              <Cpu className="text-muted-foreground h-4 w-4" />
              <div className="text-muted-foreground text-xs">CPU</div>
              <div className="text-sm font-semibold">
                {formatPercent(node.metrics.cpu_used_percent)} <span className="text-muted-foreground font-normal">load {node.metrics.load_1.toFixed(2)}</span>
              </div>
            </div>
            <div className="rounded-lg border p-3">
              <Network className="text-muted-foreground h-4 w-4" />
              <div className="text-muted-foreground text-xs">Сеть</div>
              <div className="flex items-center gap-2 text-sm font-semibold">
                <ArrowDown className="h-3 w-3" />
                {formatNetworkRate(node.metrics.network_rx_bps)}
                <ArrowUp className="h-3 w-3" />
                {formatNetworkRate(node.metrics.network_tx_bps)}
              </div>
            </div>
          </div>
        )}
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
          <div className="rounded-lg border p-3">
            <div className="text-muted-foreground text-xs">Применение</div>
            <div className="text-sm font-semibold">{applyStateLabel(node)}</div>
            <div className="text-muted-foreground mt-1 text-xs">Ядро: {core?.name || "не назначено"}</div>
            <div className="text-muted-foreground text-xs">{node.last_seen_at ? `Последний опрос ${formatRelative(node.last_seen_at)}` : "Ещё не опрошена"}</div>
          </div>
          <div className="rounded-lg border p-3">
            <div className="text-muted-foreground text-xs">Health-check</div>
            <div className="text-sm font-semibold">
              {health.filter((item) => item.state === "reachable").length} из {health.length} доступны
            </div>
            <div className="mt-1 flex flex-wrap gap-1">
              {health.map((item) => (
                <Badge key={item.address} variant={item.state === "reachable" ? "green" : item.state === "unreachable" ? "red" : "secondary"}>
                  {item.address} · {healthStateLabel(item.state)}
                </Badge>
              ))}
            </div>
          </div>
          {node.diagnostics && (
            <div className="rounded-lg border p-3">
              <div className="text-muted-foreground text-xs">Диагностика</div>
              <div className="text-sm font-semibold">{node.diagnostics.ipvs_available && node.diagnostics.firewall_ready ? "Всё в порядке" : "Есть проблемы"}</div>
              <div className="mt-1 flex gap-1">
                <Badge variant={node.diagnostics.ipvs_available ? "green" : "red"}>IPVS {node.diagnostics.ipvs_available ? "доступен" : "недоступен"}</Badge>
                <Badge variant={node.diagnostics.firewall_ready ? "green" : "red"}>Firewall {node.diagnostics.firewall_ready ? "готов" : "не готов"}</Badge>
              </div>
            </div>
          )}
        </div>
        {node.apply_error && <div className="border-destructive/30 bg-destructive/5 text-destructive rounded-lg border p-3 text-sm">Ошибка применения: {node.apply_error}</div>}

        {breakdown && breakdown.inbounds.length > 0 && (
          <div className="rounded-lg border">
            <div className="border-b p-3">
              <div className="text-muted-foreground text-xs">По входящим</div>
              <div className="text-sm font-semibold">Онлайн и трафик на каждом входящем</div>
            </div>
            <div className="flex flex-col divide-y">
              {breakdown.inbounds.map((inbound) => (
                <div key={inbound.inbound_id} className="flex items-center gap-3 p-3 text-sm">
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-medium">{inbound.name}</div>
                    <div className="text-muted-foreground truncate font-mono text-xs">
                      {inbound.listen_address}:{inbound.listen_port}
                    </div>
                  </div>
                  <span className="text-muted-foreground flex items-center gap-1 text-xs" title="Онлайн IP">
                    <Users className="h-3 w-3" />
                    {formatNumber(inbound.online_ips)}
                  </span>
                  <span className="flex items-center gap-1 font-mono text-xs">
                    <ArrowDown className="h-3 w-3" />
                    {formatNetworkRate(inbound.rx_bps)}
                    <ArrowUp className="h-3 w-3" />
                    {formatNetworkRate(inbound.tx_bps)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        {breakdown && breakdown.outbounds.length > 0 && (
          <div className="rounded-lg border">
            <div className="border-b p-3">
              <div className="text-muted-foreground text-xs">По исходящим</div>
              <div className="text-sm font-semibold">Онлайн и трафик на каждом исходящем</div>
            </div>
            <div className="flex flex-col divide-y">
              {breakdown.outbounds.map((outbound) => (
                <div key={outbound.outbound_id} className="flex items-center gap-3 p-3 text-sm">
                  <span className={`h-2 w-2 shrink-0 rounded-full ${outbound.reachable ? "bg-success" : "bg-destructive"}`} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-medium">{outbound.name}</div>
                    <div className="text-muted-foreground truncate font-mono text-xs">
                      {outbound.address}:{outbound.port}
                    </div>
                  </div>
                  <span className="text-muted-foreground flex items-center gap-1 text-xs" title="Онлайн IP">
                    <Users className="h-3 w-3" />
                    {formatNumber(outbound.online_ips)}
                  </span>
                  <span className="flex items-center gap-1 font-mono text-xs">
                    <ArrowDown className="h-3 w-3" />
                    {formatNetworkRate(outbound.rx_bps)}
                    <ArrowUp className="h-3 w-3" />
                    {formatNetworkRate(outbound.tx_bps)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}

        <div className="rounded-lg border">
          <div className="border-b p-3">
            <div className="text-muted-foreground text-xs">Live IPVS</div>
            <div className="text-sm font-semibold">{services.length} маршрутов</div>
          </div>
          {stats.length === 0 ? (
            <div className="text-muted-foreground p-4 text-center text-sm">Маршруты появятся после первого успешного опроса со статистикой.</div>
          ) : (
            <div className="flex flex-col divide-y">
              {stats.map((item) => (
                <div key={`${item.protocol}-${item.listen_address}-${item.listen_port}-${item.backend_address}-${item.backend_port}`} className="flex items-center gap-3 p-3 text-sm">
                  <Badge variant="secondary">{item.protocol.toUpperCase()}</Badge>
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">{item.backend_address ? `↳ ${item.backend_address}:${item.backend_port}` : `${item.listen_address}:${item.listen_port}`}</span>
                  <span className="font-mono text-xs">{formatNumber(item.connections)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Готово</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
