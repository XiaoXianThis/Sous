// The workbench's visual language: one icon and one tone per state, used on
// every page (work/specs/m5-workbench design §6). Kept free of Svelte so it
// can be unit tested.
import type { Persona, PersonaColor, Run, Task, TaskStatus } from './types'

/** The states the workbench draws. */
export type Look = 'running' | 'waiting' | 'queued' | 'paused' | 'done' | 'failed' | 'planned' | 'review' | 'cancelled'

export interface LookInfo {
  icon: string // a name in Icon.svelte
  tone: 'accent' | 'warning' | 'neutral' | 'success' | 'danger'
  text: string
}

export const looks: Record<Look, LookInfo> = {
  running: { icon: 'play', tone: 'accent', text: '执行中' },
  review: { icon: 'eye', tone: 'accent', text: '验收中' },
  waiting: { icon: 'hand', tone: 'warning', text: '等你审批' },
  paused: { icon: 'pause', tone: 'warning', text: '已暂停' },
  queued: { icon: 'clock', tone: 'neutral', text: '排队' },
  planned: { icon: 'circle', tone: 'neutral', text: '待开始' },
  done: { icon: 'check', tone: 'success', text: '完成' },
  failed: { icon: 'alert', tone: 'danger', text: '失败或受阻' },
  cancelled: { icon: 'x', tone: 'neutral', text: '已取消' },
}

/** The four states a task row shows on the workbench. */
export type TaskState = 'todo' | 'active' | 'attention' | 'closed'

export interface TaskStateInfo {
  text: string
  icon: string
  tone: LookInfo['tone']
}

export const taskStates: Record<TaskState, TaskStateInfo> = {
  todo: { text: '待办', icon: 'circle', tone: 'neutral' },
  active: { text: '进行中', icon: 'play', tone: 'accent' },
  attention: { text: '要你处理', icon: 'hand', tone: 'warning' },
  closed: { text: '已结束', icon: 'check', tone: 'neutral' },
}

const stateRank: Record<TaskState, number> = { attention: 0, active: 1, todo: 2, closed: 3 }

/** Orders tasks by attention, then activity, then todo, then closed. */
export function taskStateRank(s: TaskState): number {
  return stateRank[s]
}

/** Collapses a task's status into the four states the card shows. */
export function taskState(t: Pick<Task, 'status' | 'status_reason' | 'pr_url' | 'dismissed'>): TaskState {
  switch (t.status) {
    case 'inbox':
    case 'planned':
      return 'todo'
    case 'queued':
      return t.status_reason.startsWith('等待机器') ? 'attention' : 'active'
    case 'running':
      return t.status_reason.startsWith('已暂停') ? 'attention' : 'active'
    case 'review':
      return 'active'
    case 'waiting_approval':
    case 'blocked':
    case 'failed':
      return 'attention'
    case 'done':
      return t.pr_url && !t.dismissed ? 'attention' : 'closed'
    case 'cancelled':
      return 'closed'
  }
  return 'todo'
}

/** The short detail after the state, such as 排队 or PR 待合并. */
export function taskStateDetail(t: Pick<Task, 'status' | 'status_reason' | 'pr_url' | 'dismissed'>): string {
  switch (t.status) {
    case 'inbox':
      return '收件箱'
    case 'planned':
      return '待开始'
    case 'queued':
      return t.status_reason.startsWith('等待机器') ? '等待机器' : '排队'
    case 'running':
      return t.status_reason.startsWith('已暂停') ? '已暂停' : '执行中'
    case 'review':
      return '验收中'
    case 'waiting_approval':
      return '等你审批'
    case 'blocked':
      return '受阻'
    case 'failed':
      return '失败'
    case 'done':
      return t.pr_url && !t.dismissed ? 'PR 待合并' : '完成'
    case 'cancelled':
      return '已取消'
  }
  return ''
}

