import { useState } from "react"

import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import type { Outbound } from "@/types"

const isIPv4 = (value: string) => {
  const parts = value.split(".")
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)
}

export function OutboundDialog({ initial, others, onSave, onClose }: { initial: Outbound; others: Outbound[]; onSave: (outbound: Outbound) => void; onClose: () => void }) {
  const [outbound, setOutbound] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const patch = (values: Partial<Outbound>) => setOutbound((current) => ({ ...current, ...values }))
  const patchHealth = (values: Partial<Outbound["health_check"]>) => patch({ health_check: { ...outbound.health_check, ...values } })

  const submit = () => {
    const nextErrors: Record<string, string> = {}
    if (!outbound.name.trim()) nextErrors.name = "Укажите название"
    if (!isIPv4(outbound.address)) nextErrors.address = "Укажите корректный IPv4-адрес"
    if (!Number.isInteger(outbound.port) || outbound.port < 1 || outbound.port > 65535) nextErrors.port = "Порт должен быть от 1 до 65535"
    const endpointTaken = others.some((candidate) => candidate.address === outbound.address && candidate.port === outbound.port)
    if (endpointTaken) nextErrors.address = "Такой адрес и порт уже используются другим исходящим"
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length === 0) onSave({ ...outbound, name: outbound.name.trim() })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{initial.name ? `Редактирование · ${initial.name}` : "Новое исходящее"}</DialogTitle>
          <DialogDescription>Куда пересылать трафик и как проверять доступность сервера.</DialogDescription>
        </DialogHeader>

        <div className="flex items-center justify-between rounded-lg border p-3">
          <span className="text-sm font-medium">{outbound.enabled ? "Исходящее включено" : "Исходящее выключено"}</span>
          <Switch checked={outbound.enabled} onCheckedChange={(enabled) => patch({ enabled })} />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Название</Label>
          <Input value={outbound.name} onChange={(e) => patch({ name: e.target.value })} />
          {errors.name && <span className="text-destructive text-xs">{errors.name}</span>}
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label>IP-адрес</Label>
            <Input className="font-mono" placeholder="203.0.113.10" value={outbound.address} onChange={(e) => patch({ address: e.target.value })} />
            {errors.address && <span className="text-destructive text-xs">{errors.address}</span>}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Порт</Label>
            <Input type="number" min={1} max={65535} value={outbound.port} onChange={(e) => patch({ port: Number(e.target.value) })} />
            {errors.port && <span className="text-destructive text-xs">{errors.port}</span>}
          </div>
        </div>

        <Card className="flex flex-col gap-3 p-4">
          <div className="flex items-center justify-between">
            <div>
              <div className="text-sm font-semibold">Проверка жизни</div>
              <div className="text-muted-foreground text-xs">
                TCP-пинг на {outbound.address || "IP"}:{outbound.port || "порт"} — проверяет именно сервис на этом порту, а не хост целиком, и отключает исходящее при сбоях.
              </div>
            </div>
            <Switch checked={outbound.health_check.enabled} onCheckedChange={(enabled) => patchHealth({ enabled })} />
          </div>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="flex flex-col gap-1">
              <Label className="text-xs">Интервал, с</Label>
              <Input type="number" min={1} max={3600} value={outbound.health_check.interval_seconds} onChange={(e) => patchHealth({ interval_seconds: Number(e.target.value) })} />
            </div>
            <div className="flex flex-col gap-1">
              <Label className="text-xs">Timeout, мс</Label>
              <Input type="number" min={100} max={30000} step={100} value={outbound.health_check.timeout_millis} onChange={(e) => patchHealth({ timeout_millis: Number(e.target.value) })} />
            </div>
            <div className="flex flex-col gap-1">
              <Label className="text-xs">До отключения</Label>
              <Input type="number" min={1} max={100} value={outbound.health_check.failure_threshold} onChange={(e) => patchHealth({ failure_threshold: Number(e.target.value) })} />
            </div>
            <div className="flex flex-col gap-1">
              <Label className="text-xs">До возврата</Label>
              <Input type="number" min={1} max={100} value={outbound.health_check.recovery_threshold} onChange={(e) => patchHealth({ recovery_threshold: Number(e.target.value) })} />
            </div>
          </div>
        </Card>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button onClick={submit}>Сохранить исходящее</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
