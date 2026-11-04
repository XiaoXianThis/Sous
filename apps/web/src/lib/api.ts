// REST client for docs/api/core-hub-http.md.
import type {
  AgentInfo, Approval, ConfigFile, ConfigOption, HubEvent, Inbox, JobStarted, JobTail, Me, Memory, MemoryKind, Message,
  Node, Project, Run, Schedule, Settings, Task, Thread, Updates, Usage, Overview, Persona, RepoScan, MessageContext,
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
  pauseRun: (id: string) => request<{ ok: boolean }>('POST', `/api/v1/runs/${id}/pause`, {}),
  resumeRun: (id: string, text = '') => request<{ ok: boolean }>('POST', `/api/v1/runs/${id}/resume`, { text }),
  schedules: () => request<{ schedules: Schedule[] }>('GET', '/api/v1/schedules'),
  createSchedule: (s: Partial<Schedule>) => request<Schedule>('POST', '/api/v1/schedules', s),
  updateSchedule: (id: string, s: Partial<Schedule>) => request<Schedule>('PATCH', `/api/v1/schedules/${id}`, s),
  deleteSchedule: (id: string) => request<{ ok: boolean }>('DELETE', `/api/v1/schedules/${id}`),
  runSchedule: (id: string) => request<{ ok: boolean }>('POST', `/api/v1/schedules/${id}/run`, {}),
  usage: (days: number, tz: string) =>
    request<Usage>('GET', `/api/v1/usage?${new URLSearchParams({ days: String(days), tz })}`),
  updates: (refresh = false) => request<Updates>('GET', `/api/v1/updates${refresh ? '?refresh=1' : ''}`),
  updateNode: (id: string, version = '') => request<{ version: string }>('POST', `/api/v1/nodes/${id}/update`, { version }),
  detectAgents: (id: string) => request<{ agents: AgentInfo[] }>('POST', `/api/v1/nodes/${id}/agents/detect`),
  probeAgent: (id: string, agent: string) =>
    request<{ options: ConfigOption[] | null; at: number }>('POST', `/api/v1/nodes/${id}/agents/${agent}/probe`),
  agentJob: (id: string, agent: string, what: 'install' | 'login' | 'logout', size: { cols: number; rows: number }, upgrade = false) =>
    request<JobStarted>('POST', `/api/v1/nodes/${id}/agents/${agent}/${what}`, { ...size, upgrade }),
  setAgentDefaults: (id: string, agent: string, options: Record<string, string>) =>
    request<{ ok: boolean }>('PUT', `/api/v1/nodes/${id}/agents/${agent}/defaults`, { options }),
  setAgentEnv: (id: string, agent: string, name: string, value: string) =>
    request<{ ok: boolean }>('PUT', `/api/v1/nodes/${id}/agents/${agent}/env`, { name, value }),
  jobTail: (id: string, job: string) => request<JobTail>('GET', `/api/v1/nodes/${id}/jobs/${job}`),
  jobInput: (id: string, job: string, data: string) => request<{ ok: boolean }>('POST', `/api/v1/nodes/${id}/jobs/${job}/input`, { data }),
  jobResize: (id: string, job: string, cols: number, rows: number) =>
    request<{ ok: boolean }>('POST', `/api/v1/nodes/${id}/jobs/${job}/resize`, { cols, rows }),
  jobCancel: (id: string, job: string) => request<{ ok: boolean }>('POST', `/api/v1/nodes/${id}/jobs/${job}/cancel`),
  readFile: (id: string, agent: string, file: string) => request<ConfigFile>('GET', `/api/v1/nodes/${id}/files/${agent}/${file}`),
  writeFile: (id: string, agent: string, file: string, content: string, base: string) =>
    request<ConfigFile>('PUT', `/api/v1/nodes/${id}/files/${agent}/${file}`, { content, base_sha256: base }),
  send: (threadId: string, content: string, context?: MessageContext) =>
    request<{ message_id: string; seq: number }>('POST', `/api/v1/threads/${threadId}/messages`, { content, context }),

  // M5 workbench
  overview: () => request<Overview>('GET', '/api/v1/overview'),
  personas: () => request<{ personas: Persona[] }>('GET', '/api/v1/personas'),
  createPersona: (p: Partial<Persona>) => request<Persona>('POST', '/api/v1/personas', p),
  updatePersona: (id: string, p: Partial<Persona>) => request<Persona>('PATCH', `/api/v1/personas/${id}`, p),
  deletePersona: (id: string) => request<unknown>('DELETE', `/api/v1/personas/${id}`),
  repos: () => request<{ repos: RepoScan[] }>('GET', '/api/v1/repos'),
  setRole: (id: string, role: 'dev' | 'server') => request<{ role: string }>('PUT', `/api/v1/nodes/${id}/role`, { role }),
  setWorkspaces: (id: string, workspaces: string[]) =>
    request<{ workspaces: string[]; repos: unknown[] }>('PUT', `/api/v1/nodes/${id}/workspaces`, { workspaces }),
  scanNode: (id: string) => request<{ workspaces: string[] }>('POST', `/api/v1/nodes/${id}/scan`),
  newProject: (b: { name: string; node_id?: string; layout?: 'wokspace' | 'plain'; owner?: string; private?: boolean; thread_id?: string }) =>
    request<{ project: Project; run: Run }>('POST', '/api/v1/projects/new', b),
  cloneProject: (id: string, nodeId = '') => request<{ run: Run }>('POST', `/api/v1/projects/${id}/clone`, { node_id: nodeId }),
}
