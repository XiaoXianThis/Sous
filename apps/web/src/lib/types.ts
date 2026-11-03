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
  kind?: 'notice' | 'silent'
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
  last_seen: number
  created_at: number
}

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted'

export interface Run {
  id: string
  thread_id: string
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
  title: string
  tool_kind: string
  detail: string
  options: ApprovalOption[]
  status: 'pending' | 'decided' | 'expired'
  option_id: string
  created_at: number
}
