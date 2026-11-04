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

export function isOpen(t: Task): boolean {
  return t.status !== 'done' && t.status !== 'cancelled'
}

/** The agents sous knows how to drive (core's internal/agents). */
export const knownAgents = ['claude', 'codex', 'cursor', 'opencode', 'kiro']

/** Agents to offer in a picker: the known ones, plus any a machine reports. */
export function agentChoices(nodes: Node[]): string[] {
  const reported = nodes.flatMap((n) => (n.agents ?? []).filter((a) => a.installed).map((a) => a.name))
  return [...new Set([...knownAgents, ...reported])]
}
