import { Copy, MoreVertical, Plus, Search, Trash2 } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router"

import { ConfirmDialog } from "@/components/common/confirm-dialog"
import PageHeader from "@/components/layout/page-header"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { formatRelative } from "@/lib/format"
import type { Core, NodeInfo } from "@/types"

export default function CoresPage({ cores, nodes, onChanged }: { cores: Core[]; nodes: NodeInfo[]; onChanged: () => Promise<void> }) {
  const navigate = useNavigate()
  const [cloning, setCloning] = useState<Core | null>(null)
  const [cloneName, setCloneName] = useState("")
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState<Core | null>(null)
  const [query, setQuery] = useState("")
  const [selected, setSelected] = useState<Set<string>>(() => new Set())

  const filtered = cores.filter((core) => core.name.toLowerCase().includes(query.toLowerCase()))
  const toggleSelected = (id: string) =>
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const allSelected = filtered.length > 0 && filtered.every((core) => selected.has(core.id))

  return (
    <div className="flex flex-col gap-4 pb-8">
      <PageHeader title="Ядра" description="Входящие, исходящие и связующие для всех узлов." buttonText="Добавить ядро" buttonIcon={Plus} onButtonClick={() => navigate("/cores/new")} />

      <div className="mx-4 flex items-center gap-3">
        <div className="relative max-w-sm flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2" />
          <Input className="pl-9" placeholder="Поиск по названию" value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
      </div>
      <Separator className="mx-4 w-auto" />

      {filtered.length === 0 && <div className="text-muted-foreground mx-4 py-10 text-center text-sm">{query ? "Ничего не найдено" : "Создайте первое ядро, чтобы назначить его узлам."}</div>}

      {filtered.length > 0 && (
        <div className="mx-4">
          <Card className="overflow-hidden p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <Checkbox checked={allSelected} onCheckedChange={(checked) => setSelected(checked ? new Set(filtered.map((c) => c.id)) : new Set())} aria-label="Выбрать все" />
                  </TableHead>
                  <TableHead>Ядро</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((core) => {
                  const assigned = nodes.filter((node) => node.core_id === core.id).length
                  return (
                    <TableRow key={core.id} className="cursor-pointer" onClick={() => navigate(`/cores/${core.id}`)}>
                      <TableCell onClick={(e) => e.stopPropagation()}>
                        <Checkbox checked={selected.has(core.id)} onCheckedChange={() => toggleSelected(core.id)} aria-label={`Выбрать ${core.name}`} />
                      </TableCell>
                      <TableCell>
                        <div className="flex items-start gap-3">
                          <span className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${assigned > 0 ? "bg-success" : "bg-muted-foreground"}`} />
                          <div className="min-w-0">
                            <div className="text-sm font-semibold">{core.name}</div>
                            <div className="text-muted-foreground text-xs">
                              {assigned} {assigned === 1 ? "узел" : "узлов"} · изменено {formatRelative(core.updated_at)}
                            </div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell onClick={(e) => e.stopPropagation()}>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" className="h-8 w-8">
                              <MoreVertical className="h-4 w-4" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onClick={() => {
                                setCloning(core)
                                setCloneName(`${core.name} — копия`)
                              }}
                            >
                              <Copy className="h-4 w-4" /> Клонировать
                            </DropdownMenuItem>
                            <DropdownMenuItem disabled={assigned > 0} className="text-destructive focus:text-destructive" onClick={() => setDeleting(core)}>
                              <Trash2 className="h-4 w-4" /> Удалить
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </Card>
        </div>
      )}

      {cloning && (
        <ConfirmDialog
          title={`Клонирование · ${cloning.name}`}
          description="Будет создано независимое ядро с текущей конфигурацией."
          confirmLabel={busy ? "Создаю…" : "Создать копию"}
          busy={busy}
          onCancel={() => setCloning(null)}
          onConfirm={async () => {
            setBusy(true)
            try {
              await api.cloneCore(cloning.id, cloneName.trim())
              setCloning(null)
              await onChanged()
            } finally {
              setBusy(false)
            }
          }}
        />
      )}
      {deleting && (
        <ConfirmDialog
          title="Удалить ядро?"
          description={`Ядро «${deleting.name}» и его история версий будут удалены без возможности восстановления.`}
          confirmLabel="Удалить ядро"
          danger
          busy={busy}
          onCancel={() => setDeleting(null)}
          onConfirm={async () => {
            setBusy(true)
            try {
              await api.deleteCore(deleting.id)
              setDeleting(null)
              await onChanged()
            } finally {
              setBusy(false)
            }
          }}
        />
      )}
    </div>
  )
}
