import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react"

// Simplified from PasarGuard's theme-provider.tsx: keeps light/dark/system
// switching and localStorage persistence, drops the multi-accent-color
// theme picker and density/surface customization system (constants/
// color-themes.ts, lib/theme-color.ts) — this fork only needs the one dark
// palette already baked into index.css.

export type Theme = "dark" | "light" | "system"

type ThemeProviderState = {
  theme: Theme
  radius: string
  resolvedTheme: "light" | "dark"
  setTheme: (theme: Theme) => void
}

const STORAGE_KEY = "theme"
const initialState: ThemeProviderState = {
  theme: "system",
  radius: "0.5rem",
  resolvedTheme: "dark",
  setTheme: () => null,
}

const ThemeProviderContext = createContext<ThemeProviderState>(initialState)

function getSystemTheme(): "light" | "dark" {
  if (typeof window === "undefined") return "dark"
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

export function ThemeProvider({ children, defaultTheme = "system" }: { children: React.ReactNode; defaultTheme?: Theme }) {
  const [theme, setThemeState] = useState<Theme>(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY) as Theme | null
      return saved === "light" || saved === "dark" || saved === "system" ? saved : defaultTheme
    } catch {
      return defaultTheme
    }
  })
  const [resolvedTheme, setResolvedTheme] = useState<"light" | "dark">(() => (theme === "system" ? getSystemTheme() : theme))

  useEffect(() => {
    const next = theme === "system" ? getSystemTheme() : theme
    setResolvedTheme(next)
    const root = document.documentElement
    root.classList.remove("light", "dark")
    root.classList.add(next)
  }, [theme])

  useEffect(() => {
    if (theme !== "system") return
    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)")
    const onChange = () => {
      const next = getSystemTheme()
      setResolvedTheme(next)
      document.documentElement.classList.remove("light", "dark")
      document.documentElement.classList.add(next)
    }
    mediaQuery.addEventListener("change", onChange)
    return () => mediaQuery.removeEventListener("change", onChange)
  }, [theme])

  const setTheme = useCallback((next: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // ignore storage errors (private browsing, quota, etc.)
    }
    setThemeState(next)
  }, [])

  const value = useMemo<ThemeProviderState>(() => ({ theme, radius: "0.5rem", resolvedTheme, setTheme }), [theme, resolvedTheme, setTheme])

  return <ThemeProviderContext.Provider value={value}>{children}</ThemeProviderContext.Provider>
}

export function useTheme() {
  const context = useContext(ThemeProviderContext)
  if (context === undefined) throw new Error("useTheme must be used within a ThemeProvider")
  return context
}
