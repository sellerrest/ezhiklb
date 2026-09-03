import { AlertTriangle, Plus, Trash2 } from "lucide-react"
import { useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { cn } from "@/lib/utils"
import type { Binding, BindingAction, BindingMode, Inbound, MatchCondition, MatchField, MatchOperator, Outbound, SelectionStrategy } from "@/types"

const fieldLabels: Record<MatchField, string> = { sni: "SNI (хост)", path: "URI-путь" }
const operatorLabels: Record<MatchOperator, string> = {
  equals: "Равно",
  not_equals: "Не равно",
  contains: "Содержит",
  not_contains: "Не содержит",
  starts_with: "Начинается с",
  not_starts_with: "Не начинается с",
}
const strategyLabels: Record<SelectionStrategy, string> = {
  least: "Least — равномерно между исходящими",
  ping: "By ping — туда, где меньше пинг",
  manual: "Manual — вручную по процентам",
}
const modeLabels: Record<BindingMode, string> = { tcp: "L4 / TCP (по SNI)", http: "L7 / HTTP (по SNI и пути)" }

const affinityPresets = [
  { value: 0, label: "Выключено" },
  { value: 900, label: "15 минут" },
  { value: 1800, label: "30 минут" },
  { value: 3600, label: "1 час" },
  { value: 10800, label: "3 часа" },
  { value: 18000, label: "5 часов" },
  { value: 86400, label: "24 часа" },
]

const newCondition = (): MatchCondition => ({ field: "sni", operator: "equals", value: "" })

export function BindingDialog({
  initial,
  inbounds,
  outbounds,
  allBindings,
  onSave,
  onClose,
}: {
  initial: Binding
  inbounds: Inbound[]
  outbounds: Outbound[]
  allBindings: Binding[]
  onSave: (binding: Binding) => void
  onClose: () => void
}) {
  const [binding, setBinding] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const patch = (values: Partial<Binding>) => setBinding((current) => ({ ...current, ...values }))
  const inbound = inbounds.find((i) => i.id === binding.inbound_id)

  // Every binding sharing an inbound must run the same engine (one listening
  // socket can't be both a raw TCP splicer and an HTTP terminator at once) —
  // once a sibling binding has already picked a mode, this one just follows it.
  const siblings = allBindings.filter((b) => b.id !== binding.id && b.inbound_id === binding.inbound_id)
  const lockedMode = siblings.find((b) => b.mode)?.mode
  const hasSiblingDefault = siblings.some((b) => b.groups.length === 0)
  const isDefault = binding.groups.length === 0
  const availableFields: MatchField[] = binding.mode === "http" ? ["sni", "path"] : ["sni"]
  const udpWillBeDisabled = Boolean(inbound?.udp) && binding.groups.some((g) => g.conditions.length > 0)

  const selectInbound = (inboundId: string) => {
    const sibling = allBindings.find((b) => b.id !== binding.id && b.inbound_id === inboundId)
    patch({ inbound_id: inboundId, groups: [], mode: sibling?.mode ?? binding.mode })
  }

  const patchGroupCondition = (gi: number, ci: number, values: Partial<MatchCondition>) =>
    patch({ groups: binding.groups.map((group, i) => (i === gi ? { conditions: group.conditions.map((c, j) => (j === ci ? { ...c, ...values } : c)) } : group)) })
  const addCondition = (gi: number) => patch({ groups: binding.groups.map((group, i) => (i === gi ? { conditions: [...group.conditions, newCondition()] } : group)) })
  const removeCondition = (gi: number, ci: number) => patch({ groups: binding.groups.map((group, i) => (i === gi ? { conditions: group.conditions.filter((_, j) => j !== ci) } : group)) })
  const addGroup = () => patch({ groups: [...binding.groups, { conditions: [newCondition()] }] })
  const removeGroup = (gi: number) => patch({ groups: binding.groups.filter((_, i) => i !== gi) })

  const toggleTarget = (outboundId: string, checked: boolean) => {
    if (checked) patch({ targets: [...binding.targets, { outbound_id: outboundId, weight_percent: 100 }] })
    else patch({ targets: binding.targets.filter((t) => t.outbound_id !== outboundId) })
  }
  const setTargetWeight = (outboundId: string, weight: number) => patch({ targets: binding.targets.map((t) => (t.outbound_id === outboundId ? { ...t, weight_percent: weight } : t)) })
  const weightSum = binding.targets.reduce((sum, t) => sum + t.weight_percent, 0)

  const setAction = (action: BindingAction) => patch({ action, targets: action === "drop" ? [] : binding.targets })

  const submit = () => {
    const nextErrors: Record<string, string> = {}
    if (!binding.inbound_id) nextErrors.inbound_id = "Выберите входящий"
    if (lockedMode && binding.mode !== lockedMode) nextErrors.mode = `Режим для этого входящего уже задан как ${modeLabels[lockedMode]}`
    if (isDefault && hasSiblingDefault) nextErrors.groups = "У этого входящего уже есть правило по умолчанию — оставьте условия здесь или уберите их в том правиле"
    for (const group of binding.groups) for (const condition of group.conditions) if (!condition.value.trim()) nextErrors.groups = "Заполните значения всех условий или удалите пустые строки"
    if (binding.action === "forward" && binding.targets.length === 0) nextErrors.targets = "Выберите хотя бы один исходящий"
    if (binding.action === "forward" && binding.selection_strategy === "manual" && binding.targets.length > 0 && weightSum !== 100) nextErrors.weights = "Сумма процентов должна быть равна 100"
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length === 0) onSave({ ...binding, name: binding.name.trim() || bindingDefaultName(binding, inbounds, outbounds) })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{initial.name ? `Редактирование · ${initial.name}` : "Новое связующее"}</DialogTitle>
          <DialogDescription>Свяжите входящий с одним или несколькими исходящими и правилами маршрутизации.</DialogDescription>
        </DialogHeader>

        <div className="flex items-center justify-between rounded-lg border p-3">
          <span className="text-sm font-medium">{binding.enabled ? "Связующее включено" : "Связующее выключено"}</span>
          <Switch checked={binding.enabled} onCheckedChange={(enabled) => patch({ enabled })} />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Название (необязательно)</Label>
          <Input value={binding.name} onChange={(e) => patch({ name: e.target.value })} />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Входящий</Label>
          <Select value={binding.inbound_id} onValueChange={selectInbound}>
            <SelectTrigger>
              <SelectValue placeholder="Выберите входящий" />
            </SelectTrigger>
            <SelectContent>
              {inbounds.map((i) => (
                <SelectItem key={i.id} value={i.id}>
                  {i.name} · {i.listen_address}:{i.listen_port}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {errors.inbound_id && <span className="text-destructive text-xs">{errors.inbound_id}</span>}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Режим</Label>
          <Select value={binding.mode} disabled={Boolean(lockedMode)} onValueChange={(v) => patch({ mode: v as BindingMode })}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(Object.keys(modeLabels) as BindingMode[]).map((mode) => (
                <SelectItem key={mode} value={mode}>
                  {modeLabels[mode]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-muted-foreground text-xs">
            {lockedMode ? "Режим уже задан другим связующим для этого входящего — один входящий работает только в одном режиме." : "L4 матчит только по SNI (не расшифровывая TLS); L7 дополнительно матчит по URI-пути (обычный HTTP, без TLS)."}
          </span>
          {errors.mode && <span className="text-destructive text-xs">{errors.mode}</span>}
        </div>

        <div>
          <div className="mb-2 flex items-center justify-between">
            <div>
              <p className="text-muted-foreground text-xs font-semibold uppercase">Правила</p>
              <h3 className="text-sm font-semibold">{isDefault ? "Правило по умолчанию (весь остальной трафик)" : `${binding.groups.length} групп условий`}</h3>
            </div>
            <Button variant="outline" size="sm" disabled={!binding.inbound_id} onClick={addGroup}>
              <Plus className="h-4 w-4" /> Группа условий (ИЛИ)
            </Button>
          </div>

          <div className="flex flex-col gap-2">
            {binding.groups.map((group, gi) => (
              <div key={gi}>
                {gi > 0 && <div className="text-muted-foreground my-2 text-center text-xs font-semibold">ИЛИ</div>}
                <Card className="flex flex-col gap-2 p-3">
                  {group.conditions.map((condition, ci) => (
                    <div key={ci}>
                      {ci > 0 && <div className="text-muted-foreground my-1 text-xs font-semibold">И</div>}
                      <div className="grid grid-cols-[1fr_1fr_1.4fr_auto] items-center gap-2">
                        <Select value={condition.field} onValueChange={(v) => patchGroupCondition(gi, ci, { field: v as MatchField })}>
                          <SelectTrigger className="h-8">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {availableFields.map((field) => (
                              <SelectItem key={field} value={field}>
                                {fieldLabels[field]}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        <Select value={condition.operator} onValueChange={(v) => patchGroupCondition(gi, ci, { operator: v as MatchOperator })}>
                          <SelectTrigger className="h-8">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {(Object.keys(operatorLabels) as MatchOperator[]).map((op) => (
                              <SelectItem key={op} value={op}>
                                {operatorLabels[op]}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        <Input className="h-8" placeholder={condition.field === "sni" ? "example.com" : "/api"} value={condition.value} onChange={(e) => patchGroupCondition(gi, ci, { value: e.target.value })} />
                        <Button variant="ghost" size="icon" className="text-destructive h-8 w-8" onClick={() => removeCondition(gi, ci)}>
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    </div>
                  ))}
                  <div className="flex items-center justify-between">
                    <Button variant="outline" size="sm" onClick={() => addCondition(gi)}>
                      <Plus className="h-4 w-4" /> Условие (И)
                    </Button>
                    <Button variant="ghost" size="sm" className="text-destructive" onClick={() => removeGroup(gi)}>
                      Удалить группу
                    </Button>
                  </div>
                </Card>
              </div>
            ))}
          </div>
          {errors.groups && <span className="text-destructive text-xs">{errors.groups}</span>}
          {udpWillBeDisabled && (
            <div className="text-destructive bg-destructive/10 border-destructive/30 mt-2 flex items-start gap-2 rounded-lg border p-2.5 text-xs">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>При включении правил маршрутизации трафика UDP пакеты перестают обрабатываться. UDP не несёт SNI/путь, чтобы проверить эти условия — на этом входящем он будет отключён.</span>
            </div>
          )}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Что делать с подходящим трафиком</Label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setAction("forward")}
              className={cn("flex-1 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors", binding.action === "forward" ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground")}
            >
              Пересылать
            </button>
            <button
              type="button"
              onClick={() => setAction("drop")}
              className={cn("flex-1 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors", binding.action === "drop" ? "border-destructive bg-destructive/10 text-destructive" : "text-muted-foreground")}
            >
              Drop Packet — сбросить соединение
            </button>
          </div>
          {isDefault && binding.action === "drop" && <span className="text-muted-foreground text-xs">Весь трафик, не подошедший ни под одно правило этого входящего, будет разрываться.</span>}
        </div>

        {binding.action === "forward" && (
          <>
            <div>
              <p className="text-muted-foreground mb-2 text-xs font-semibold uppercase">Исходящие</p>
              <div className="flex flex-col gap-2">
                {outbounds.map((outbound) => {
                  const target = binding.targets.find((t) => t.outbound_id === outbound.id)
                  const checked = Boolean(target)
                  return (
                    <div key={outbound.id} className="flex items-center gap-3 rounded-lg border p-2">
                      <Checkbox checked={checked} onCheckedChange={(c) => toggleTarget(outbound.id, Boolean(c))} aria-label={`Выбрать ${outbound.name}`} />
                      <span className="flex-1 truncate text-sm">{outbound.name}</span>
                      <span className="text-muted-foreground font-mono text-xs">
                        {outbound.address}:{outbound.port}
                      </span>
                      {checked && binding.selection_strategy === "manual" && (
                        <div className="flex items-center gap-1">
                          <Input className="h-8 w-16" type="number" min={1} max={100} value={target!.weight_percent} onChange={(e) => setTargetWeight(outbound.id, Number(e.target.value))} />
                          <span className="text-muted-foreground text-xs">%</span>
                        </div>
                      )}
                    </div>
                  )
                })}
                {outbounds.length === 0 && <div className="text-muted-foreground rounded-lg border border-dashed p-4 text-center text-sm">Сначала добавьте исходящие</div>}
              </div>
              {errors.targets && <span className="text-destructive text-xs">{errors.targets}</span>}
              {binding.selection_strategy === "manual" && binding.targets.length > 0 && (
                <div className="mt-1 text-xs">
                  Сумма: <Badge variant={weightSum === 100 ? "green" : "red"}>{weightSum}%</Badge>
                </div>
              )}
              {errors.weights && <span className="text-destructive text-xs">{errors.weights}</span>}
            </div>

            {binding.targets.length > 1 && (
              <div className="flex flex-col gap-1.5">
                <Label>Критерий выбора исходящего</Label>
                <Select value={binding.selection_strategy} onValueChange={(v) => patch({ selection_strategy: v as SelectionStrategy })}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(Object.keys(strategyLabels) as SelectionStrategy[]).map((strategy) => (
                      <SelectItem key={strategy} value={strategy}>
                        {strategyLabels[strategy]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <Label>Affinity</Label>
              <Select value={String(binding.affinity_seconds)} onValueChange={(v) => patch({ affinity_seconds: Number(v) })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {affinityPresets.map((preset) => (
                    <SelectItem key={preset.value} value={String(preset.value)}>
                      {preset.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <span className="text-muted-foreground text-xs">Закрепляет IP клиента за одним исходящим на указанное время</span>
            </div>
          </>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button onClick={submit}>Сохранить связующее</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function bindingDefaultName(binding: Binding, inbounds: Inbound[], outbounds: Outbound[]): string {
  const inbound = inbounds.find((i) => i.id === binding.inbound_id)
  if (binding.action === "drop") return `${inbound?.name || "?"} → Drop`
  const target = outbounds.find((o) => o.id === binding.targets[0]?.outbound_id)
  return `${inbound?.name || "?"} → ${target?.name || "?"}`
}
