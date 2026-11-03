// Run state built from run.* and approval.* events.
import type { Approval, HubEvent, Run } from './types'

export interface ToolCall {
  id: string
  title: string
  status: string
  kind: string
}

export interface RunView {
  run: Run
  /** seq of the run.queued event, to place the run in its thread's timeline */
  seq: number
  tools: ToolCall[]
  plan: { content: string; status: string }[]
  /** Agent text streamed in this session (not kept across reloads; see the log). */
  output: string
  thinking: string
  statusLine: string
  approvals: Approval[]
}

export function isActive(r: Run): boolean {
  return r.status === 'queued' || r.status === 'running'
}

/** Applies a run or approval event. Returns false for events of other kinds. */
export function applyRunEvent(runs: Record<string, RunView>, ev: HubEvent): boolean {
  const p = ev.payload ?? {}
  const id = ev.run_id
  if (!id) return false
  if (ev.type === 'run.queued') {
    if (!runs[id]) {
      runs[id] = {
        run: {
          id, thread_id: ev.thread_id ?? '', node_id: p.node_id, agent: p.agent, kind: p.kind, repo_path: p.repo_path ?? '',
          prompt: p.prompt, status: 'queued', workdir: '', branch: '', summary: '', diffstat: '', error: '',
          created_at: ev.ts, started_at: 0, ended_at: 0,
        },
        seq: ev.seq, tools: [], plan: [], output: '', thinking: '', statusLine: '', approvals: [],
      }
    }
    return true
  }
  const v = runs[id]
  if (!v) return ev.type.startsWith('run.') || ev.type.startsWith('approval.')
  switch (ev.type) {
    case 'run.started':
      v.run.status = 'running'
      v.run.workdir = p.workdir ?? ''
      v.run.branch = p.branch ?? ''
      v.run.started_at = ev.ts
      v.statusLine = ''
      break
    case 'run.update':
    case 'run.output':
      applyUpdate(v, p)
      break
    case 'run.ended':
      Object.assign(v.run, {
        status: p.status, summary: p.summary ?? '', diffstat: p.diffstat ?? '', error: p.error ?? '', ended_at: ev.ts,
      })
      v.statusLine = ''
      for (const a of v.approvals) if (a.status === 'pending') a.status = 'expired'
      break
    case 'approval.requested':
      if (!v.approvals.some((a) => a.id === p.approval_id)) {
        v.approvals.push({
          id: p.approval_id, run_id: id, title: p.title, tool_kind: p.tool_kind ?? '', detail: p.detail ?? '',
          options: p.options ?? [], status: 'pending', option_id: '', created_at: ev.ts,
        })
      }
      break
    case 'approval.decided': {
      const a = v.approvals.find((a) => a.id === p.approval_id)
      if (a && a.status === 'pending') {
        a.status = 'decided'
        a.option_id = p.option_id ?? ''
      }
      break
    }
    default:
      return false
  }
  return true
}

function applyUpdate(v: RunView, p: any) {
  // A status line ("starting the agent") is stale once the agent reports anything.
  if (p.kind !== 'status' && p.kind !== 'thought') v.statusLine = ''
  switch (p.kind) {
    case 'text':
      v.output += p.text ?? ''
      break
    case 'thought':
      v.thinking += p.text ?? ''
      break
    case 'status':
      v.statusLine = p.text ?? ''
      break
    case 'plan':
      v.plan = (p.plan ?? []).map((e: any) => ({ content: e.content, status: e.status ?? '' }))
      break
    case 'tool': {
      let t = v.tools.find((t) => t.id === p.tool_call_id)
      if (!t) {
        t = { id: p.tool_call_id, title: p.title || '工具调用', status: p.status || 'pending', kind: p.tool_kind ?? '' }
        v.tools.push(t)
      } else {
        if (p.title) t.title = p.title
        if (p.status) t.status = p.status
        if (p.tool_kind) t.kind = p.tool_kind
      }
      break
    }
  }
}

/** Loads a run from GET /api/v1/runs/{id} by replaying its stored events. */
export function runFromHistory(run: Run, events: HubEvent[], approvals: Approval[]): RunView {
  const runs: Record<string, RunView> = {}
  for (const ev of events) applyRunEvent(runs, ev)
  const v = runs[run.id] ?? {
    run, seq: 0, tools: [], plan: [], output: '', thinking: '', statusLine: '', approvals: [],
  }
  v.run = run
  v.approvals = approvals
  return v
}
