// The system page as a live sequence diagram: who asks whom for what, row by
// row, from the Hub's flow records (work/specs/system-map). Pure functions; the
// page only draws what these return.
import type { SysGraph, SysRecord, SysState } from './types'

/** A column of the diagram: one participant, made of one or more modules. */
export interface Lane {
  id: string
  name: string
  /** Modules shown in this lane. */
  modules: string[]
  /** Only in the detailed view (plumbing such as the event log). */
  detail?: boolean
}

export const LANES: Lane[] = [
  { id: 'you', name: '你', modules: ['ui', 'cli'] },
  { id: 'api', name: '接口', modules: ['httpapi'] },
  { id: 'log', name: '事件日志', modules: ['eventlog', 'effects'], detail: true },
  { id: 'sup', name: '主管', modules: ['supervisor'] },
  { id: 'model', name: '模型', modules: ['models', 'provider:'] },
  { id: 'tools', name: '外部工具', modules: ['web', 'search', 'mcp', 'mcpserver:'] },
  { id: 'task', name: '任务引擎', modules: ['task'] },
  { id: 'hub', name: '机器连接', modules: ['nodehub'] },
  { id: 'machine', name: '机器', modules: ['machine:'] },
  { id: 'auto', name: '定时与巡检', modules: ['trigger', 'inspector'] },
  { id: 'push', name: '推送', modules: ['notify', 'push'] },
]

/** The lane a module belongs to ('' for none). Entries ending in ':' are prefixes. */
export function laneOf(module: string): string {
  for (const l of LANES) {
    if (l.modules.some((m) => (m.endsWith(':') ? module.startsWith(m) : module === m))) return l.id
  }
  return ''
}

const EVENT_TEXT: Record<string, string> = {
  'message.user': '用户消息',
  'message.assistant': '回复',
  'message.assistant.delta': '流式输出',
  'message.tools': '记下工具调用',
  'message.seen': '已看到',
  'message.moved': '并入话题',
  'thread.created': '新对话',
  'thread.compacted': '整理上下文',
  'topic.updated': '更新话题',
  'trigger.fired': '触发',
  'usage.recorded': '记录用量',
  'memory.put': '写记忆',
  'memory.deleted': '删记忆',
  'task.created': '建任务',
  'task.updated': '改任务',
  'task.status': '任务状态',
  'task.reviewed': '验收结论',
  'task.git': 'git 结果',
  'project.put': '改项目',
  'project.archived': '归档项目',
  'project.deployed': '记下部署',
  'persona.put': '改预设',
  'run.queued': 'Run 排队',
  'run.started': 'Run 开始',
  'run.update': 'Run 进展',
  'run.output': 'agent 输出',
  'run.ended': 'Run 结束',
  'run.turn': '顾问回答',
  'run.message': '追加指令',
  'run.message.delivered': '指令送达',
  'run.paused': '暂停 Run',
  'run.resumed': '继续 Run',
  'run.archived': '归档 Run',
  'consult.closed': '结束顾问会话',
  'approval.requested': '请求审批',
  'approval.decided': '审批结果',
  'approval.checked': '审批检查',
  'approval.escalated': '交给你审批',
  'node.online': '机器上线',
  'node.offline': '机器下线',
  'node.inventory': '上报 agent',
  'node.repos': '上报仓库',
  'node.paired': '配对机器',
  'node.role': '改机器角色',
  'agent.job.started': '开始作业',
  'agent.job.output': '作业输出',
  'agent.job.ended': '作业结束',
  'schedule.put': '改定时任务',
  'schedule.deleted': '删定时任务',
  'inspector.finding': '巡检发现',
  'notify.sent': '发通知',
  'models.changed': '改模型配置',
  'note.put': '记笔记',
  'note.deleted': '删笔记',
  'note.sent': '笔记发出',
}

