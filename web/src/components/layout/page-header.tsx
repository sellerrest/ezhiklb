import type { LucideIcon } from "lucide-react"
import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

// Simplified from PasarGuard's page-header.tsx: drops the per-page "docs
// tutorial" link (this fork has no hosted per-page docs) and i18n; keeps the
// sticky title/description/action-button layout.
export default function PageHeader({
  title,
  description,
  buttonText,
  onButtonClick,
  buttonIcon: Icon,
  className,
  action,
}: {
  title: string
  description?: string
  buttonText?: string
  onButtonClick?: () => void
  buttonIcon?: LucideIcon
  className?: string
  action?: ReactNode
}) {
  return (
    <div className={cn("bg-background/80 sticky top-0 z-20 flex w-full flex-row items-start justify-between gap-4 px-4 py-4 backdrop-blur-md md:pt-6", className)}>
      <div className="flex min-w-0 flex-1 flex-col gap-y-1">
        <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <span className="text-muted-foreground text-sm leading-relaxed whitespace-normal">{description}</span>}
      </div>
      {action}
      {buttonText && onButtonClick && (
        <div className="shrink-0">
          <button
            type="button"
            onClick={onButtonClick}
            className="bg-primary text-primary-foreground hover:bg-primary/90 inline-flex h-9 items-center gap-1.5 rounded-lg px-4 text-sm font-medium transition-colors"
          >
            {Icon && <Icon className="h-4 w-4" />}
            <span>{buttonText}</span>
          </button>
        </div>
      )}
    </div>
  )
}
