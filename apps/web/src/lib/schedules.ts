// Display helpers for schedules and token usage, free of Svelte for tests.
import type { UsageDay, UsageRow } from './types'

/** Common schedules, offered as presets. */
export const cronPresets: { label: string; cron: string }[] = [
  { label: '每天 9:00', cron: '0 9 * * *' },
  { label: '工作日 9:00', cron: '0 9 * * 1-5' },
  { label: '每天 18:00', cron: '0 18 * * *' },
  { label: '每周一 9:00', cron: '0 9 * * 1' },
  { label: '每小时', cron: '0 * * * *' },
]

const weekday = ['日', '一', '二', '三', '四', '五', '六', '日']

/** Describes a five-field cron expression in Chinese, for the common shapes. */
export function describeCron(cron: string): string {
  const f = cron.trim().split(/\s+/)
  if (cron.trim() === '@hourly') return '每小时'
  if (cron.trim() === '@daily') return '每天 0:00'
  if (f.length !== 5) return cron
  const [min, hour, dom, mon, dow] = f
  const num = (s: string) => /^\d+$/.test(s)
  if (num(min) && hour === '*' && dom === '*' && mon === '*' && dow === '*') return `每小时第 ${min} 分`
  if (min.startsWith('*/') && hour === '*' && dom === '*' && mon === '*' && dow === '*') return `每 ${min.slice(2)} 分钟`
  if (!num(min) || !num(hour) || mon !== '*') return cron
  const at = `${hour}:${min.padStart(2, '0')}`
  if (dom === '*' && dow === '*') return `每天 ${at}`
  if (dom === '*' && dow === '1-5') return `工作日 ${at}`
  if (dom === '*' && /^[0-7](,[0-7])*$/.test(dow)) return `每周${dow.split(',').map((d) => weekday[+d]).join('、')} ${at}`
  if (num(dom) && dow === '*') return `每月 ${dom} 日 ${at}`
  return cron
}

/** Formats a token count: 1234 -> 1,234; 2_500_000 -> 250 万. */
export function tokens(n: number): string {
  if (n >= 100_000_000) return `${(n / 100_000_000).toFixed(1)} 亿`
  if (n >= 100_000) return `${Math.round(n / 10_000)} 万`
  return n.toLocaleString('en-US')
}

export interface DayTotal {
  day: string
  input: number
  output: number
  cached: number
  calls: number
}

/** Sums the daily rows over roles, and fills days without calls. */
export function dailyTotals(rows: UsageDay[], since: number, days: number, tz: string): DayTotal[] {
  const fmt = new Intl.DateTimeFormat('en-CA', { timeZone: tz || undefined, year: 'numeric', month: '2-digit', day: '2-digit' })
  const out: DayTotal[] = []
  const idx: Record<string, number> = {}
  for (let i = 0; i < days; i++) {
    const day = fmt.format(new Date(since + i * 86_400_000 + 12 * 3_600_000))
    if (idx[day] !== undefined) continue
    idx[day] = out.length
    out.push({ day, input: 0, output: 0, cached: 0, calls: 0 })
  }
  for (const r of rows) {
    let i = idx[r.day]
    if (i === undefined) {
      i = idx[r.day] = out.length
      out.push({ day: r.day, input: 0, output: 0, cached: 0, calls: 0 })
    }
    out[i].input += r.input_tokens
    out[i].output += r.output_tokens
    out[i].cached += r.cached_tokens
    out[i].calls += r.calls
  }
  return out.sort((a, b) => a.day.localeCompare(b.day))
}

/** Sums usage rows by role. */
export function byRole(rows: UsageRow[]): { role: string; calls: number; input: number; output: number; cached: number }[] {
  const m: Record<string, { role: string; calls: number; input: number; output: number; cached: number }> = {}
  for (const r of rows) {
    const x = (m[r.role] ??= { role: r.role, calls: 0, input: 0, output: 0, cached: 0 })
    x.calls += r.calls
    x.input += r.input_tokens
    x.output += r.output_tokens
    x.cached += r.cached_tokens
  }
  return Object.values(m).sort((a, b) => b.input + b.output - (a.input + a.output))
}

export const roleText: Record<string, string> = {
  supervisor: '主管', reviewer: '验收', approver: '审批', inspector: '巡检',
}
