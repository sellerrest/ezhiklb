import type { AuditEvent, BackendHealth, Core, CoreConfig, CoreRevision, NodeBreakdown, NodeInfo, NodeMetricPoint, OutboundStatusEntry, ServiceStat, Status, SystemSettings } from "../types"

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers: { "Content-Type": "application/json", ...options?.headers },
  })
  if (!response.ok) {
    const body = await response.json().catch(() => null)
    throw new ApiError(response.status, body?.error?.message ?? body?.detail?.error?.message ?? `HTTP ${response.status}`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

// The panel API always intends to return `[]` for an empty list, but any
// future regression (server or network) could resend something malformed.
// Every list endpoint goes through this so a bad payload degrades to an
// empty list instead of crashing the whole render tree.
async function requestArray<T>(path: string, options?: RequestInit): Promise<T[]> {
  const result = await request<T[] | null | undefined>(path, options)
  return Array.isArray(result) ? result : []
}

export interface NodeEnrollmentPayload {
  name: string
  ingress_address: string
  control_address: string
  control_port: number
  api_key: string
  cert_pem: string
  core_id: string
  poll_interval_seconds?: number | null
  timeout_seconds?: number | null
}

export const api = {
  setupRequired: () => request<{ setup_required: boolean }>("/api/v1/auth/setup-required"),
  login: (login: string, password: string) => request<{ authenticated: boolean }>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ login, password }) }),
  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),
  status: () => request<Status>("/api/v1/status"),

  cores: () => requestArray<Core>("/api/v1/cores"),
  core: (id: string) => request<{ core: Core; revision: CoreRevision }>(`/api/v1/cores/${id}`),
  createCore: (name: string, description: string, config: CoreConfig) =>
    request<{ core: Core; revision: CoreRevision }>("/api/v1/cores", { method: "POST", body: JSON.stringify({ name, description, config }) }),
  publishCore: (id: string, name: string, description: string, config: CoreConfig, resetConnections = false) =>
    request<{ core: Core; revision: CoreRevision }>(`/api/v1/cores/${id}`, { method: "PUT", body: JSON.stringify({ name, description, config, reset_connections: resetConnections }) }),
  cloneCore: (id: string, name: string) => request<{ core: Core; revision: CoreRevision }>(`/api/v1/cores/${id}/clone`, { method: "POST", body: JSON.stringify({ name }) }),
  deleteCore: (id: string) => request<void>(`/api/v1/cores/${id}`, { method: "DELETE" }),

  nodes: () => requestArray<NodeInfo>("/api/v1/nodes"),
  // One-step enrollment (point 4 of the fork): the operator pastes every
  // connection field the node's install script printed, in one dialog.
  createNode: (payload: NodeEnrollmentPayload) =>
    request<{ node: NodeInfo; connected: boolean; connect_error: string }>("/api/v1/nodes", { method: "POST", body: JSON.stringify(payload) }),
  updateNode: (
    id: string, name: string, ingressAddress: string, controlAddress: string, controlPort: number,
    apiKey = "", certPem = "", pollIntervalSeconds: number | null = null, timeoutSeconds: number | null = null,
  ) =>
    request<void>(`/api/v1/nodes/${id}`, {
      method: "PUT",
      body: JSON.stringify({
        name, ingress_address: ingressAddress, control_address: controlAddress, control_port: controlPort, api_key: apiKey, cert_pem: certPem,
        poll_interval_seconds: pollIntervalSeconds, timeout_seconds: timeoutSeconds,
      }),
    }),
  deleteNode: (id: string) => request<void>(`/api/v1/nodes/${id}`, { method: "DELETE" }),
  forceDeleteNode: (id: string) => request<void>(`/api/v1/nodes/${id}/force-delete`, { method: "POST" }),
  setNodeEnabled: (id: string, enabled: boolean) => request<void>(`/api/v1/nodes/${id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) }),
  requestHealthProbe: (id: string) => request<{ health: BackendHealth[] }>(`/api/v1/nodes/${id}/health-probe`, { method: "POST" }),
  // "Проверить статус"/"Переподключить" — an immediate reachability probe.
  checkNodeConnectivity: (id: string) => request<{ connected: boolean; state: unknown }>(`/api/v1/nodes/${id}/check`, { method: "POST" }),
  requestNodeUpdate: (id: string) => request<{ version: string }>(`/api/v1/nodes/${id}/update`, { method: "POST" }),
  // "Синхронизировать" — polls and pushes to this node right now.
  syncNode: (id: string) => request<NodeInfo>(`/api/v1/nodes/${id}/sync`, { method: "POST" }),
  nodeBreakdown: (id: string) => request<NodeBreakdown>(`/api/v1/nodes/${id}/breakdown`),
  health: () => requestArray<BackendHealth>("/api/v1/health"),
  stats: () => requestArray<ServiceStat>("/api/v1/stats"),
  outbounds: () => requestArray<OutboundStatusEntry>("/api/v1/outbounds"),
  metricHistory: (nodeID = "all") => requestArray<NodeMetricPoint>(`/api/v1/metrics/history?node_id=${encodeURIComponent(nodeID)}`),
  events: (filter = "all") => requestArray<AuditEvent>(`/api/v1/events?filter=${encodeURIComponent(filter)}`),
  settings: () => request<SystemSettings>("/api/v1/settings"),
  assignCore: (nodeID: string, coreID: string) => request<void>(`/api/v1/nodes/${nodeID}/profile`, { method: "PUT", body: JSON.stringify({ core_id: coreID }) }),
}
