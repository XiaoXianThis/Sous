// Who a project chat can talk to directly (decision 0028), kept free of the
// UI so it can be unit tested.
import type { DmTarget, Run, Task } from './types'

/**
 * The targets of a project chat's picker: running work/chat runs, tasks that
 * are not running but whose agent may still have a session, and the project
 * consultant. The consultant is always last.
 */
export function dmTargets(project: { id: string }, tasks: Task[], runs: Run[]): DmTarget[] {
  const mine = tasks.filter((t) => t.project_id === project.id)
  const byID = new Map(mine.map((t) => [t.id, t]))
  const out: DmTarget[] = []
  const seen = new Set<string>()

  const active = runs
    .filter((r) => r.task_id && byID.has(r.task_id) && (r.purpose === 'work' || r.purpose === 'chat') && r.status === 'running')
    .sort((a, b) => b.created_at - a.created_at)
  for (const r of active) {
    if (seen.has(r.task_id)) continue
    seen.add(r.task_id)
    const t = byID.get(r.task_id)!
    out.push({ key: `run:${r.id}`, kind: 'run', label: `${r.agent} · ${t.title}`, agent: r.agent, id: r.id })
  }

  const idle = mine
    .filter((t) => !seen.has(t.id) && t.status !== 'queued' && t.status !== 'waiting_approval')
    .sort((a, b) => b.updated_at - a.updated_at)
    .slice(0, 10)
  for (const t of idle) {
    out.push({ key: `task:${t.id}`, kind: 'task', label: `${t.last_agent || 'agent'} · ${t.title}`, agent: t.last_agent, id: t.id })
  }

  out.push({ key: 'consult', kind: 'consult', label: '项目顾问', agent: '顾问' })
  return out
}

/** The API's `to` object for a target. */
export function dmTo(target: DmTarget): { run_id?: string; task_id?: string; consult?: boolean } {
  if (target.kind === 'run') return { run_id: target.id }
  if (target.kind === 'task') return { task_id: target.id }
  return { consult: true }
}
