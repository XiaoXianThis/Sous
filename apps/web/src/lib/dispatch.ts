// The dispatch page's model: who is holding which piece of work. People (you,
// the supervisor and its helpers, an agent per running Run), things (tasks as
// tickets on a board, approvals as requests, reports as letters) and places
// (one workshop per machine, a seat per slot). Pure: the floor comes from the
// workbench overview, a move from one Hub event. office.ts lays it out,
// dispatch-draw.ts draws it.
import { agentTitle } from './agents'
import type { Approval, HubEvent, Node, Overview, Run, Task, TaskStatus } from './types'

/** A column of the task board on the dispatch room's wall. */
export type Column = 'todo' | 'doing' | 'approval' | 'review' | 'blocked' | 'done'

export const COLUMNS: { id: Column; name: string; statuses: TaskStatus[] }[] = [
  { id: 'todo', name: '待办', statuses: ['inbox', 'planned', 'queued'] },
  { id: 'doing', name: '进行中', statuses: ['running'] },
  { id: 'approval', name: '等审批', statuses: ['waiting_approval'] },
  { id: 'review', name: '待验收', statuses: ['review'] },
  { id: 'blocked', name: '受阻', statuses: ['blocked', 'failed'] },
  { id: 'done', name: '完成', statuses: ['done'] },
]

export function columnOf(status: string): Column | '' {
  return COLUMNS.find((c) => (c.statuses as string[]).includes(status))?.id ?? ''
}

/** Finished tasks stay on the board this long. */
export const DONE_MS = 24 * 3600 * 1000

/** One machine's workshop: a seat per slot (a running Run or nobody) and the Runs waiting at the door. */
export interface Shop {
  node: Node
  seats: (Run | null)[]
  queue: Run[]
}

export interface Floor {
  shops: Shop[]
  board: Record<Column, Task[]>
  /** Requests on your desk (pending, yours to decide). */
  mine: Approval[]
  /** Requests the approver agent is looking at. */
  theirs: Approval[]
}

/** Seats per machine from the last frame, so an agent keeps its seat while others come and go. */
export type Seating = Record<string, (string | null)[]>

const working = (r: Run) => r.status === 'running' && !r.archived && !!r.node_id

/**
 * The floor now. Machines online first; each has max(concurrency, running)
 * seats, and a Run keeps the seat it had in `prev`.
 */
export function floorOf(data: Overview, now: number, prev: Seating = {}): Floor {
  const nodes = [...data.nodes].sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name))
  const shops = nodes.map((node): Shop => {
    const runs = data.runs.filter((r) => r.node_id === node.id && working(r)).sort((a, b) => a.started_at - b.started_at)
    const n = Math.max(node.concurrency || 1, runs.length)
    const seats: (Run | null)[] = Array(n).fill(null)
    const old = prev[node.id] ?? []
    const left: Run[] = []
    for (const r of runs) {
      const i = old.indexOf(r.id)
      if (i >= 0 && i < n && !seats[i]) seats[i] = r
      else left.push(r)
    }
    for (const r of left) seats[seats.indexOf(null)] = r
    const queue = data.runs.filter((r) => r.node_id === node.id && r.status === 'queued' && !r.archived).sort((a, b) => a.created_at - b.created_at)
    return { node, seats, queue }
  })
  const board = Object.fromEntries(COLUMNS.map((c) => [c.id, [] as Task[]])) as Record<Column, Task[]>
  for (const t of data.tasks) {
    if (t.dismissed) continue
    const col = columnOf(t.status)
    if (!col) continue
    if (col === 'done' && now - t.updated_at > DONE_MS) continue
    board[col].push(t)
  }
  for (const c of COLUMNS) board[c.id].sort((a, b) => b.updated_at - a.updated_at)
  const pending = data.approvals.filter((a) => a.status === 'pending')
  return { shops, board, mine: pending.filter((a) => a.review !== 'ai' || !!a.escalation), theirs: pending.filter((a) => a.review === 'ai' && !a.escalation) }
}

/** The seating to remember for the next frame. */
export function seatingOf(floor: Floor): Seating {
  return Object.fromEntries(floor.shops.map((s) => [s.node.id, s.seats.map((r) => r?.id ?? null)]))
}

/** People in the dispatch room and your office. */
export type Actor = 'you' | 'sup' | 'reviewer' | 'approver' | 'inspector' | 'clock'

export const ACTOR_NAME: Record<Actor, string> = {
  you: '你',
  sup: '主管',
  reviewer: '验收员',
  approver: '审批员',
  inspector: '巡检员',
  clock: '定时',
}

