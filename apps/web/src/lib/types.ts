// Shapes from docs/api/core-hub-http.md.

export interface Thread {
  id: string
  title: string
  kind: 'main' | 'chat' | 'project'
  created_at: number
  updated_at: number
  archived: boolean
}

export interface Message {
  id: string
  thread_id: string
  role: 'user' | 'assistant'
  content: string
  error?: boolean
  reply_to?: number
  /** "notice" for a proactive notification, "silent" for a turn that chose not to reply. */
  /**
   * "notice": a proactive notification; "silent": a turn that chose not to
   * reply; "report": a task's result posted by the task engine.
   */
  kind?: 'notice' | 'silent' | 'report'
  seq: number
  created_at: number
  /** Set while the reply is still streaming; never sent by the server. */
  streaming?: boolean
  /** Reasoning text streamed before the answer, if the model sends it. */
  thinking?: string
}

export interface HubEvent {
  seq: number
  id: string
  ts: number
  type: string
  actor: string
  thread_id?: string
  task_id?: string
  run_id?: string
  payload: any
}

export interface Me {
  logged_in: boolean
  has_passkey: boolean
  dev_login: boolean
}

export type MemoryKind = 'profile' | 'project' | 'lesson' | 'note'

export interface Memory {
  id: string
  kind: MemoryKind
  content: string
  created_at: number
  updated_at: number
}

export interface AgentInfo {
  name: string
  title: string
  installed: boolean
  version?: string
  transport: string
  logged_in?: boolean
  note?: string
}

export interface Node {
  id: string
  name: string
  os: string
  arch: string
  hostname: string
  version: string
  agents: AgentInfo[] | null
  online: boolean
  concurrency: number
  last_seen: number
  created_at: number
}

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted'

export type RunPurpose = 'work' | 'rebase' | 'deliver'

export interface Run {
  id: string
  thread_id: string
  task_id: string
  purpose: RunPurpose
  permission_mode: PermissionMode
  base_branch: string
  pr_url: string
  conflict: boolean
  title: string
  node_id: string
  agent: string
  kind: 'code' | 'general'
  repo_path: string
  prompt: string
  status: RunStatus
  workdir: string
  branch: string
  summary: string
  diffstat: string
  error: string
  created_at: number
  started_at: number
  ended_at: number
}

export interface ApprovalOption {
  option_id: string
  name: string
  kind: string
}

export interface Approval {
  id: string
  run_id: string
  task_id: string
  /** The node policy's class of the action: exec-other, git-remote, ... */
  category: string
  /** Who decides: the user, or the approver agent ("ai") in auto mode. */
  review: 'user' | 'ai'
  /** Why the approver agent handed it to the user. */
  escalation: string
  decided_by?: string
  note?: string
  title: string
  tool_kind: string
  detail: string
  options: ApprovalOption[]
  status: 'pending' | 'decided' | 'expired'
  option_id: string
  created_at: number
}

export type PermissionMode = 'default' | 'auto' | 'open'

export type TaskStatus =
  | 'inbox' | 'planned' | 'queued' | 'running' | 'waiting_approval'
  | 'blocked' | 'review' | 'done' | 'failed' | 'cancelled'

export interface Task {
  id: string
  parent_id: string
  origin_thread_id: string
  kind: 'code' | 'general'
  title: string
  detail: string
  status: TaskStatus
  status_reason: string
  priority: number
  project_id: string
  agent_pref: string
  node_pref: string
  permission_mode: PermissionMode | ''
  branch: string
  base_branch: string
  pr_url: string
  review_note: string
  next: string
  dismissed: boolean
  depends_on: string[]
  created_at: number
  updated_at: number
}

export interface ProjectNode {
  node_id: string
  path: string
  priority: number
}

export interface Project {
  id: string
  name: string
  repo_url: string
  default_mode: PermissionMode | ''
  default_agent: string
  nodes: ProjectNode[]
  created_at: number
  updated_at: number
}

export interface Quiet {
  enabled: boolean
  start: string
  end: string
  tz: string
}

export interface Settings {
  default_mode: PermissionMode
  default_agent: string
  notify: Record<string, boolean>
  quiet: Quiet
  roles: Record<string, string>
  push_devices: number
}

export interface Inbox {
  approvals: Approval[]
  tasks: Task[]
}
