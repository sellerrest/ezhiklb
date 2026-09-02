import { ArrowDown, ArrowUp, Cpu, MemoryStick, Radio, Server, Users, XCircle } from "lucide-react"
import { useEffect, useState } from "react"
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis } from "recharts"

import { NodeDetailsDialog } from "@/components/node-dialogs"
import { NodeStatsLine } from "@/components/node-stats-line"
import PageHeader from "@/components/layout/page-header"
import { Card } from "@/components/ui/card"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api"
import { formatNumber, formatPercent, formatNetworkRate } from "@/lib/format"
import type { BackendHealth, Core, NodeInfo, NodeMetricPoint, OutboundStatusEntry, ServiceStat, Status } from "@/types"

function aggregateMetricHistory(items: NodeMetricPoint[], aggregate: boolean): NodeMetricPoint[] {
  if (!aggregate) return items
  const buckets = new Map<string, { point: NodeMetricPoint; count: number }>()
  for (const item of items) {
    const key = item.collected_at
    const bucket = buckets.get(key) ?? { point: { ...item, node_id: "all", ram_used_percent: 0, cpu_used_percent: 0, load_1: 0, network_rx_bps: 0, network_tx_bps: 0, active_ips: 0 }, count: 0 }
    bucket.count++
    bucket.point.ram_used_percent += item.ram_used_percent
    bucket.point.cpu_used_percent += item.cpu_used_percent
    bucket.point.network_rx_bps += item.network_rx_bps
    bucket.point.network_tx_bps += item.network_tx_bps
    bucket.point.active_ips += item.active_ips
    buckets.set(key, bucket)
  }
  return [...buckets.values()]
    .map(({ point, count }) => ({ ...point, ram_used_percent: point.ram_used_percent / count, cpu_used_percent: point.cpu_used_percent / count }))
    .sort((a, b) => a.collected_at.localeCompare(b.collected_at))
}

