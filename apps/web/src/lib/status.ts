// The workbench's visual language: one icon and one tone per state, used on
// every page (work/specs/m5-workbench design §6). Kept free of the UI so it
// can be unit tested.
import type { GitTaskState, Persona, PersonaColor, Run, Task, TaskStatus } from './types'

/** The states the workbench draws. */
export type Look = 'ended' | 'interrupted' | 'running' | 'waiting' | 'queued' | 'paused' | 'done' | 'failed' | 'planned' | 'review' | 'cancelled'

export interface LookInfo {
  icon: string // a name in Icon.tsx
  tone: 'accent' | 'warning' | 'neutral' | 'success' | 'danger'
  text: string
}

export const looks: Record<Look, LookInfo> = {
  ended: { icon: 'check', tone: 'success', text: '本轮正常结束' },
  interrupted: { icon: 'pause', tone: 'neutral', text: '连接中断，工作可能已完成' },
  running: { icon: 'play', tone: 'accent', text: '执行中' },
  review: { icon: 'eye', tone: 'accent', text: '验收中' },
  waiting: { icon: 'hand', tone: 'warning', text: '等你审批' },
  paused: { icon: 'pause', tone: 'warning', text: '已暂停' },
  queued: { icon: 'clock', tone: 'neutral', text: '排队中' },
  planned: { icon: 'circle-dashed', tone: 'neutral', text: '待开始' },
  done: { icon: 'check', tone: 'success', text: '已完成' },
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
  todo: { text: '待办', icon: 'circle-dashed', tone: 'neutral' },
  active: { text: '进行中', icon: 'play', tone: 'accent' },
  attention: { text: '要你处理', icon: 'hand', tone: 'warning' },
  closed: { text: '已结束', icon: 'check', tone: 'neutral' },
}

const stateRank: Record<TaskState, number> = { attention: 0, active: 1, todo: 2, closed: 3 }

/** Orders tasks by attention, then activity, then todo, then closed. */
export function taskStateRank(s: TaskState): number {
  return stateRank[s]
}

/** Whether a task in review waits for the user (not a model or an agent). */
export function userReviewing(t: Pick<Task, 'status' | 'status_reason'>): boolean {
  return t.status === 'review' && t.status_reason.includes('等你验收')
}

/**
 * Collapses a task's status into the four states the card shows. A done task
 * is closed whatever became of its branch: merging or pushing is the user's
 * call, made in the conversation (decision 0022).
 */
export function taskState(t: Pick<Task, 'status' | 'status_reason'>): TaskState {
  switch (t.status) {
    case 'inbox':
    case 'planned':
      return 'todo'
    case 'queued':
      return t.status_reason.startsWith('等待机器') ? 'attention' : 'active'
    case 'running':
      return t.status_reason.startsWith('已暂停') ? 'attention' : 'active'
    case 'review':
      return userReviewing(t) ? 'attention' : 'active'
    case 'waiting_approval':
    case 'blocked':
    case 'failed':
      return 'attention'
    case 'done':
    case 'cancelled':
      return 'closed'
  }
  return 'todo'
}

/** What became of a task's branch (or in-place changes). */
export const gitStateText: Record<GitTaskState, string> = {
  unmerged: '未合并',
  merged: '已合并',
  pr: 'PR 已开',
  discarded: '已丢弃',
  restored: '已撤销',
}

/** The short detail after the state, such as 排队 or 未合并. */
export function taskStateDetail(t: Pick<Task, 'status' | 'status_reason'> & Partial<Pick<Task, 'git_state'>>): string {
  switch (t.status) {
    case 'inbox':
      return '待整理'
    case 'planned':
      return '待开始'
    case 'queued':
      return t.status_reason.startsWith('等待机器') ? '等待机器' : '排队中'
    case 'running':
      return t.status_reason.startsWith('已暂停') ? '已暂停' : '执行中'
    case 'review':
      return userReviewing(t) ? '等你验收' : '验收中'
    case 'waiting_approval':
      return '等你审批'
    case 'blocked':
      return '受阻'
    case 'failed':
      return '失败'
    case 'done':
      return (t.git_state && gitStateText[t.git_state]) || '已完成'
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

/** Execution labels say nothing about whether the task passed acceptance. */
export const runStatusText: Record<Run['status'], string> = {
  queued: '排队中', running: '执行中', succeeded: '本轮正常结束', failed: '失败',
  cancelled: '已取消', interrupted: '连接中断，工作可能已完成',
}

/** How a run looks. */
export function runLook(r: Pick<Run, 'status' | 'paused'>, waiting = false): Look {
  if (r.status === 'queued') return 'queued'
  if (r.status === 'running') return r.paused ? 'paused' : waiting ? 'waiting' : 'running'
  if (r.status === 'succeeded') return 'ended'
  if (r.status === 'interrupted') return 'interrupted'
  if (r.status === 'cancelled') return 'cancelled'
  return 'failed'
}

/** A task's stages, by kind, and how far it got. */
export interface Stage {
  key: string
  text: string
  state: 'done' | 'current' | 'todo' | 'failed'
}

// A code task ends on its branch (decision 0022): merging and deploying are
// the project's business, shown on the project card, not stages of the task
// (end-to-end review 4.5).
const stageNames: Record<Task['kind'], [string, string][]> = {
  code: [['plan', '计划'], ['work', '编码'], ['review', '验收']],
  general: [['plan', '计划'], ['work', '执行'], ['review', '验收']],
  ops: [['work', '执行'], ['health', '健康检查']],
}

/**
 * stages turns a task into its stages. There is no delivery stage: what
 * happens to the branch is a separate step the user asks for (decision
 * 0022), and a task without a review has no review stage. An ops task has a
 * health check stage only when it deploys a commit (the Hub runs the
 * project's health check before the deploy counts).
 */
export function stages(
  t: Pick<Task, 'kind' | 'status' | 'next' | 'status_reason'> & Partial<Pick<Task, 'review' | 'deploy_commit'>>,
): Stage[] {
  let names = stageNames[t.kind] ?? stageNames.general
  if (t.kind === 'ops' && !t.deploy_commit) names = names.filter(([k]) => k !== 'health')
  if (t.review === 'none' || t.review === '' || t.kind === 'ops') names = names.filter(([k]) => k !== 'review')
  const keys = names.map(([k]) => k)
  const at = (k: string) => {
    const i = keys.indexOf(k)
    return i < 0 ? Math.max(keys.indexOf('work'), 0) : i
  }
  let current = 0
  let failed = false
  switch (t.status) {
    case 'inbox':
    case 'planned':
      current = 0
      break
    case 'queued':
    case 'running':
    case 'waiting_approval':
      current = at(t.next === 'review' ? 'review' : t.next === 'health' ? 'health' : 'work')
      break
    case 'review':
      current = at('review')
      break
    case 'done':
      current = keys.length
      break
    case 'failed':
    case 'blocked':
      failed = true
      current = at(t.status_reason.startsWith('验收') ? 'review' : t.status_reason.startsWith('部署后的健康检查') ? 'health' : 'work')
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

/** Where a task works and how it is reviewed, in words. */
export const placeText: Record<string, string> = { worktree: '独立分支（worktree）', in_place: '就地修改（有快照）', scratch: '临时目录' }
export const reviewText: Record<string, string> = { none: '不验收', model: '验收模型看 diff', agent: '验收 agent 跑测试', user: '你来验收' }

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
