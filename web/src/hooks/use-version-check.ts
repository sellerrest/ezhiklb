import { useEffect, useState } from "react"

// Adapted from PasarGuard's hooks/use-version-check.ts (no react-query in
// this app, so this is a plain fetch + localStorage cache instead).

export const REPO_URL = "https://github.com/sellerrest/ezhiklb"
const REPO_SLUG = "sellerrest/ezhiklb"

interface CachedRelease {
  version: string
  url: string
  timestamp: number
}

interface VersionCheckResult {
  hasUpdate: boolean
  latestVersion: string | null
  releaseUrl: string | null
  isLoading: boolean
}

const CACHE_KEY = "ezhiklb_release"
const CACHE_DURATION = 10 * 60 * 1000

function compareVersions(current: string, latest: string): number {
  const currentParts = current.replace(/^v/, "").split(".").map(Number)
  const latestParts = latest.replace(/^v/, "").split(".").map(Number)
  for (let i = 0; i < Math.max(currentParts.length, latestParts.length); i++) {
    const curr = currentParts[i] || 0
    const lat = latestParts[i] || 0
    if (curr < lat) return -1
    if (curr > lat) return 1
  }
  return 0
}

function getCached(): CachedRelease | null {
  try {
    const cached = localStorage.getItem(CACHE_KEY)
    return cached ? JSON.parse(cached) : null
  } catch {
    return null
  }
}

function setCache(version: string, url: string): void {
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify({ version, url, timestamp: Date.now() } satisfies CachedRelease))
  } catch {
    // ignore storage errors
  }
}

async function fetchLatestRelease(): Promise<{ version: string; url: string } | null> {
  const cached = getCached()
  if (cached && Date.now() - cached.timestamp < CACHE_DURATION) return { version: cached.version, url: cached.url }

  try {
    const response = await fetch(`https://api.github.com/repos/${REPO_SLUG}/releases/latest`, {
      referrerPolicy: "no-referrer",
      credentials: "omit",
      headers: { Accept: "application/vnd.github.v3+json" },
    })
    if (!response.ok) return cached ? { version: cached.version, url: cached.url } : null
    const data = await response.json()
    const version = (data.tag_name ?? "").replace(/^v/, "")
    const url = data.html_url ?? ""
    if (version) setCache(version, url)
    return version ? { version, url } : null
  } catch {
    return cached ? { version: cached.version, url: cached.url } : null
  }
}

export function useVersionCheck(currentVersion: string | null): VersionCheckResult {
  const [state, setState] = useState<{ version: string | null; url: string | null; loading: boolean }>({ version: null, url: null, loading: true })

  useEffect(() => {
    let active = true
    setState((s) => ({ ...s, loading: true }))
    void fetchLatestRelease().then((result) => {
      if (!active) return
      setState({ version: result?.version ?? null, url: result?.url ?? null, loading: false })
    })
    return () => {
      active = false
    }
  }, [])

  const hasUpdate = Boolean(currentVersion && state.version && compareVersions(currentVersion, state.version) < 0)
  return { hasUpdate, latestVersion: state.version, releaseUrl: state.url, isLoading: state.loading }
}
