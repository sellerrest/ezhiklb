import { Monitor, Moon, Sun } from "lucide-react"
import { useCallback, useContext } from "react"

import { Theme, useTheme } from "@/app/providers/theme-provider"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { SidebarContext } from "@/components/ui/sidebar"

// Adapted from PasarGuard's components/common/theme-toggle.tsx, i18n removed.
export function ThemeToggle() {
  const { setTheme } = useTheme()
  const sidebarContext = useContext(SidebarContext)
  const sidebarState: "expanded" | "collapsed" = sidebarContext?.state ?? "expanded"
  const isMobile = sidebarContext?.isMobile ?? false

  const toggleTheme = useCallback((theme: Theme) => setTheme(theme), [setTheme])

  if (sidebarState === "collapsed" && !isMobile) {
    return (
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="outline" size="icon" className="h-8 w-8 transition-colors duration-200">
            <Sun className="transition-all duration-300 ease-in-out dark:hidden" />
            <Moon className="hidden transition-all duration-300 ease-in-out dark:block" />
            <span className="sr-only">Тема</span>
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-48 p-2" side="right" align="start">
          <div className="space-y-1">
            <div className="px-2 py-1.5 text-sm font-semibold">Тема</div>
            <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => toggleTheme("light")}>
              <Sun className="mr-2 h-4 w-4" />
              Светлая
            </Button>
            <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => toggleTheme("dark")}>
              <Moon className="mr-2 h-4 w-4" />
              Тёмная
            </Button>
            <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => toggleTheme("system")}>
              <Monitor className="mr-2 h-4 w-4" />
              Системная
            </Button>
          </div>
        </PopoverContent>
      </Popover>
    )
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="icon" className="transition-colors duration-200">
          <Sun className="transition-all duration-300 ease-in-out dark:hidden" />
          <Moon className="hidden transition-all duration-300 ease-in-out dark:block" />
          <span className="sr-only">Тема</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top">
        <DropdownMenuItem onClick={() => toggleTheme("light")}>
          <Sun className="mr-2 h-4 w-4" />
          Светлая
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => toggleTheme("dark")}>
          <Moon className="mr-2 h-4 w-4" />
          Тёмная
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => toggleTheme("system")}>
          <Monitor className="mr-2 h-4 w-4" />
          Системная
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
