// REST client for docs/api/core-hub-http.md.
import type {
  Approval, HubEvent, Inbox, Me, Memory, MemoryKind, Message, Node, Project, Run, Settings, Task, Thread,
} from './types'

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

function csrf(): string {
  const m = document.cookie.match(/(?:^|;\s*)sous_csrf=([^;]+)/)
  return m ? decodeURIComponent(m[1]) : ''
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') headers['X-CSRF-Token'] = csrf()
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, data?.error?.code ?? 'http', data?.error?.message ?? `HTTP ${res.status}`)
  }
  return data as T
}

export const api = {
  me: () => request<Me>('GET', '/api/v1/me'),
  devLogin: () => request<{ ok: boolean }>('POST', '/api/v1/auth/dev-login'),
  logout: () => request<{ ok: boolean }>('POST', '/api/v1/auth/logout'),
  threads: () => request<{ threads: Thread[] }>('GET', '/api/v1/threads'),
  createThread: (title: string) => request<Thread>('POST', '/api/v1/threads', { title }),
  messages: (threadId: string, before = 0) =>
    request<{ messages: Message[]; last_seq: number }>(
      'GET',
      `/api/v1/threads/${threadId}/messages?limit=50${before ? `&before=${before}` : ''}`,
    ),
  memories: (q = '', kind = '') =>
    request<{ memories: Memory[]; kinds: MemoryKind[] }>(
      'GET',
      `/api/v1/memories?${new URLSearchParams({ ...(q && { q }), ...(kind && { kind }) })}`,
    ),
  createMemory: (kind: MemoryKind, content: string) => request<Memory>('POST', '/api/v1/memories', { kind, content }),
  updateMemory: (id: string, kind: MemoryKind, content: string) =>
    request<Memory>('PUT', `/api/v1/memories/${id}`, { kind, content }),
  deleteMemory: (id: string) => request<{ ok: boolean }>('DELETE', `/api/v1/memories/${id}`),
  nodes: () => request<{ nodes: Node[] }>('GET', '/api/v1/nodes'),
  pairingCode: () => request<{ code: string; expires_at: number }>('POST', '/api/v1/nodes/pairing-code'),
  renameNode: (id: string, name: string) => request<Node>('PATCH', `/api/v1/nodes/${id}`, { name }),
  removeNode: (id: string) => request<{ ok: boolean }>('DELETE', `/api/v1/nodes/${id}`),
  runs: (threadId: string) => request<{ runs: Run[] }>('GET', `/api/v1/runs?thread_id=${encodeURIComponent(threadId)}`),
  run: (id: string) => request<{ run: Run; events: HubEvent[]; approvals: Approval[] }>('GET', `/api/v1/runs/${id}`),
  runText: async (id: string, what: 'log' | 'diff') => {
    const res = await fetch(`/api/v1/runs/${id}/${what}`, { credentials: 'same-origin' })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    return res.text()
  },
  cancelRun: (id: string) => request<{ ok: boolean }>('POST', `/api/v1/runs/${id}/cancel`),
  messageRun: (id: string, text: string) => request<{ ok: boolean }>('POST', `/api/v1/runs/${id}/message`, { text }),
  decide: (id: string, optionId: string, note = '') =>
    request<{ ok: boolean }>('POST', `/api/v1/approvals/${id}/decide`, { option_id: optionId, note }),
  approvals: (status = 'pending') => request<{ approvals: Approval[] }>('GET', `/api/v1/approvals?status=${status}`),
  inbox: () => request<Inbox>('GET', '/api/v1/inbox'),
  tasks: (status = '') => request<{ tasks: Task[] }>('GET', `/api/v1/tasks${status ? `?status=${status}` : ''}`),
  task: (id: string) => request<{ task: Task; runs: Run[]; subtasks: Task[] }>('GET', `/api/v1/tasks/${id}`),
  createTask: (t: Partial<Task> & { dispatch?: boolean; thread_id?: string }) => request<Task>('POST', '/api/v1/tasks', t),
  patchTask: (id: string, t: Partial<Task> & { status?: string; reason?: string }) =>
    request<Task>('PATCH', `/api/v1/tasks/${id}`, t),
  dispatchTask: (id: string, nodeId = '', agent = '') =>
    request<Task>('POST', `/api/v1/tasks/${id}/dispatch`, { node_id: nodeId, agent }),
  cancelTask: (id: string) => request<Task>('POST', `/api/v1/tasks/${id}/cancel`, {}),
  projects: () => request<{ projects: Project[] }>('GET', '/api/v1/projects'),
  createProject: (p: Partial<Project>) => request<Project>('POST', '/api/v1/projects', p),
  updateProject: (id: string, p: Partial<Project>) => request<Project>('PATCH', `/api/v1/projects/${id}`, p),
  deleteProject: (id: string) => request<{ ok: boolean }>('DELETE', `/api/v1/projects/${id}`),
  settings: () => request<Settings>('GET', '/api/v1/settings'),
  patchSettings: (s: Partial<Settings>) => request<Settings>('PATCH', '/api/v1/settings', s),
  vapidKey: () => request<{ key: string }>('GET', '/api/v1/push/vapid-public-key'),
  subscribePush: (sub: PushSubscriptionJSON) => request<{ ok: boolean }>('POST', '/api/v1/push/subscriptions', sub),
  unsubscribePush: (endpoint: string) => request<{ ok: boolean }>('DELETE', '/api/v1/push/subscriptions', { endpoint }),
  testPush: () => request<{ devices: number; sent: number; errors: string[] }>('POST', '/api/v1/push/test'),
  send: (threadId: string, content: string) =>
    request<{ message_id: string; seq: number }>('POST', `/api/v1/threads/${threadId}/messages`, { content }),
}
