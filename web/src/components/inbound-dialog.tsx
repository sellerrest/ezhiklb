import { useState } from "react"

import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { cn } from "@/lib/utils"
import type { Inbound, InboundMode } from "@/types"

const isIPv4OrWildcard = (value: string) => {
  if (value === "0.0.0.0") return true
  const parts = value.split(".")
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)
}

export function InboundDialog({ initial, others, onSave, onClose }: { initial: Inbound; others: Inbound[]; onSave: (inbound: Inbound) => void; onClose: () => void }) {
  const [inbound, setInbound] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const patch = (values: Partial<Inbound>) => setInbound((current) => ({ ...current, ...values }))

  const submit = () => {
    const nextErrors: Record<string, string> = {}
    if (!inbound.name.trim()) nextErrors.name = "Укажите название"
    if (!isIPv4OrWildcard(inbound.listen_address)) nextErrors.listen_address = "Укажите 0.0.0.0 или корректный IPv4-адрес"
    if (!Number.isInteger(inbound.listen_port) || inbound.listen_port < 1 || inbound.listen_port > 65535) nextErrors.listen_port = "Порт должен быть от 1 до 65535"
    if (!inbound.tcp && !inbound.udp) nextErrors.protocol = "Включите прослушку TCP, UDP или обоих"
    const conflict = others.some(
      (candidate) => (candidate.listen_address === inbound.listen_address || candidate.listen_address === "0.0.0.0" || inbound.listen_address === "0.0.0.0") && candidate.listen_port === inbound.listen_port,
    )
    if (conflict) nextErrors.listen_port = "Этот хост и порт уже заняты другим входящим"
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length === 0) onSave({ ...inbound, name: inbound.name.trim() })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{initial.name ? `Редактирование · ${initial.name}` : "Новое входящее"}</DialogTitle>
          <DialogDescription>Какой хост и порт слушать, и в каком режиме разбирать трафик.</DialogDescription>
        </DialogHeader>

        <div className="flex items-center justify-between rounded-lg border p-3">
          <span className="text-sm font-medium">{inbound.enabled ? "Входящее включено" : "Входящее выключено"}</span>
          <Switch checked={inbound.enabled} onCheckedChange={(enabled) => patch({ enabled })} />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Название</Label>
          <Input value={inbound.name} onChange={(e) => patch({ name: e.target.value })} />
          {errors.name && <span className="text-destructive text-xs">{errors.name}</span>}
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label>Хост</Label>
            <Input className="font-mono" placeholder="0.0.0.0" value={inbound.listen_address} onChange={(e) => patch({ listen_address: e.target.value })} />
            {errors.listen_address && <span className="text-destructive text-xs">{errors.listen_address}</span>}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>Порт</Label>
            <Input type="number" min={1} max={65535} value={inbound.listen_port} onChange={(e) => patch({ listen_port: Number(e.target.value) })} />
            {errors.listen_port && <span className="text-destructive text-xs">{errors.listen_port}</span>}
          </div>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Режим</Label>
          <Select value={inbound.mode} onValueChange={(v) => patch({ mode: v as InboundMode })}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="tcp">TCP (по SNI)</SelectItem>
              <SelectItem value="http">HTTP (по SNI и пути)</SelectItem>
            </SelectContent>
          </Select>
          <span className="text-muted-foreground text-xs">В связующих для этого входящего будут доступны условия {inbound.mode === "http" ? "по SNI и по URI-пути" : "по SNI"}.</span>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label>Прослушка</Label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => patch({ tcp: !inbound.tcp })}
              className={cn("flex-1 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors", inbound.tcp ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground")}
            >
              TCP
            </button>
            <button
              type="button"
              onClick={() => patch({ udp: !inbound.udp })}
              className={cn("flex-1 rounded-lg border px-3 py-2 text-sm font-semibold transition-colors", inbound.udp ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground")}
            >
              UDP
            </button>
          </div>
          {errors.protocol && <span className="text-destructive text-xs">{errors.protocol}</span>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Отмена
          </Button>
          <Button onClick={submit}>Сохранить входящее</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