/** How a task looks, given its status (and whether its run is paused). */
export function taskLook(t: Pick<Task, 'status' | 'status_reason'>): Look {
  const paused = t.status_reason?.startsWith('已暂停')
  switch (t.status as TaskStatus) {
    case 'running':
      return paused ? 'paused' : 'running'
    case 'waiting_approval':
      return 'waiting'
    case 'review':
      return 'review'
    case 'queued':
      return 'queued'
    case 'done':
      return 'done'
    case 'failed':
    case 'blocked':
      return 'failed'
    case 'cancelled':
      return 'cancelled'
  }
  return 'planned'
}

/** How a run looks. */
export function runLook(r: Pick<Run, 'status' | 'paused'>, waiting = false): Look {
  if (r.status === 'queued') return 'queued'
  if (r.status === 'running') return r.paused ? 'paused' : waiting ? 'waiting' : 'running'
  if (r.status === 'succeeded') return 'done'
  if (r.status === 'cancelled') return 'cancelled'
  return 'failed'
}

/** A task's stages, by kind, and how far it got. */
export interface Stage {
  key: string
  text: string
  state: 'done' | 'current' | 'todo' | 'failed'
}

const stageNames: Record<Task['kind'], [string, string][]> = {
  code: [['plan', '计划'], ['work', '编码'], ['review', '验收'], ['pr', 'PR'], ['deploy', '部署']],
  general: [['plan', '计划'], ['work', '执行'], ['review', '验收']],
  ops: [['work', '执行'], ['health', '健康检查']],
}

/**
 * stages turns a task into its pipeline. deployed says whether a later ops
 * task of the project finished (only known to callers with the whole
 * project), and hasDeploy whether the project deploys at all.
 */
export function stages(t: Pick<Task, 'kind' | 'status' | 'pr_url' | 'next' | 'status_reason'>, opts: { hasDeploy?: boolean; deployed?: boolean } = {}): Stage[] {
  let names = stageNames[t.kind] ?? stageNames.general
  if (t.kind === 'code' && !opts.hasDeploy) names = names.filter(([k]) => k !== 'deploy')
  const keys = names.map(([k]) => k)
  let current = 0
  let failed = false
  switch (t.status) {
    case 'inbox':
    case 'planned':
      current = 0
      break
    case 'queued':
      current = keys.indexOf(t.next === 'deliver' ? 'pr' : 'work')
      if (current < 0) current = 0
      break
    case 'running':
    case 'waiting_approval':
      current = keys.indexOf(t.next === 'deliver' || t.next === 'rebase' || t.status_reason.startsWith('正在交付') ? 'pr' : 'work')
      if (current < 0) current = keys.indexOf('work')
      break
    case 'review':
      current = keys.indexOf('review')
      break
    case 'done':
      current = keys.length
      if (t.kind === 'code' && opts.hasDeploy && !opts.deployed) current = keys.indexOf('deploy')
      break
    case 'failed':
    case 'blocked':
      failed = true
      current = keys.indexOf(t.pr_url || t.next === 'deliver' ? 'pr' : 'work')
      if (current < 0) current = 0
      break
    case 'cancelled':
      current = -1
  }
  return names.map(([key, text], i) => ({
    key,
    text,
    state: current < 0 ? 'todo' : i < current ? 'done' : i === current ? (failed ? 'failed' : t.status === 'done' ? 'todo' : 'current') : 'todo',
  }))
}

/** How many stages are done, for "3/5". */
export function progress(st: Stage[]): string {
  return `${st.filter((s) => s.state === 'done').length}/${st.length}`
}

export const personaColors: PersonaColor[] = ['purple', 'teal', 'coral', 'pink', 'blue', 'green', 'amber', 'gray']

/** The avatar of a persona, or of the agent when the task has none. */
export function avatarOf(p: Persona | undefined, agent = ''): { glyph: string; color: PersonaColor; title: string } {
  if (p) return { glyph: p.glyph || p.name.slice(0, 1), color: p.color || 'gray', title: p.name }
  const a = agent || '?'
  return { glyph: a.slice(0, 1).toUpperCase(), color: 'gray', title: agent || '未分配' }
}

export const kindText: Record<Task['kind'], string> = { code: '代码', general: '一般', ops: '运维' }
