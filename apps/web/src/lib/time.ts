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
