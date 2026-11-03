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
