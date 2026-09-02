export type Protocol = "tcp" | "udp"

export interface HealthCheck {
  enabled: boolean
  interval_seconds: number
  timeout_millis: number
  failure_threshold: number
  recovery_threshold: number
}

export type InboundMode = "tcp" | "http"

export interface Inbound {
  id: string
  name: string
  enabled: boolean
  listen_address: string
  listen_port: number
  mode: InboundMode
  tcp: boolean
  udp: boolean
}

export interface Outbound {
  id: string
  name: string
  address: string
  port: number
  enabled: boolean
  health_check: HealthCheck
}

export type MatchField = "sni" | "path"
export type MatchOperator = "equals" | "not_equals" | "contains" | "not_contains" | "starts_with" | "not_starts_with"

export interface MatchCondition {
  field: MatchField
  operator: MatchOperator
  value: string
}

export interface MatchGroup {
  conditions: MatchCondition[]
}

export type SelectionStrategy = "ping" | "least" | "manual"

export interface BindingTarget {
  outbound_id: string
  weight_percent: number
}

export interface Binding {
  id: string
  name: string
  enabled: boolean
  inbound_id: string
  affinity_seconds: number
  selection_strategy: SelectionStrategy
  groups: MatchGroup[]
  targets: BindingTarget[]
}

export interface CoreConfig {
  schema_version: number
  inbounds: Inbound[]
  outbounds: Outbound[]
  bindings: Binding[]
}

// "Ядро" (Core) — was "Профиль"/"Profile" upstream. Same shape (a reusable
// set of inbound listeners + their outbound backends assignable to nodes);
// renamed for clarity per the panel UX rework.
export interface Core {
  id: string
  name: string
  description: string
  created_at: string
  updated_at: string
}

export interface CoreRevision {
  id: number
  core_id: string
  number: number
  version: string
  config: CoreConfig
  created_at: string
}

export interface AuditEvent {
  id: number
  action: string
  target_type: string
  target_id: string
  details: string
  created_at: string
}

export interface NodeInfo {
  id: string
  name: string
  ingress_address: string
  // Where the panel dials *out* to this node's own local control API — the
  // protocol inversion means the node no longer needs an inbound-reachable
  // panel port; the panel needs an outbound route to this address instead.
  control_address: string
  control_port: number
  cert_fingerprint: string
  core_id: string
  desired_revision: number
  applied_revision: number
  agent_version: string
  status: "connecting" | "online" | "offline" | "disabled" | "deleting"
  apply_state: "waiting" | "applying" | "applied" | "error" | "disabled" | "decommissioning"
  apply_error?: string
  last_seen_at?: string
  online_since?: string
  metrics?: NodeMetrics
  diagnostics?: NodeDiagnostics
  update_target?: string
  update_state?: "idle" | "requested" | "downloading" | "verifying" | "installing" | "restarting" | "completed" | "error"
  update_error?: string
  enabled: boolean
  // Per-node overrides — null means "use the panel-wide default".
  poll_interval_seconds: number | null
  timeout_seconds: number | null
  // Computed: how many of this node's used, enabled outbounds are alive.
  outbound_alive: number
  outbound_total: number
  updated_at: string
}

export interface NodeInboundBreakdown {
  inbound_id: string
  name: string
  listen_address: string
  listen_port: number
  online_ips: number
  rx_bps: number
  tx_bps: number
}

export interface NodeOutboundBreakdown {
  outbound_id: string
  name: string
  address: string
  port: number
  enabled: boolean
  reachable: boolean
  online_ips: number
  rx_bps: number
  tx_bps: number
}

export interface NodeBreakdown {
  inbounds: NodeInboundBreakdown[]
  outbounds: NodeOutboundBreakdown[]
  node: { active_ips: number; network_rx_bps: number; network_tx_bps: number }
}

export interface NodeDiagnostics {
  ipvs_available: boolean
  firewall_ready: boolean
  service_count: number
  destination_count: number
  error?: string
  checked_at: string
}

export interface NodeMetrics {
  ram_used_percent: number
  cpu_used_percent: number
  load_1: number
  cpu_cores: number
  network_rx_bps: number
  network_tx_bps: number
  active_ips: number
  collected_at: string
}

export interface NodeMetricPoint {
  node_id: string
  ram_used_percent: number
  cpu_used_percent: number
  load_1: number
  network_rx_bps: number
  network_tx_bps: number
  active_ips: number
  collected_at: string
}

// No more agent_port: the panel has no inbound-facing agent listener to
// configure any more, only its own admin web port.
export interface SystemSettings {
  panel_port: number
  db_backend: "sqlite" | "postgresql"
}

export interface Status {
  version: string
  cores: number
  nodes: number
  online_nodes: number
  listeners: number
}

export type OutboundLiveStatus = "alive" | "dead" | "unused"

export interface OutboundStatusEntry {
  core_id: string
  core_name: string
  outbound_id: string
  name: string
  address: string
  port: number
  enabled: boolean
  status: OutboundLiveStatus
  online_ips: number
}

export interface BackendHealth {
  node_id: string
  address: string
  state: "unknown" | "reachable" | "unreachable"
  consecutive_successes: number
  consecutive_failures: number
  latency_millis: number
  checked_at: string
}

export interface ServiceStat {
  node_id: string
  protocol: Protocol
  listen_address: string
  listen_port: number
  backend_address?: string
  backend_port?: number
  connections: number
  incoming_packets: number
  outgoing_packets: number
  incoming_bytes: number
  outgoing_bytes: number
  collected_at: string
}
