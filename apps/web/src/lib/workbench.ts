// What the overview draws, computed from GET /overview. Pure, so it can be
// unit tested.
import { mostUrgent, runLook, taskLook, type Look } from './status'
import type { Node, Overview, Persona, Project, Run, Task } from './types'

export const MISC = 'misc'

export interface Worker {
  run: Run
  task?: Task
  persona?: Persona
  look: Look
}

export interface Cell {
  /** The project has a place (a checkout) on this machine. */
  place: boolean
  scan: boolean
  /** The machine is the project's deploy target. */
  deploy: boolean
  workers: Worker[]
}

export interface Row {
  project: Project
  cells: Record<string, Cell>
  /** Open tasks without a machine yet (queued, planned), and counts by look. */
  waiting: Task[]
  counts: Partial<Record<Look, number>>
  look?: Look
}

/** Machines in column order: online dev machines, offline ones, then servers. */
export function columns(nodes: Node[]): Node[] {
  const rank = (n: Node) => (n.role === 'server' ? 2 : 0) + (n.online ? 0 : 1)
  return [...nodes].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
}

/** The persona of a task, else the one a run names. */
export function personaFor(o: Pick<Overview, 'personas'>, task?: Task, run?: Run): Persona | undefined {
  if (task?.persona_id) {
    const p = o.personas.find((x) => x.id === task.persona_id)
    if (p) return p
  }
  if (run?.persona) return o.personas.find((x) => x.name === run.persona)
  return undefined
}

/** One row per project: who works where, and what waits. Misc only shows when it has work. */
export function rows(o: Overview): Row[] {
  const tasks = new Map(o.tasks.map((t) => [t.id, t]))
  const waitingRuns = new Set(o.approvals.filter((a) => a.review === 'user').map((a) => a.run_id))
  const out: Row[] = []
  for (const p of o.projects) {
    const cells: Record<string, Cell> = {}
    for (const n of o.nodes) {
      const place = p.nodes.find((x) => x.node_id === n.id)
      cells[n.id] = { place: !!place, scan: place?.source === 'scan', deploy: p.deploy?.node_id === n.id, workers: [] }
    }
    for (const r of o.runs) {
      const t = r.task_id ? tasks.get(r.task_id) : undefined
      const projectOf = t?.project_id || (r.purpose === 'clone' || r.purpose === 'create' ? r.prompt : '')
      if (projectOf !== p.id || !cells[r.node_id]) continue
      cells[r.node_id].workers.push({ run: r, task: t, persona: personaFor(o, t, r), look: runLook(r, waitingRuns.has(r.id)) })
    }
    const mine = o.tasks.filter((t) => (t.project_id || MISC) === p.id)
    const busy = new Set(o.runs.map((r) => r.task_id))
    const waiting = mine.filter((t) => !busy.has(t.id) && (t.status === 'queued' || t.status === 'planned' || t.status === 'inbox'))
    const counts: Partial<Record<Look, number>> = {}
    for (const t of mine) {
      const l = taskLook(t)
      counts[l] = (counts[l] ?? 0) + 1
    }
    const looks = Object.keys(counts) as Look[]
    const active = Object.values(cells).some((c) => c.workers.length)
    if (p.id === MISC && !active && mine.length === 0) continue
    out.push({ project: p, cells, waiting, counts, look: mostUrgent(looks) })
  }
  // Projects with something going on first, misc last.
  const score = (r: Row) => (r.project.id === MISC ? 2 : 0) + (r.look && r.look !== 'done' && r.look !== 'cancelled' ? 0 : 1)
  return out.sort((a, b) => score(a) - score(b) || a.project.name.localeCompare(b.project.name))
}

/** Busy slots of a machine: running or queued runs over its concurrency. */
export function load(o: Overview, nodeId: string): { busy: number; slots: number } {
  const n = o.nodes.find((x) => x.id === nodeId)
  return { busy: o.runs.filter((r) => r.node_id === nodeId).length, slots: n?.concurrency || 2 }
}

/** Whether a project's latest ops task finished after the given time. */
export function deployedAfter(tasks: Task[], projectId: string, ts: number): boolean {
  return tasks.some((t) => t.project_id === projectId && t.kind === 'ops' && t.status === 'done' && t.updated_at >= ts)
}