const FRAME_TEXT: Record<string, string> = {
  hello: '连接',
  welcome: '欢迎',
  ack: '确认',
  inventory: '上报 agent',
  'run.start': '开始 Run',
  'run.message': '追加指令',
  'run.cancel': '取消 Run',
  'run.pause': '暂停 Run',
  'run.resume': '继续 Run',
  'run.started': '已开始',
  'run.update': '进展',
  'run.output': '输出',
  'run.diff': '改动',
  'run.ended': '结束',
  'run.turn': '回答',
  'run.delivered': '已送达',
  'run.ask': '提问',
  'approval.request': '请求审批',
  'approval.decide': '审批结果',
  'node.request': '请求',
  'node.response': '回应',
  'node.stream': '输出流',
  'node.job_ended': '作业结束',
  'node.repos': '上报仓库',
  'node.update': '更新',
  'node.update_result': '更新结果',
  'workspace.remove': '清理目录',
}

const EFFECT_TEXT: Record<string, string> = {
  'task.advance': '推进任务',
  approve: '审批',
  'project.setup': '准备项目',
  push: '推送',
}

const HTTP_TEXT: [RegExp, string][] = [
  [/^POST \/api\/v1\/threads\/[^/]+\/messages$/, '发消息'],
  [/^POST \/api\/v1\/threads\/[^/]+\/dm$/, '私聊'],
  [/^POST \/api\/v1\/threads$/, '新对话'],
  [/^POST \/api\/v1\/tasks$/, '建任务'],
  [/^POST \/api\/v1\/tasks\/[^/]+\/dispatch$/, '派发任务'],
  [/^POST \/api\/v1\/tasks\/[^/]+\/cancel$/, '取消任务'],
  [/^POST \/api\/v1\/tasks\/[^/]+\/verdict$/, '验收'],
  [/^POST \/api\/v1\/runs\/[^/]+\/message$/, '追加指令'],
  [/^POST \/api\/v1\/approvals\/[^/]+\/decide$/, '审批'],
  [/^POST \/api\/v1\/notes$/, '记笔记'],
  [/^PATCH \/api\/v1\/notes\/[^/]+$/, '改笔记'],
  [/^DELETE \/api\/v1\/notes\/[^/]+$/, '删笔记'],
  [/^PATCH \/api\/v1\/topics\/[^/]+$/, '改话题'],
  [/^POST \/api\/v1\/attachments/, '上传附件'],
  [/^PUT \/api\/v1\/models\/roles$/, '改模型'],
  [/^POST \/api\/v1\/auth\//, '登录'],
]

/** A record's words: what went along the line, in Chinese. */
export function recordText(r: SysRecord): string {
  const [from, to] = r.edge.split('>')
  const s = r.summary
  if (s.startsWith('call ')) return EVENT_TEXT[s.slice(5)] ?? s.slice(5)
  if (from === 'ui' || from === 'cli') {
    for (const [re, text] of HTTP_TEXT) if (re.test(s)) return text
    const m = s.match(/^(\w+) \/api\/v1\/(.*)$/)
    return m ? `${m[1]} ${m[2].replace(/[0-9A-Z]{20,}/g, '…')}` : s
  }
  if (to === 'models' || from === 'models') {
    const [role, model] = s.split(' · ')
    const who: Record<string, string> = { supervisor: '主管', compactor: '整理', reviewer: '验收', approver: '审批', inspector: '巡检' }
    return model ? `${who[role] ?? role}调用 ${model}` : s
  }
  if (from === 'effects') return EFFECT_TEXT[s] ?? s
  if (from.startsWith('machine:') || to.startsWith('machine:')) return FRAME_TEXT[s] ?? s
  if (from === 'task' && s === 'message.assistant') return '汇报'
  if (to === 'eventlog' || from === 'eventlog') return EVENT_TEXT[s] ?? s
  if (from === 'supervisor') return `工具 ${s}`
  return EVENT_TEXT[s] ?? s
}

/** Events that are bookkeeping, not coordination: left out of the plain view. */
const QUIET = new Set(['message.seen', 'usage.recorded', 'message.assistant.delta', 'message.tools', 'agent.job.output', 'approval.checked'])

/** The machine connection writes down what a machine reported; the frame's arrow already says it. */
const ECHOES = new Set(['run.started', 'run.update', 'run.output', 'run.ended', 'run.turn', 'run.message.delivered', 'node.inventory', 'node.repos'])

/** Noise in the plain view: page polling, pushes to the page, frame acks, bookkeeping. */
function plumbing(r: SysRecord): boolean {
  const [from, to] = r.edge.split('>')
  if (from === 'httpapi' && (to === 'ui' || to === 'cli')) return true
  if ((from === 'ui' || from === 'cli') && r.summary.startsWith('GET ')) return true
  if (r.summary === 'ack') return true
  if (r.edge === 'eventlog>effects') return true
  if (to === 'eventlog' && QUIET.has(r.summary)) return true
  // Within one lane (the model router and its provider, the API and a page…).
  if (from !== 'eventlog' && to !== 'eventlog' && laneOf(from) === laneOf(to) && !r.summary.startsWith('call ')) return true
  return false
}

export interface Step {
  key: string
  ts: number
  /** Last record folded into this step. */
  until: number
  from: string // lane id
  to: string // lane id; equal to from for a note on one lane
  text: string
  count: number
  activity: string // "task:<id>", "thread:<id>", "run:<id>" or ""
  records: SysRecord[]
}

/** The activity a record belongs to: its task, else its run, else its conversation. */
export function activityOf(r: SysRecord, taskOfRun: (runId: string) => string = () => ''): string {
  if (r.refs.task_id) return `task:${r.refs.task_id}`
  if (r.refs.run_id) {
    const t = taskOfRun(r.refs.run_id)
    return t ? `task:${t}` : `run:${r.refs.run_id}`
  }
  if (r.refs.thread_id) return `thread:${r.refs.thread_id}`
  return ''
}

const PAIR_MS = 20 // a write and the reads it caused share one transaction
const FOLD_MS = 3000 // the same thing again within this gap folds into one row
const FOLD_BACK = 4 // how many recent rows a repeat may fold into

/**
 * Turns flow records into the diagram's rows. In the plain view a write to the
 * event log and the reads it caused become one arrow from writer to reader, a
 * write nobody listens to becomes a note on the writer's lane, and plumbing is
 * left out; the detailed view shows every record as it is.
 */
export function steps(records: SysRecord[], opts: { detail: boolean; taskOfRun?: (runId: string) => string }): Step[] {
  const out: Step[] = []
  const used = new Set<number>()
  const sorted = [...records].sort((a, b) => a.ts - b.ts)
  const push = (r: SysRecord, from: string, to: string, text: string, extra: SysRecord[] = []) => {
    if (!from || !to) return
    const activity = activityOf(r, opts.taskOfRun)
    for (let k = out.length - 1; k >= Math.max(0, out.length - FOLD_BACK); k--) {
      const last = out[k]
      if (last.from === from && last.to === to && last.text === text && last.activity === activity && r.ts - last.until <= FOLD_MS) {
        last.count++
        last.until = r.ts
        last.records.push(r, ...extra)
        return
      }
    }
    out.push({ key: `${r.ts}-${r.edge}-${out.length}`, ts: r.ts, until: r.ts, from, to, text, count: 1, activity, records: [r, ...extra] })
  }
  sorted.forEach((r, i) => {
    if (used.has(i)) return
    const [fromMod, toMod] = r.edge.split('>')
    if (opts.detail) {
      push(r, laneOf(fromMod), laneOf(toMod), recordText(r))
      return
    }
    if (plumbing(r)) return
    if (toMod === 'eventlog') {
      // Pair with the reads of the same event in the same write.
      const reads: { r: SysRecord; i: number }[] = []
      for (let j = i + 1; j < sorted.length && sorted[j].ts - r.ts <= PAIR_MS; j++) {
        const o = sorted[j]
        if (!used.has(j) && o.edge.startsWith('eventlog>') && o.summary === r.summary && sameRefs(o, r)) reads.push({ r: o, i: j })
      }
      const from = laneOf(fromMod)
      // A caller's arrow already says it ("call <type>" into this writer).
      const called = sorted.some((o) => Math.abs(o.ts - r.ts) <= PAIR_MS && o.summary === `call ${r.summary}` && o.edge.endsWith(`>${fromMod}`))
      if (reads.length === 0) {
        if (fromMod === 'nodehub' && ECHOES.has(r.summary)) return
        if (!called) push(r, from, from, recordText(r))
        return
      }
      for (const x of reads) {
        used.add(x.i)
        const to = laneOf(x.r.edge.split('>')[1])
        if (to === from && called) continue // the writer reading its own event: the call says it
        push(r, from, to, recordText(r), [x.r])
      }
      return
    }
    if (fromMod === 'eventlog') {
      // A read whose write was not seen (the writer is not a known module).
      const to = laneOf(toMod)
      push(r, to, to, `收到${recordText(r)}`)
      return
    }
    if (fromMod === 'effects') {
      const to = laneOf(toMod)
      push(r, to, to, recordText(r))
      return
    }
    push(r, laneOf(fromMod), laneOf(toMod), recordText(r))
  })
  return out
}

function sameRefs(a: SysRecord, b: SysRecord): boolean {
  return (a.refs.thread_id ?? '') === (b.refs.thread_id ?? '') && (a.refs.task_id ?? '') === (b.refs.task_id ?? '') && (a.refs.run_id ?? '') === (b.refs.run_id ?? '')
}

/** The lanes there are: the plain ones always, the detailed ones only in detail view. */
export function visibleLanes(detail: boolean): Lane[] {
  return LANES.filter((l) => detail || !l.detail)
}

const CORE = ['you', 'api', 'sup', 'model', 'task']

/**
 * The lanes worth a column: the core ones, the machines when there are any,
 * and any other lane that has rows or is not idle.
 */
export function usedLanes(detail: boolean, list: Step[], graph: SysGraph): Lane[] {
  const busy = new Set<string>()
  for (const s of list) {
    busy.add(s.from)
    busy.add(s.to)
  }
  const hasMachines = graph.modules.some((m) => m.id.startsWith('machine:'))
  return visibleLanes(detail).filter((l) => {
    if (CORE.includes(l.id) || busy.has(l.id)) return true
    if ((l.id === 'hub' || l.id === 'machine') && hasMachines) return true
    const state = laneState(l, graph)
    return state === 'busy' || state === 'degraded'
  })
}

/** The worst state of a lane's modules, for its header. */
export function laneState(lane: Lane, graph: SysGraph): SysState {
  const order: SysState[] = ['down', 'degraded', 'busy', 'ok', 'idle']
  let best: SysState = 'idle'
  let found = false
  for (const m of graph.modules) {
    if (laneOf(m.id) !== lane.id) continue
    const s = graph.status[m.id]?.state ?? 'idle'
    if (!found || order.indexOf(s) < order.indexOf(best)) best = s
    found = true
  }
  // A lane of machines is down only when every machine is.
  if (lane.id === 'machine' && best === 'down') {
    const states = graph.modules.filter((m) => laneOf(m.id) === 'machine').map((m) => graph.status[m.id]?.state ?? 'idle')
    if (states.some((s) => s !== 'down')) return states.includes('busy') ? 'busy' : 'ok'
  }
  return best
}

/** The one-line summary of a lane: its main module's summary. */
export function laneSummary(lane: Lane, graph: SysGraph): string {
  const mods = graph.modules.filter((m) => laneOf(m.id) === lane.id)
  if (lane.id === 'machine') {
    if (mods.length === 0) return '没有机器'
    const online = mods.filter((m) => graph.status[m.id]?.state !== 'down').length
    return `${online}/${mods.length} 台在线`
  }
  const main = mods.find((m) => !m.id.includes(':')) ?? mods[0]
  return (main && graph.status[main.id]?.summary) || ''
}

/** Which task each run belongs to, as far as the records tell (frames carry both). */
export function runTasks(records: SysRecord[]): Map<string, string> {
  const m = new Map<string, string>()
  for (const r of records) if (r.refs.run_id && r.refs.task_id) m.set(r.refs.run_id, r.refs.task_id)
  return m
}

/** Activities seen in the steps, newest first. */
export function activities(list: Step[]): string[] {
  const seen = new Map<string, number>()
  for (const s of list) if (s.activity) seen.set(s.activity, Math.max(seen.get(s.activity) ?? 0, s.until))
  return [...seen.entries()].sort((a, b) => b[1] - a[1]).map(([k]) => k)
}

/** Horizontal position (percent of the lanes area) of a lane's center. */
export function laneX(lanes: Lane[], id: string): number {
  const i = lanes.findIndex((l) => l.id === id)
  return i < 0 ? -1 : ((i + 0.5) / lanes.length) * 100
}
