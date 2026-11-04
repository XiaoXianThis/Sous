// The live activity of running Runs, folded from the event stream. Pure, so
// the card can show the last two lines and the detail sheet the whole feed.
import type { HubEvent } from './types'

export type ActivityKind = 'text' | 'thought' | 'tool' | 'status' | 'permission'

export interface Activity {
  id: string
  kind: ActivityKind
  text: string
  /** A tool call's state: pending, in_progress, completed or failed. */
  status?: string
}

/** Activities by Run id. */
export type Live = Record<string, Activity[]>

/** Keep the feed short: the last 24 activities, each at most 2000 chars. */
const MAX_ACTIVITY = 24
const MAX_TEXT = 2000

let counter = 0
const nextID = (prefix: string) => `${prefix}-${++counter}`

/** Applies one run event. Returns false for events of other kinds. */
export function applyLive(live: Live, ev: HubEvent): boolean {
  const runId = ev.run_id
  if (!runId) return false
  const p = ev.payload ?? {}

  if (ev.type === 'run.output') {
    const text = typeof p.text === 'string' ? p.text : ''
    if (!text) return false
    append(liveOf(live, runId), p.kind === 'thought' ? 'thought' : 'text', text)
    return true
  }
  if (ev.type !== 'run.update') return false

  switch (p.kind) {
    case 'tool': {
      const list = liveOf(live, runId)
      const key = p.tool_call_id || nextID('tool')
      let a = list.find((x) => x.id === key)
      if (!a) {
        a = { id: key, kind: 'tool', text: p.title || '工具调用', status: p.status || 'pending' }
        list.push(a)
      } else {
        if (p.title) a.text = p.title
        if (p.status) a.status = p.status
      }
      trim(list)
      return true
    }
    case 'permission': {
      const list = liveOf(live, runId)
      list.push({
        id: nextID('permission'),
        kind: 'permission',
        text: `${p.decision === 'deny' ? '已拒绝' : '自动放行'}：${p.title ?? ''}`,
      })
      trim(list)
      return true
    }
    case 'status': {
      const list = liveOf(live, runId)
      const last = list[list.length - 1]
      if (last?.kind === 'status') last.text = p.text ?? ''
      else list.push({ id: nextID('status'), kind: 'status', text: p.text ?? '' })
      trim(list)
      return true
    }
    default:
      // Plans and anything else are drawn from the stored run, not here.
      return false
  }
}

/** The last n activities, oldest first. */
export function tail(acts: Activity[], n = 2): Activity[] {
  return acts.slice(Math.max(0, acts.length - n))
}

/** One readable line: whitespace collapsed, the newest part kept. */
export function oneLine(a: Activity, max = 90): string {
  const s = a.text.replace(/\s+/g, ' ').trim()
  return s.length > max ? `…${s.slice(-max)}` : s
}

/**
 * Reads the end of a run's log (runs/<id>.log) into activities. The log has
 * agent text and "[status] kind title" tool lines; thinking is not stored.
 */
export function parseLogTail(text: string, maxChars = 4096): Activity[] {
  const s = text.length > maxChars ? text.slice(-maxChars) : text
  const out: Activity[] = []
  for (const raw of s.split('\n')) {
    const line = raw.trim()
    if (!line) continue
    const m = line.match(/^\[([^\]]+)\]\s+(\S+)\s+(.+)$/)
    if (m) out.push({ id: `log-${out.length}`, kind: 'tool', text: m[3], status: m[1] })
    else out.push({ id: `log-${out.length}`, kind: 'text', text: line })
  }
  return out.slice(-MAX_ACTIVITY)
}

/** Seeds a Run's feed from its log once, when the page opens or reloads. */
export function seedLive(live: Live, runId: string, text: string): boolean {
  if (!text || (live[runId]?.length ?? 0) > 0) return false
  const acts = parseLogTail(text)
  if (!acts.length) return false
  live[runId] = acts
  return true
}

function liveOf(live: Live, runId: string): Activity[] {
  const list = live[runId]
  if (list) return list
  const created: Activity[] = []
  live[runId] = created
  return created
}

function append(list: Activity[], kind: 'text' | 'thought', text: string) {
  const last = list[list.length - 1]
  if (last && last.kind === kind) last.text = keepTail(last.text + text)
  else list.push({ id: nextID(kind), kind, text: keepTail(text) })
  trim(list)
}

function keepTail(s: string): string {
  return s.length > MAX_TEXT ? s.slice(-MAX_TEXT) : s
}

function trim(list: Activity[]) {
  while (list.length > MAX_ACTIVITY) list.shift()
  for (const a of list) a.text = keepTail(a.text)
}
