import type { Binding, Inbound, MatchGroup, Outbound } from "@/types"

const MAX_SUMMARY_LENGTH = 64

function groupText(group: MatchGroup): string {
  return group.conditions.map((c) => c.value || "?").join(" and ")
}

export function bindingRuleSummary(binding: Binding): string {
  if (binding.groups.length === 0) return "весь трафик"
  const text = binding.groups.map(groupText).join(" or ")
  return text.length > MAX_SUMMARY_LENGTH ? `${text.slice(0, MAX_SUMMARY_LENGTH - 1)}…` : text
}

export function bindingStrip(binding: Binding, inbounds: Inbound[], outbounds: Outbound[]): string {
  const inbound = inbounds.find((i) => i.id === binding.inbound_id)
  const targetNames = binding.targets.map((t) => outbounds.find((o) => o.id === t.outbound_id)?.name || "?").join(" + ")
  return `${inbound?.name || "?"} -> ${bindingRuleSummary(binding)} -> ${targetNames || "?"}`
}
