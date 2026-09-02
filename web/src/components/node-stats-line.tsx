import { formatNetworkRate, formatNumber } from "@/lib/format"
import type { NodeInfo } from "@/types"

// The shared "Общий онлайн X | Активные исходящие A/T | Трафик: вход Y, исход Z"
// line — identical on Статистика and Узлы, per an explicit request that both
// pages show the same per-node stats.
export function NodeStatsLine({ node }: { node: NodeInfo }) {
  const online = node.metrics?.active_ips ?? 0
  const rx = node.metrics?.network_rx_bps ?? 0
  const tx = node.metrics?.network_tx_bps ?? 0
  return (
    <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs">
      <span>Общий онлайн {formatNumber(online)}</span>
      <span>
        Активные исходящие {node.outbound_alive}/{node.outbound_total}
      </span>
      <span>
        Трафик: входящий {formatNetworkRate(rx)}, исходящий {formatNetworkRate(tx)}
      </span>
    </div>
  )
}