function MetricCard({ label, value, icon: Icon, tone }: { label: string; value: string; icon: React.ComponentType<{ className?: string }>; tone?: "success" }) {
  return (
    <Card className="flex items-center gap-3 p-4">
      <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ${tone === "success" ? "bg-success/10 text-success" : "bg-accent text-foreground"}`}>
        <Icon className="h-5 w-5" />
      </div>
      <div className="min-w-0">
        <div className="text-muted-foreground truncate text-xs">{label}</div>
        <div className="truncate text-lg font-semibold">{value}</div>
      </div>
    </Card>
  )
}

function MiniChart({ title, icon: Icon, points, dataKey, format, color }: { title: string; icon: React.ComponentType<{ className?: string }>; points: NodeMetricPoint[]; dataKey: keyof NodeMetricPoint; format: (v: number) => string; color: string }) {
  const latest = points.at(-1)
  return (
    <Card className="p-4">
      <div className="mb-2 flex items-center gap-2">
        <Icon className="text-muted-foreground h-4 w-4" />
        <span className="text-sm font-medium">{title}</span>
        <span className="text-muted-foreground ml-auto text-xs">{latest ? format(Number(latest[dataKey])) : "—"}</span>
      </div>
      <div className="h-[120px]">
        {points.length < 2 ? (
          <div className="text-muted-foreground flex h-full items-center justify-center text-xs">Данных пока мало</div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={points} margin={{ top: 4, right: 4, left: 4, bottom: 0 }}>
              <defs>
                <linearGradient id={`grad-${dataKey}`} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor={color} stopOpacity={0.35} />
                  <stop offset="95%" stopColor={color} stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="collected_at" hide />
              <Tooltip
                formatter={(value) => format(Number(value))}
                labelFormatter={(label) => new Date(String(label)).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })}
                contentStyle={{ background: "hsl(var(--popover))", border: "1px solid hsl(var(--border))", borderRadius: 8, fontSize: 12 }}
              />
              <Area type="monotone" dataKey={dataKey} stroke={color} fill={`url(#grad-${dataKey})`} strokeWidth={2} />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </div>
    </Card>
  )
}

export default function StatisticsPage({
  status, nodes, cores, stats, health, outbounds,
}: {
  status: Status | null
  nodes: NodeInfo[]
  cores: Core[]
  stats: ServiceStat[]
  health: BackendHealth[]
  outbounds: OutboundStatusEntry[]
}) {
  const [chartNode, setChartNode] = useState("all")
  const [history, setHistory] = useState<NodeMetricPoint[]>([])
  const [selectedNode, setSelectedNode] = useState<NodeInfo | null>(null)

  const deadNodes = Math.max(0, (status?.nodes ?? 0) - (status?.online_nodes ?? 0))
  const aliveOutbounds = outbounds.filter((o) => o.status === "alive").length
  const deadOutbounds = outbounds.filter((o) => o.status === "dead").length
  const onlineNodes = nodes.filter((n) => n.status === "online")
  const totalRx = onlineNodes.reduce((sum, n) => sum + (n.metrics?.network_rx_bps ?? 0), 0)
  const totalTx = onlineNodes.reduce((sum, n) => sum + (n.metrics?.network_tx_bps ?? 0), 0)

  useEffect(() => {
    let active = true
    const loadHistory = () => {
      void api
        .metricHistory(chartNode)
        .then((items) => {
          if (active) setHistory(items)
        })
        .catch(() => {
          if (active) setHistory([])
        })
    }
    loadHistory()
    const timer = window.setInterval(loadHistory, 60000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [chartNode])
  const chartPoints = aggregateMetricHistory(history, chartNode === "all")

  return (
    <div className="flex flex-col gap-4 pb-8">
      <PageHeader title="Статистика" description="Состояние инфраструктуры EzhikLB в одном месте." />
      <div className="grid grid-cols-1 gap-3 px-4 sm:grid-cols-3">
        <MetricCard label="Ноды онлайн" value={String(status?.online_nodes ?? 0)} icon={Server} tone="success" />
        <MetricCard label="Активные исходящие" value={String(aliveOutbounds)} icon={Radio} tone="success" />
        <MetricCard label="Общий входящий трафик" value={formatNetworkRate(totalRx)} icon={ArrowDown} />
        <MetricCard label="Ноды мертвые" value={String(deadNodes)} icon={Server} />
        <MetricCard label="Мертвые исходящие" value={String(deadOutbounds)} icon={XCircle} />
        <MetricCard label="Общий исходящий трафик" value={formatNetworkRate(totalTx)} icon={ArrowUp} />
      </div>

      <div className="px-4">
        <Card className="p-4">
          <div className="mb-3">
            <p className="text-muted-foreground text-xs font-semibold uppercase">Ноды</p>
            <h2 className="text-sm font-semibold">Состояние применения</h2>
          </div>
          <div className="flex flex-col gap-2">
            {nodes.length === 0 && <div className="text-muted-foreground py-6 text-center text-sm">Узлов пока нет</div>}
            {nodes.map((node) => (
              <button
                key={node.id}
                type="button"
                onClick={() => setSelectedNode(node)}
                className="hover:bg-accent/40 flex items-start gap-3 rounded-lg border px-3 py-2 text-left transition-colors"
              >
                <span className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${node.status === "online" ? "bg-success" : node.apply_error ? "bg-destructive" : "bg-muted-foreground"}`} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{node.name}</div>
                  {node.apply_error ? <div className="text-destructive truncate text-xs">{node.apply_error}</div> : <NodeStatsLine node={node} />}
                </div>
              </button>
            ))}
          </div>
        </Card>
      </div>

      <div className="px-4">
        <div className="mb-3 flex items-center justify-between">
          <div>
            <p className="text-muted-foreground text-xs font-semibold uppercase">Последние 24 часа</p>
            <h2 className="text-sm font-semibold">Нагрузка и активность</h2>
          </div>
          <Select value={chartNode} onValueChange={setChartNode}>
            <SelectTrigger className="w-[200px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Все ноды</SelectItem>
              {nodes.map((node) => (
                <SelectItem key={node.id} value={node.id}>
                  {node.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <MiniChart title="Приём" icon={ArrowDown} points={chartPoints} dataKey="network_rx_bps" format={formatNetworkRate} color="hsl(var(--chart-1))" />
          <MiniChart title="CPU" icon={Cpu} points={chartPoints} dataKey="cpu_used_percent" format={formatPercent} color="hsl(var(--chart-3))" />
          <MiniChart title="RAM" icon={MemoryStick} points={chartPoints} dataKey="ram_used_percent" format={formatPercent} color="hsl(var(--chart-4))" />
          <MiniChart title="Активные IP" icon={Users} points={chartPoints} dataKey="active_ips" format={(v) => formatNumber(v)} color="hsl(var(--chart-2))" />
        </div>
      </div>

      {selectedNode && (
        <NodeDetailsDialog
          node={nodes.find((n) => n.id === selectedNode.id) ?? selectedNode}
          core={cores.find((c) => c.id === selectedNode.core_id)}
          stats={stats.filter((item) => item.node_id === selectedNode.id)}
          health={health.filter((item) => item.node_id === selectedNode.id)}
          onClose={() => setSelectedNode(null)}
        />
      )}
    </div>
  )
}
