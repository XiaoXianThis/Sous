// Relative times in Chinese: 刚刚, 3 分钟前, 4 小时前, 2 天前, 3 个月前, 1 年前.

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/** How long ago ts (unix ms) was, as of now; '' for no time. */
export function ago(ts: number, now = Date.now()): string {
  if (!ts) return ''
  const d = Math.max(0, now - ts)
  if (d < MINUTE) return '刚刚'
  if (d < HOUR) return `${Math.floor(d / MINUTE)} 分钟前`
  if (d < DAY) return `${Math.floor(d / HOUR)} 小时前`
  const days = Math.floor(d / DAY)
  if (days < 30) return `${days} 天前`
  if (days < 365) return `${Math.floor(days / 30)} 个月前`
  return `${Math.floor(days / 365)} 年前`
}

/** The full local date and time, for a tooltip. */
export function fullTime(ts: number): string {
  return ts ? new Date(ts).toLocaleString('zh-CN', { hour12: false }) : ''
}

/** How long something ran (ms), short: 12 秒, 3 分 5 秒, 1 小时 2 分; '' for nothing. */
export function duration(ms: number): string {
  if (!(ms > 0)) return ''
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s} 秒`
  const m = Math.floor(s / 60)
  if (m < 60) return s % 60 ? `${m} 分 ${s % 60} 秒` : `${m} 分`
  const h = Math.floor(m / 60)
  return m % 60 ? `${h} 小时 ${m % 60} 分` : `${h} 小时`
}