/** Where a move starts or ends. */
export type Spot =
  | { kind: 'actor'; actor: Actor }
  | { kind: 'board'; col: Column }
  | { kind: 'run'; run: string; node: string }
  | { kind: 'machine'; node: string }

/** What is carried: drawn differently, all in the accent color. */
export type Item = 'note' | 'ticket' | 'memo' | 'request' | 'stamp' | 'parcel' | 'letter' | 'bubble'

export interface Move {
  id: string
  ts: number
  item: Item
  from: Spot
  to: Spot
  /** What it is, in a few words. */
  text: string
  task_id?: string
  run_id?: string
  thread_id?: string
  /** False for a failure, a refusal or a rejected review. */
  ok?: boolean
}

const actor = (a: Actor): Spot => ({ kind: 'actor', actor: a })

function runSpot(data: Overview | null, runId: string | undefined, nodeId = ''): Spot {
  const r = runId ? data?.runs.find((x) => x.id === runId) : undefined
  const node = r?.node_id || nodeId
  if (runId && r && r.status === 'running') return { kind: 'run', run: runId, node }
  return { kind: 'machine', node }
}

const TOOL_TEXT: [RegExp, string][] = [
  [/^web_(search|fetch)/, '上网查资料'],
  [/^machine_look/, '看一眼机器'],
  [/^(git_|repo_|read_file|list_files)/, '翻代码'],
  [/^(memory_|remember|recall)/, '翻笔记'],
  [/^(schedule|cron)/, '排定时任务'],
  [/^(consult|ask_consultant)/, '请教顾问'],
]

const FINDING_TEXT: Record<string, string> = { repeat: '在打转', stalled: '卡住了', long: '太久了', tokens: '用量太大' }

const RUN_PURPOSE: Record<string, string> = { consult: '请教', review: '复查', git: 'git', deliver: '交付', rebase: '变基', clone: '取代码', create: '建仓库', chat: '对话' }

/** Shortens text to n characters with an ellipsis, on one line. */
export function brief(s: string, n: number): string {
  const one = s.replace(/\s+/g, ' ').trim()
  const chars = [...one]
  return chars.length <= n ? one : chars.slice(0, n - 1).join('') + '…'
}

/**
 * What one Hub event looks like on the floor, if anything: something carried
 * from one spot to another, or a bubble (from equal to to). `data` is the
 * overview before the event is applied, so a Run that just ended still has
 * its seat.
 */
