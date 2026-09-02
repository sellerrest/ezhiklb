import type { BackendHealth, NodeInfo } from "@/types"

export function nodeVisualState(node: NodeInfo): "disabled" | "applying" | "online" | "offline" {
  if (!node.enabled) return "disabled"
  if (node.status === "connecting" || node.status === "deleting" || node.apply_state === "applying" || node.desired_revision !== node.applied_revision) return "applying"
  if (node.status === "online") return "online"
  return "offline"
}

export function healthStateLabel(state: BackendHealth["state"]) {
  return ({ reachable: "Доступен", unreachable: "Недоступен", unknown: "Нет данных" } as const)[state]
}

export function nodeStatusLabel(status?: NodeInfo["status"]) {
  return ({ connecting: "подключается", online: "online", offline: "offline", disabled: "выключена", deleting: "удаляется" } as const)[status ?? "offline"]
}

export function applyStateLabel(node: NodeInfo) {
  if (node.status === "deleting" || node.apply_state === "decommissioning") return "очистка и остановка"
  if (!node.enabled) return "выключена"
  if (node.apply_state === "error" || node.apply_error) return "ошибка применения"
  if (node.apply_state === "applying" || node.desired_revision !== node.applied_revision) return "применяется"
  if (node.apply_state === "applied") return "конфигурация актуальна"
  return "ожидает конфигурацию"
}

// A node stuck in "deleting" has no automatic timeout — its agent may be
// gone for good and will never confirm cleanup. Give the normal flow this
// long to self-resolve before offering the force-delete escape hatch.
const FORCE_DELETE_GRACE_MS = 60_000
export function forceDeleteEligible(node: NodeInfo): boolean {
  if (node.status !== "deleting") return false
  return Date.now() - new Date(node.updated_at).getTime() > FORCE_DELETE_GRACE_MS
}

export const updateStageInfo: Record<string, { percent: number; label: string }> = {
  requested: { percent: 8, label: "Отправлен запрос…" },
  downloading: { percent: 32, label: "Скачивание релиза…" },
  verifying: { percent: 58, label: "Проверка SHA-256…" },
  installing: { percent: 78, label: "Установка…" },
  restarting: { percent: 93, label: "Перезапуск агента…" },
}

export const releaseVersion = "1.0.0"
