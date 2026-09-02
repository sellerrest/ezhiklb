export function formatNumber(value: number) {
  return new Intl.NumberFormat("ru-RU", { notation: value >= 10000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value)
}

export function formatBytes(value: number) {
  if (value < 1024) return `${value} Б`
  const units = ["КБ", "МБ", "ГБ", "ТБ"]
  let next = value / 1024
  let unit = 0
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024
    unit++
  }
  return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 1 }).format(next)} ${units[unit]}`
}

export function formatPercent(value: number) {
  return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: 0 }).format(value)}%`
}

export function formatNetworkRate(bytesPerSecond: number) {
  const bits = bytesPerSecond * 8
  if (bits < 1000) return `${Math.round(bits)} бит/с`
  const units = ["Кбит/с", "Мбит/с", "Гбит/с"]
  let next = bits / 1000
  let unit = 0
  while (next >= 1000 && unit < units.length - 1) {
    next /= 1000
    unit++
  }
  return `${new Intl.NumberFormat("ru-RU", { maximumFractionDigits: next >= 100 ? 0 : 1 }).format(next)} ${units[unit]}`
}

export function formatRelative(value: string) {
  const minutes = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 60000))
  if (minutes < 1) return "только что"
  if (minutes < 60) return `${minutes} мин назад`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} ч назад`
  return new Date(value).toLocaleDateString("ru-RU")
}

export function formatDuration(milliseconds: number) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000))
  if (seconds < 60) return `${seconds} сек`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} мин`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} ч ${minutes % 60} мин`
  const days = Math.floor(hours / 24)
  return `${days} д ${hours % 24} ч`
}

export function isOlderVersion(current: string | undefined, target: string): boolean {
  if (!current) return false
  const parse = (value: string) => {
    const [core, ...prereleaseParts] = value.replace(/^v/i, "").split("-")
    const [major, minor, patch] = core.split(".").map((part) => Number(part) || 0)
    const segments = prereleaseParts.join("-").split(".").filter(Boolean)
    const channel = segments[0] ?? ""
    const channelNumbers = segments.slice(1).map((part) => Number(part) || 0)
    return { major, minor, patch, channel, channelNumbers }
  }
  const a = parse(current)
  const b = parse(target)
  if (a.major !== b.major) return a.major < b.major
  if (a.minor !== b.minor) return a.minor < b.minor
  if (a.patch !== b.patch) return a.patch < b.patch
  const rank: Record<string, number> = { "": 3, alpha: 1, beta: 2 }
  const rankOf = (channel: string) => rank[channel] ?? 2
  if (a.channel !== b.channel) return rankOf(a.channel) < rankOf(b.channel)
  const depth = Math.max(a.channelNumbers.length, b.channelNumbers.length)
  for (let i = 0; i < depth; i++) {
    const av = a.channelNumbers[i] ?? 0
    const bv = b.channelNumbers[i] ?? 0
    if (av !== bv) return av < bv
  }
  return false
}
