import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { useVersionCheck, REPO_URL } from "@/hooks/use-version-check"
import { cn } from "@/lib/utils"

// Adapted from PasarGuard's components/layout/version-badge.tsx: a small dot
// next to the installed version — green when up to date, amber with a
// tooltip (current -> latest) when a newer GitHub release exists. Clicking
// it opens the release page.
export function VersionBadge({ currentVersion, className }: { currentVersion: string | null; className?: string }) {
  const { hasUpdate, latestVersion, releaseUrl, isLoading } = useVersionCheck(currentVersion)
  if (isLoading || !currentVersion) return null

  const releaseLink = releaseUrl || `${REPO_URL}/releases/latest`
  const dotClass = hasUpdate ? "bg-amber-500 dark:bg-amber-400" : "bg-emerald-500/60 dark:bg-emerald-400/60"

  return (
    <TooltipProvider>
      <Tooltip delayDuration={100}>
        <TooltipTrigger asChild>
          <a href={releaseLink} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className={cn("inline-flex shrink-0", className)}>
            <span className={cn("h-1.5 w-1.5 rounded-full", dotClass)} />
          </a>
        </TooltipTrigger>
        <TooltipContent side="bottom" className="p-1.5">
          <div className="space-y-0.5 text-[10px]">
            {hasUpdate && latestVersion ? (
              <>
                <p className="font-medium">Доступно обновление</p>
                <p>
                  v{currentVersion} → v{latestVersion}
                </p>
                <p className="text-[9px]">Нажмите, чтобы открыть релиз</p>
              </>
            ) : (
              <p className="font-medium">Установлена последняя версия</p>
            )}
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
