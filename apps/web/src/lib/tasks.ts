// Display helpers for tasks, kept free of the UI so they can be unit tested.
import type { Node, Task, TaskStatus } from './types'

export const statusText: Record<TaskStatus, string> = {
  inbox: '待整理', planned: '待开始', queued: '排队中', running: '执行中', waiting_approval: '等你审批',
  blocked: '受阻', review: '验收中', done: '已完成', failed: '失败', cancelled: '已取消',
}

export const modeText: Record<string, string> = { default: '默认', auto: '自动审批', open: '完全开放' }

/** ACP tool kinds, for an approval that is not a command. */
export const toolKindText: Record<string, string> = {
  read: '读取', edit: '修改文件', delete: '删除', move: '移动文件', search: '搜索', execute: '执行命令', think: '思考', fetch: '联网读取', other: '其他操作',
}

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
  const reported = nodes.flatMap((n) => (n.agents ?? []).filter((a) => a.installed && !a.disabled).map((a) => a.name))
  return [...new Set([...knownAgents, ...reported])]
}

/** Why an agent cannot run a task on the chosen machine (or on any online
 *  machine when none is chosen); '' when it can (end-to-end review 4.10). */
/** The machines that can run an agent now: installed, enabled, logged in
 *  as far as anyone knows (decision 0024). */
export function machinesFor(agent: string, nodes: Node[]): Node[] {
  return nodes.filter((n) => (n.agents ?? []).some((a) => a.name === agent && a.installed && !a.disabled && a.logged_in !== false))
}

export function agentUnavailable(agent: string, nodes: Node[], nodeId: string): string {
  if (!agent) return ''
  const has = (n: Node) => (n.agents ?? []).some((a) => a.name === agent && a.installed && !a.disabled)
  if (nodeId) {
    const n = nodes.find((x) => x.id === nodeId)
    if (!n) return ''
    if ((n.agents ?? []).some((a) => a.name === agent && a.disabled)) return `${n.name} 上停用了这个 agent`
    return has(n) ? '' : `${n.name} 上没有装这个 agent`
  }
  if (nodes.some((n) => n.online && has(n))) return ''
  if (nodes.some(has)) return '装了它的机器现在都不在线'
  return '还没有机器装了这个 agent'
}

/** How a machine reads in the task form: its role and whether it is online. */
export function nodeLabel(n: Node, place: string): { text: string; note: string } {
  const notes: string[] = []
  if (n.role === 'server') notes.push(place === 'in_place' ? '服务器' : '服务器，一般只做运维')
  if (!n.online) notes.push('离线，上线后开始')
  return { text: notes.length ? `${n.name}（${notes.join('，')}）` : n.name, note: notes.join('，') }
}

/** Approval options by kind, in the page's words (end-to-end review 4.6,
 *  4.29). The agent's own name stays in the tooltip. */
export const optionText: Record<string, { text: string; tip: string }> = {
  allow_once: { text: '允许这一次', tip: '只放行这一次操作' },
  allow_always: {
    text: '以后都允许',
    tip: '由 agent 自己记住：通常在这次执行里同类操作不再问；有的 agent（如 Claude Code）会写进项目的权限设置，以后也不再问。sous 的底线规则和硬信号不受影响。',
  },
  reject_once: { text: '拒绝', tip: '拒绝这一次；同一个任务里再请求同样的操作时，sous 会照这次的决定直接拒绝' },
  reject_always: { text: '一直拒绝', tip: '由 agent 记住，同类操作不再请求；同一个任务里再请求时 sous 也会直接拒绝' },
}

/** An approval's title in the page's words, for the titles agents commonly
 *  use; others stay as they are. */
export function approvalTitle(title: string): string {
  const t = title.trim()
  const rules: [RegExp, string][] = [
    [/^Allow web search\??$/i, '允许联网搜索？'],
    [/^Allow web fetch\??$/i, '允许读取网页？'],
    [/^Write (.+)$/i, '写入 $1'],
    [/^Edit (.+)$/i, '修改 $1'],
    [/^Create (.+)$/i, '新建 $1'],
    [/^Delete (.+)$/i, '删除 $1'],
    [/^Read (.+)$/i, '读取 $1'],
    [/^(Run|Execute|Bash)[: ] *(.+)$/i, '执行 $2'],
    [/^Fetch (.+)$/i, '读取网页 $1'],
    [/^Search (.+)$/i, '搜索 $1'],
  ]
  for (const [re, to] of rules) if (re.test(t)) return t.replace(re, to)
  return t
}

/** Why an agent stopped, in plain words (end-to-end review 4.14). */
export const errorKindText: Record<string, string> = {
  agent_quota: '额度用完了（套餐或用量上限），什么都没做。换一个 agent，或者等额度恢复后重试。',
  agent_rate_limit: '被限流了，什么都没做。稍后重试，或者换一个 agent。',
  agent_not_logged_in: '在这台机器上没有登录。到“设置 → 机器”里登录后重试。',
  agent_unavailable: '服务暂时不可用。稍后重试，或者换一个 agent。',
}
