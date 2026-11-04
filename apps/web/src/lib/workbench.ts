// What the workbench draws, computed from GET /overview and GET /inbox.
// Pure, so it can be unit tested: project cards, their locations, the
// sub-agents working there, and the task rows.
import {
  runLook, stages, taskState, taskStateDetail, taskStateRank,
  type Look, type Stage, type TaskState,
} from './status'
import type { AgentInfo, Node, Overview, Persona, Project, Run, Task } from './types'

const MISC = 'misc'

/** How many agent cards and task rows a project card shows. */
const AGENT_LIMIT = 2
const TASK_LIMIT = 4

/** One chip in a project's location row. */
export interface Location {
  kind: 'node' | 'deploy' | 'remote'
  id: string
  name: string
  icon: string
  online?: boolean
  title?: string
  href?: string
}

/** A running Run, drawn as a sub-agent card. */
export interface CardAgent {
  run: Run
  task?: Task
  persona?: Persona
  look: Look
  /** The task title, or the setup run's title. */
  title: string
}

/** A task row in a project card. */
export interface CardTask {
  task: Task
  state: TaskState
  detail: string
  summary: string
  stages: Stage[]
}

export interface ProjectCard {
  project: Project
  locations: Location[]
  /** The agents the card shows (capped). */
  agents: CardAgent[]
  /** The tasks the card shows (capped, without the ones in agents). */
  tasks: CardTask[]
  counts: Record<TaskState, number>
  /** The most urgent state in the project; closed means idle. */
  rank: TaskState
  /** Everything behind the caps, for the project sheet. */
  allAgents: CardAgent[]
  allTasks: CardTask[]
  hiddenAgents: number
  hiddenTasks: number
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

/** One line under a task title: who, with which agent, on which machine. */
export function taskSummary(o: Overview, t: Task, run?: Run): string {
  const p = personaFor(o, t, run)
  const who = p?.name || ''
  const agent = run?.agent || p?.agent || t.agent_pref || ''
  const nodeId = run?.node_id || t.node_pref
  const node = nodeId ? o.nodes.find((n) => n.id === nodeId)?.name ?? '' : ''
  return [who, agent, node].filter(Boolean).join(' · ') || '未分配'
}

/** The agent, model and thinking effort a run shows. */
export interface RunMeta {
  agentTitle: string
  model: string
  effort: string
}

export function runMeta(run: Run, node?: Node): RunMeta {
  const info = node?.agents?.find((a) => a.name === run.agent)
  const opts = run.options ?? {}
  return {
    agentTitle: info?.title || run.agent || 'agent',
    model: optionName(info, 'model', opts.model),
    effort: optionName(info, 'thought_level', opts.thought_level || opts.effort || opts.reasoning_effort),
  }
}

function optionName(info: AgentInfo | undefined, id: string, value?: string): string {
  const opt = info?.options?.find((o) => o.id === id)
  if (!value) value = info?.defaults?.[id] || opt?.current || ''
  if (!opt) return value || ''
  if (!value) return '默认'
  return opt.values.find((v) => v.value === value)?.name || value
}

/** One card per project: where it lives, who works there, and what waits. */
export function projectCards(o: Overview, inboxTasks: Task[] = []): ProjectCard[] {
  // Keep the tasks the overview has; add the inbox ones it no longer lists
  // (a done task with a PR stays in the inbox until dismissed).
  const tasks = new Map<string, Task>()
  for (const t of [...o.tasks, ...inboxTasks]) if (!tasks.has(t.id)) tasks.set(t.id, t)
  const nodes = new Map(o.nodes.map((n) => [n.id, n]))
  const projects = new Map(o.projects.map((p) => [p.id, p]))
  const waitingRuns = new Set(o.approvals.filter((a) => a.review === 'user').map((a) => a.run_id))

  // Group the running runs by project.
  const agentsByProject = new Map<string, CardAgent[]>()
  for (const r of o.runs) {
    if (r.status !== 'running') continue
    const t = r.task_id ? tasks.get(r.task_id) : undefined
    const pid = projectOfRun(r, tasks, o.projects)
    const agent: CardAgent = {
      run: r,
      task: t,
      persona: personaFor(o, t, r),
      look: runLook(r, waitingRuns.has(r.id)),
      title: t?.title || r.title || setupTitle(r),
    }
    const list = agentsByProject.get(pid)
    if (list) list.push(agent)
    else agentsByProject.set(pid, [agent])
  }

  const out: ProjectCard[] = []
  for (const p of o.projects) {
    const mine = [...tasks.values()].filter((t) => (t.project_id || MISC) === p.id)
    const all = mine.map((t) => cardTask(o, t, mine, projects.get(p.id) ?? p))
    const allAgents = [...(agentsByProject.get(p.id) ?? [])].sort((a, b) => a.run.created_at - b.run.created_at)
    const activeTaskIds = new Set(allAgents.map((a) => a.task?.id).filter(Boolean))
    const allTasks = all
      .filter((c) => !activeTaskIds.has(c.task.id))
      .sort((a, b) => taskStateRank(a.state) - taskStateRank(b.state) || b.task.updated_at - a.task.updated_at)

    const counts: Record<TaskState, number> = { todo: 0, active: 0, attention: 0, closed: 0 }
    for (const c of all) counts[c.state]++
    let rank: TaskState = 'closed'
    for (const c of all) if (taskStateRank(c.state) < taskStateRank(rank)) rank = c.state

    const agents = allAgents.slice(0, AGENT_LIMIT)
    const listed = allTasks.slice(0, TASK_LIMIT)
    out.push({
      project: p,
      locations: locationsOf(p, nodes),
      agents,
      tasks: listed,
      counts,
      rank,
      allAgents,
      allTasks,
      hiddenAgents: allAgents.length - agents.length,
      hiddenTasks: allTasks.length - listed.length,
    })
  }

  // Attention first, then active, todo, idle; misc always last.
  const score = (c: ProjectCard) => (c.project.id === MISC ? 10 : 0) + taskStateRank(c.rank)
  return out.sort((a, b) => score(a) - score(b) || a.project.name.localeCompare(b.project.name))
}

/** Whether a project's latest ops task finished after the given time. */
export function deployedAfter(tasks: Task[], projectId: string, ts: number): boolean {
  return tasks.some((t) => t.project_id === projectId && t.kind === 'ops' && t.status === 'done' && t.updated_at >= ts)
}

function cardTask(o: Overview, t: Task, mine: Task[], p: Project): CardTask {
  return {
    task: t,
    state: taskState(t),
    detail: taskStateDetail(t),
    summary: taskSummary(o, t),
    stages: stages(t, {
      hasDeploy: !!p.deploy,
      deployed: t.status === 'done' && deployedAfter(mine, p.id, t.updated_at),
    }),
  }
}

function projectOfRun(r: Run, tasks: Map<string, Task>, projects: Project[]): string {
  const t = r.task_id ? tasks.get(r.task_id) : undefined
  if (t) return t.project_id || MISC
  if (r.purpose === 'create' || r.purpose === 'clone') return projects.some((p) => p.id === r.prompt) ? r.prompt : MISC
  return MISC
}

function setupTitle(r: Run): string {
  if (r.purpose === 'clone') return '把项目 clone 到这台机器'
  if (r.purpose === 'create') return '新建项目'
  if (r.purpose === 'deliver') return '交付'
  if (r.purpose === 'rebase') return 'rebase'
  return '准备中'
}

function locationsOf(p: Project, nodes: Map<string, Node>): Location[] {
  const out: Location[] = []
  for (const pn of p.nodes) {
    const n = nodes.get(pn.node_id)
    out.push({
      kind: 'node',
      id: pn.node_id,
      name: n?.name ?? '未知机器',
      icon: n?.role === 'server' ? 'server' : 'laptop',
      online: n?.online ?? false,
      title: [n?.name ?? pn.node_id, n ? osText(n.os) : '', pn.path].filter(Boolean).join(' · '),
    })
  }
  if (p.deploy) {
    const n = nodes.get(p.deploy.node_id)
    out.push({
      kind: 'deploy',
      id: p.deploy.node_id,
      name: n?.name ?? '服务器',
      icon: 'rocket',
      online: n?.online ?? false,
      title: [n?.name ?? '服务器', p.deploy.path, p.deploy.command].filter(Boolean).join(' · '),
    })
  }
  const host = remoteHost(p)
  if (host) {
    out.push({
      kind: 'remote',
      id: host,
      name: remoteName(host),
      icon: 'link',
      title: p.repo_url || p.remote_key || host,
      href: remoteURL(p.repo_url),
    })
  }
  return out
}

function osText(os: string): string {
  if (os === 'darwin') return 'Mac'
  if (os === 'windows') return 'PC'
  if (os === 'linux') return 'Linux'
  return os
}

function remoteHost(p: Project): string {
  const key = p.remote_key || ''
  if (key) return key.split('/')[0]
  const u = p.repo_url || ''
  const m = u.match(/^git@([^:]+):/) || u.match(/^https?:\/\/([^/]+)/)
  return m ? m[1] : ''
}

function remoteName(host: string): string {
  if (host === 'github.com') return 'GitHub'
  if (host === 'gitlab.com') return 'GitLab'
  return host
}

function remoteURL(repo: string): string {
  if (!repo) return ''
  if (/^https?:\/\//.test(repo)) return repo
  const m = repo.match(/^git@([^:]+):(.+?)(?:\.git)?$/)
  return m ? `https://${m[1]}/${m[2].replace(/\.git$/, '')}` : ''
}
