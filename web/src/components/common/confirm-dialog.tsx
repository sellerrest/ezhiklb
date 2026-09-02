import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"

export function ConfirmDialog({
  title,
  description,
  confirmLabel,
  danger = false,
  busy = false,
  onCancel,
  onConfirm,
}: {
  title: string
  description: string
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  onCancel: () => void
  onConfirm: () => void | Promise<void>
}) {
  return (
    <AlertDialog open onOpenChange={(open) => !open && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy} onClick={onCancel}>
            Отмена
          </AlertDialogCancel>
          <AlertDialogAction
            variant={danger ? "destructive" : "default"}
            disabled={busy}
            onClick={(event) => {
              event.preventDefault()
              void onConfirm()
            }}
          >
            {busy ? "Выполняю…" : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
