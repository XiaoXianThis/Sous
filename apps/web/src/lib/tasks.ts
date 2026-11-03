// Display helpers for tasks, kept free of Svelte so they can be unit tested.
import type { Node, Task, TaskStatus } from './types'

export const statusText: Record<TaskStatus, string> = {
  inbox: '待整理', planned: '已计划', queued: '排队中', running: '进行中', waiting_approval: '等待审批',
  blocked: '受阻', review: '验收中', done: '已完成', failed: '失败', cancelled: '已取消',
}

export const modeText: Record<string, string> = { default: '默认', auto: '自动审批', open: '完全开放' }

export const categoryText: Record<string, string> = {
  read: '读取', 'edit-worktree': '修改工作区', 'exec-safe': '构建测试', 'exec-other': '执行命令', install: '安装依赖',
  'outside-worktree': '工作区之外', delete: '删除', 'git-remote': 'push / PR', irreversible: '不可逆', floor: '底线规则',
}

export const pushKindText: Record<string, string> = {
  approval: '需要审批', task_done: '任务完成', task_failed: '任务失败', task_blocked: '任务受阻或排队',
  report: '主管的主动提醒', inspector: '巡检发现的问题',
}

/** Board columns, left to right, and the statuses each one shows. */
export const columns: { key: string; title: string; statuses: TaskStatus[] }[] = [
  { key: 'todo', title: '待办', statuses: ['inbox', 'planned', 'queued'] },
  { key: 'doing', title: '进行中', statuses: ['running', 'waiting_approval', 'review'] },
  { key: 'attention', title: '要你处理', statuses: ['blocked', 'failed'] },
  { key: 'closed', title: '已结束', statuses: ['done', 'cancelled'] },
]

export function isOpen(t: Task): boolean {
  return t.status !== 'done' && t.status !== 'cancelled'
}

/** Groups tasks into board columns, by priority then age within a column. */
export function board(tasks: Task[]): Record<string, Task[]> {
  const out: Record<string, Task[]> = {}
  for (const c of columns) out[c.key] = []
  for (const t of tasks) {
    const c = columns.find((c) => c.statuses.includes(t.status))
    if (c) out[c.key].push(t)
  }
  for (const k in out) {
    out[k].sort((a, b) => (k === 'closed' ? b.updated_at - a.updated_at : b.priority - a.priority || a.created_at - b.created_at))
  }
  return out
}

/** The tone of a status, for color: running, attention, done or quiet. */
export function tone(s: TaskStatus): 'active' | 'attention' | 'done' | 'quiet' {
  if (s === 'running' || s === 'review' || s === 'waiting_approval') return 'active'
  if (s === 'blocked' || s === 'failed') return 'attention'
  if (s === 'done') return 'done'
  return 'quiet'
}

/** Parses the PWA's hash routes: #approvals, #task=<id>, #thread=<id>. */
export function parseHash(hash: string): { approvals?: boolean; task?: string; thread?: string } {
  const h = hash.replace(/^#\/?/, '')
  if (h === 'approvals') return { approvals: true }
  const [k, v] = h.split('=')
  if (k === 'task' && v) return { task: decodeURIComponent(v) }
  if (k === 'thread' && v) return { thread: decodeURIComponent(v) }
  return {}
}

/** The agents sous knows how to drive (core's internal/agents). */
export const knownAgents = ['claude', 'codex', 'cursor', 'opencode', 'kiro']

/** Agents to offer in a picker: the known ones, plus any a machine reports. */
export function agentChoices(nodes: Node[]): string[] {
  const reported = nodes.flatMap((n) => (n.agents ?? []).filter((a) => a.installed).map((a) => a.name))
  return [...new Set([...knownAgents, ...reported])]
}