export function moveOf(ev: HubEvent, data: Overview | null): Move | null {
  const p = ev.payload ?? {}
  const refs = { task_id: ev.task_id || undefined, run_id: ev.run_id || undefined, thread_id: ev.thread_id || undefined }
  const m = (item: Item, from: Spot, to: Spot, text: string, ok?: boolean): Move => ({ id: ev.id || `${ev.seq}-${ev.type}`, ts: ev.ts, item, from, to, text, ...refs, ...(ok === false && { ok }) })
  const task = ev.task_id ? data?.tasks.find((t) => t.id === ev.task_id) : undefined
  switch (ev.type) {
    case 'message.user':
      if (p.kind === 'trigger') return null
      return m('note', actor('you'), actor('sup'), brief(p.content || '留言', 14))
    case 'message.assistant': {
      if (p.kind === 'step' || p.kind === 'silent') return null
      if (p.kind === 'report') return m('letter', actor('sup'), actor('you'), task ? `汇报：${brief(task.title, 10)}` : '汇报')
      if (p.kind === 'inspector') return m('letter', actor('inspector'), actor('you'), '巡检报告')
      if (p.kind === 'notice') return m('letter', actor('sup'), actor('you'), '通知')
      if (p.error) return m('letter', actor('sup'), actor('you'), '回复出错', false)
      return m('letter', actor('sup'), actor('you'), brief(p.content || '回复', 14))
    }
    case 'message.tools': {
      const tools: { name: string }[] = p.tools ?? []
      const last = tools[tools.length - 1]
      if (!last) return null
      const text = TOOL_TEXT.find(([re]) => re.test(last.name))?.[1]
      return text ? m('bubble', actor('sup'), actor('sup'), text) : null
    }
    case 'thread.compacted':
      return m('bubble', actor('sup'), actor('sup'), '整理笔记')
    case 'trigger.fired':
      return m('note', actor('clock'), actor('sup'), p.schedule_id ? '定时任务到点' : '该跟进了')
    case 'task.created':
      return m('ticket', actor('sup'), { kind: 'board', col: columnOf(p.status) || 'todo' }, brief(p.title || '新任务', 12))
    case 'task.status': {
      const a = columnOf(p.from)
      const b = columnOf(p.to)
      if (!a || !b || a === b) return null
      return m('ticket', { kind: 'board', col: a }, { kind: 'board', col: b }, task ? brief(task.title, 10) : COLUMNS.find((c) => c.id === b)!.name, b !== 'blocked')
    }
    case 'run.queued': {
      const who = agentTitle(p.agent || '')
      const what = RUN_PURPOSE[p.purpose] ?? ''
      return m('ticket', actor('sup'), { kind: 'machine', node: p.node_id || '' }, `${what ? what + ' · ' : ''}派给 ${who}`)
    }
    case 'run.message':
      return m('memo', actor('sup'), runSpot(data, ev.run_id), `追加：${brief(p.text || '', 10)}`)
    case 'run.turn':
      return m('memo', runSpot(data, ev.run_id), actor('sup'), '顾问回答')
    case 'run.ended': {
      const s = p.status as string
      const text = s === 'succeeded' ? (p.pr_url ? '交付 · PR' : '交付') : s === 'failed' ? '失败' : s === 'cancelled' ? '取消' : '中断'
      return m('parcel', runSpot(data, ev.run_id), actor('sup'), text, s === 'succeeded')
    }
    case 'approval.requested':
      return m('request', runSpot(data, ev.run_id), actor(p.review === 'ai' ? 'approver' : 'you'), `请示：${brief(p.title || '', 10)}`)
    case 'approval.escalated':
      return m('request', actor('approver'), actor('you'), '转给你批')
    case 'approval.decided': {
      const a = data?.approvals.find((x) => x.id === p.approval_id)
      const kind = a?.options.find((o) => o.option_id === p.option_id)?.kind ?? ''
      const text = !p.option_id ? '撤回' : kind.startsWith('reject') ? '驳回' : kind.startsWith('allow') ? '批准' : '已批'
      return m('stamp', actor(p.decided_by === 'approver' ? 'approver' : 'you'), runSpot(data, ev.run_id || a?.run_id), text, !kind.startsWith('reject') && !!p.option_id)
    }
    case 'task.reviewed':
      return m('stamp', actor('reviewer'), { kind: 'board', col: p.pass ? 'done' : 'blocked' }, p.pass ? '验收通过' : '退回', !!p.pass)
    case 'inspector.finding':
      return m('memo', actor('inspector'), actor('sup'), `巡检：${FINDING_TEXT[p.rule] ?? '有发现'}`, false)
    case 'node.online':
    case 'node.offline': {
      const node = p.node_id || ev.actor
      return m('bubble', { kind: 'machine', node }, { kind: 'machine', node }, ev.type === 'node.online' ? '上线' : '下线')
    }
    default:
      return null
  }
}

/** A spot's name for the feed. */
export function spotName(s: Spot, data: Overview | null): string {
  switch (s.kind) {
    case 'actor':
      return ACTOR_NAME[s.actor]
    case 'board':
      return `看板·${COLUMNS.find((c) => c.id === s.col)!.name}`
    case 'run': {
      const r = data?.runs.find((x) => x.id === s.run)
      return r ? agentTitle(r.agent) : 'agent'
    }
    case 'machine':
      return data?.nodes.find((n) => n.id === s.node)?.name || '机器'
  }
}

/** What a focus key ("task:<id>", "run:<id>", "thread:<id>") covers. */
export function focusOf(key: string, data: Overview | null): { tasks: Set<string>; runs: Set<string>; threads: Set<string> } | null {
  if (!key) return null
  const [kind, id] = key.split(':')
  const tasks = new Set<string>()
  const runs = new Set<string>()
  const threads = new Set<string>()
  const allRuns = data?.runs ?? []
  if (kind === 'task') tasks.add(id)
  if (kind === 'run') {
    runs.add(id)
    const t = allRuns.find((r) => r.id === id)?.task_id
    if (t) tasks.add(t)
  }
  if (kind === 'thread') {
    threads.add(id)
    for (const t of data?.tasks ?? []) if (t.origin_thread_id === id) tasks.add(t.id)
  }
  for (const r of allRuns) if ((r.task_id && tasks.has(r.task_id)) || (kind === 'thread' && r.thread_id === id)) runs.add(r.id)
  return { tasks, runs, threads }
}

/** Whether something with these refs is part of the focus (everything is, without one). */
export function inFocus(f: ReturnType<typeof focusOf>, refs: { task_id?: string; run_id?: string; thread_id?: string }): boolean {
  if (!f) return true
  return (!!refs.task_id && f.tasks.has(refs.task_id)) || (!!refs.run_id && f.runs.has(refs.run_id)) || (!!refs.thread_id && f.threads.has(refs.thread_id))
}

/** Moves the feed keeps. */
export const FEED_MAX = 80
