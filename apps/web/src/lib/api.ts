// REST client for docs/api/core-hub-http.md.
import type {
  SysGraph, SysHistory, SysTrace, AgentInfo, Attachment, Note, Approval, ConfigFile, ConfigOption, HubEvent, Inbox, JobStarted, JobTail, Me, Memory, MemoryKind, Message,
  Node, Project, Run, Schedule, Shell, RepoResult, ModelsView, McpView, Settings, ThreadContext, Task, Thread, Updates, Usage, Overview, Persona, RepoScan, MessageContext, Topic, Talk,
  MeetingDetail, MeetingHost, MinutesRow,
} from './types'

export interface McpInput {
  name?: string
  transport?: 'stdio' | 'http'
  command?: string
  args?: string[]
  url?: string
  /** Values to set; a name already saved with an empty value keeps its value. */
  env?: Record<string, string>
  remove_env?: string[]
  headers?: Record<string, string>
  remove_headers?: string[]
  enabled?: boolean
  disabled_tools?: string[]
}

export interface ProviderInput {
  name?: string
  kind?: string
  base_url?: string
  api_key_env?: string
  /** A new key; leave out to keep the stored one. */
  api_key?: string
  clear_key?: boolean
  models?: string[]
}

import { fromUtf8, utf8 } from '../trust/bytes'
import { nodeAddr } from '../trust/docs'
import { rpc, rpcJSON } from '../trust/rpc'
import { ask, intentHeaders, permit } from '../trust/vault'

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

// Every request goes to the brain through the relay, end-to-end encrypted
// (decision 0030): there is no cookie and no HTTP API on any server.
export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res = await rpcJSON<{ error?: { code?: string; message?: string } }>(method, path, body)
  if (res.status === 401 && res.json?.error?.code === 'reauth') {
    // A critical request to the brain itself: the user approves exactly this
    // request with Face ID, and the brain checks that approval.
    const raw = body === undefined ? new Uint8Array() : utf8(JSON.stringify(body))
    const headers = await intentHeaders(method, path, raw)
    res = await rpcJSON(method, path, body, headers)
  }
  if (res.status >= 300) {
    const e = res.json?.error
    throw new ApiError(res.status, e?.code ?? 'http', e?.message ?? `HTTP ${res.status}`)
  }
  return res.json as T
}

async function upload(file: Blob, name: string): Promise<Attachment> {
  const bytes = new Uint8Array(await file.arrayBuffer())
  const res = await rpc('POST', `/api/v1/attachments?name=${encodeURIComponent(name)}`, bytes, { 'Content-Type': 'application/octet-stream' })
  const data = JSON.parse(fromUtf8(res.body) || '{}')
  if (res.status >= 300) throw new ApiError(res.status, data?.error?.code ?? 'http', data?.error?.message ?? `HTTP ${res.status}`)
  return data as Attachment
}

/**
 * Machine-level critical operations (design §5.3): first a permit with the
 * user's Face ID goes straight to the machine, then the brain may ask it.
 * The machine checks the permit; the brain cannot forge one.
 */
async function withPermit<T>(nodeId: string, match: Record<string, string>, call: () => Promise<T>): Promise<T> {
  await permit(nodeId, match.op ? 'admin' : 'update', match, true)
  return call()
}

/** Typing into a terminal goes from this session straight to the machine. */
async function jobDirect(nodeId: string, k: 'job.input' | 'job.resize', args: Record<string, unknown>) {
  const res = await ask(nodeAddr(nodeId), { k, ...args }, k + '.res')
  if (res.ok !== true) throw new ApiError(400, 'job', (res.error as string) || '机器拒绝了输入')
  return { ok: true }
}

export const api = {
  me: () => request<Me>('GET', '/api/v1/me'),
  threads: (projectId = '') =>
    request<{ threads: Thread[] }>('GET', `/api/v1/threads${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`),
  thread: (id: string) => request<Thread>('GET', `/api/v1/threads/${id}`),
  createThread: (title: string, projectId = '') =>
    request<Thread>('POST', '/api/v1/threads', { title, ...(projectId && { project_id: projectId }) }),
  threadContext: (id: string) => request<ThreadContext>('GET', `/api/v1/threads/${id}/context`),
  /** Folds the older messages into the summary now (runs after any turn in progress). */
  compactThread: (id: string) => request<{ queued: boolean }>('POST', `/api/v1/threads/${id}/compact`),
  putSummary: (id: string, summary: string) => request<{ queued: boolean }>('PUT', `/api/v1/threads/${id}/summary`, { summary }),
  messages: (threadId: string, before = 0) =>
    request<{ messages: Message[]; last_seq: number }>(
      'GET',
      `/api/v1/threads/${threadId}/messages?limit=50${before ? `&before=${before}` : ''}`,
    ),
  notes: (params: { status?: string; project_id?: string; q?: string } = {}) => request<{ notes: Note[] }>('GET', `/api/v1/notes?${new URLSearchParams(params)}`),
  createNote: (text: string, projectId = '', attachments: string[] = []) => request<Note>('POST', '/api/v1/notes', { text, project_id: projectId, attachments }),
  updateNote: (id: string, patch: Partial<Pick<Note, 'text' | 'project_id' | 'status'>>) => request<Note>('PATCH', `/api/v1/notes/${id}`, patch),
  deleteNote: (id: string) => request<void>('DELETE', `/api/v1/notes/${id}`),
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
  renameNode: (id: string, name: string) => request<Node>('PATCH', `/api/v1/nodes/${id}`, { name }),
  runs: (threadId: string) => request<{ runs: Run[] }>('GET', `/api/v1/runs?thread_id=${encodeURIComponent(threadId)}`),
  run: (id: string) => request<{ run: Run; events: HubEvent[]; approvals: Approval[] }>('GET', `/api/v1/runs/${id}`),
  runText: async (id: string, what: 'log' | 'diff') => {
    const res = await rpc('GET', `/api/v1/runs/${id}/${what}`)
    if (res.status >= 300) throw new Error(`HTTP ${res.status}`)
    return fromUtf8(res.body)
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
  /** The user's review of a task waiting for one (decision 0022). */
  verdict: (id: string, pass: boolean, reason = '') => request<Task>('POST', `/api/v1/tasks/${id}/verdict`, { pass, reason }),
  /** Review a finished task again: by the model, a reviewing agent or the user. */
  reviewTask: (id: string, by: 'model' | 'agent' | 'user') => request<Task>('POST', `/api/v1/tasks/${id}/review`, { by }),
  projects: () => request<{ projects: Project[] }>('GET', '/api/v1/projects'),
  createProject: (p: Partial<Project>) => request<Project>('POST', '/api/v1/projects', p),
  updateProject: (id: string, p: Partial<Project>) => request<Project>('PATCH', `/api/v1/projects/${id}`, p),
  deleteProject: (id: string) => request<{ ok: boolean }>('DELETE', `/api/v1/projects/${id}`),
  archiveProject: (id: string) => request<Project>('POST', `/api/v1/projects/${id}/archive`),
  activateProject: (id: string) => request<Project>('POST', `/api/v1/projects/${id}/activate`),
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
  /** Installing an update is critical: the machine wants Face ID for that exact version. */
  updateNode: async (id: string, version: string) => {
    await permit(id, 'update', { version }, true)
    return request<{ version: string }>('POST', `/api/v1/nodes/${id}/update`, { version })
  },
  detectAgents: (id: string) => request<{ agents: AgentInfo[] }>('POST', `/api/v1/nodes/${id}/agents/detect`),
  probeAgent: (id: string, agent: string) =>
    request<{ options: ConfigOption[] | null; at: number }>('POST', `/api/v1/nodes/${id}/agents/${agent}/probe`),
  agentJob: (id: string, agent: string, what: 'install' | 'login' | 'logout', size: { cols: number; rows: number }, upgrade = false) =>
    withPermit(id, { op: `agents.${what}`, agent }, () =>
      request<JobStarted>('POST', `/api/v1/nodes/${id}/agents/${agent}/${what}`, { ...size, upgrade })),
  setAgentDisabled: (id: string, agent: string, disabled: boolean) =>
    request<AgentInfo>('POST', `/api/v1/nodes/${id}/agents/${agent}/disabled`, { disabled }),
  setAgentDefaults: (id: string, agent: string, options: Record<string, string>) =>
    request<{ ok: boolean }>('PUT', `/api/v1/nodes/${id}/agents/${agent}/defaults`, { options }),
  setAgentEnv: (id: string, agent: string, name: string, value: string) =>
    withPermit(id, { op: 'agents.set_env', agent, name }, () =>
      request<{ ok: boolean }>('PUT', `/api/v1/nodes/${id}/agents/${agent}/env`, { name, value })),
  jobTail: (id: string, job: string) => request<JobTail>('GET', `/api/v1/nodes/${id}/jobs/${job}`),
  jobInput: (id: string, job: string, data: string) => jobDirect(id, 'job.input', { job_id: job, data }),
  jobResize: (id: string, job: string, cols: number, rows: number) => jobDirect(id, 'job.resize', { job_id: job, cols, rows }),
  jobCancel: (id: string, job: string) => request<{ ok: boolean }>('POST', `/api/v1/nodes/${id}/jobs/${job}/cancel`),
  /** Fetch the project's checkouts now and look for a new deploy commit. */
  checkDeploy: (projectId: string) =>
    request<{ fetched: number; failures: string[] | null; deployed_commit: string }>('POST', `/api/v1/projects/${projectId}/deploy/check`),
  repoSync: (projectId: string, nodeId: string) => request<RepoResult>('POST', `/api/v1/projects/${projectId}/locations/${nodeId}/sync`),
  repoWip: (projectId: string, nodeId: string) => request<RepoResult>('POST', `/api/v1/projects/${projectId}/locations/${nodeId}/wip`),
  models: () => request<ModelsView>('GET', '/api/v1/models'),
  mcp: () => request<McpView>('GET', '/api/v1/mcp'),
  /** Create, edit (except switching on/off) and approve need a recent login ("reauth"). */
  createMcp: (b: McpInput) => request<McpView>('POST', '/api/v1/mcp/servers', b),
  updateMcp: (id: string, b: McpInput) => request<McpView>('PATCH', `/api/v1/mcp/servers/${id}`, b),
  approveMcp: (id: string) => request<McpView>('POST', `/api/v1/mcp/servers/${id}/approve`),
  reconnectMcp: (id: string) => request<McpView>('POST', `/api/v1/mcp/servers/${id}/reconnect`),
  deleteMcp: (id: string) => request<McpView>('DELETE', `/api/v1/mcp/servers/${id}`),
  /** Creating, changing and deleting providers need a recent login ("reauth"). */
  createProvider: (p: ProviderInput) => request<ModelsView>('POST', '/api/v1/models/providers', p),
  updateProvider: (id: string, p: ProviderInput) => request<ModelsView>('PATCH', `/api/v1/models/providers/${id}`, p),
  deleteProvider: (id: string) => request<ModelsView>('DELETE', `/api/v1/models/providers/${id}`),
  discoverModels: (id: string) => request<{ models: string[] }>('POST', `/api/v1/models/providers/${id}/discover`),
  testModel: (providerId: string, model: string, effort = '') =>
    request<{ ok: boolean; reply?: string; error?: string; ms: number }>('POST', '/api/v1/models/test', { provider_id: providerId, model, effort }),
  /** A role with model "" falls back on the supervisor's. */
  putRoles: (roles: Record<string, { provider_id: string; model: string; effort?: string; context_window?: number } | null>) =>
    request<ModelsView>('PUT', '/api/v1/models/roles', roles),
  shells: (projectId: string) => request<{ shells: Shell[] }>('GET', `/api/v1/projects/${projectId}/shells`),
  /** Opening a terminal is critical: Face ID, checked by the machine. */
  openShell: (projectId: string, nodeId: string, cols: number, rows: number) =>
    withPermit(nodeId, { op: 'shell.open' }, () =>
      request<{ node_id: string; job_id: string; command: string; root: string }>('POST', `/api/v1/projects/${projectId}/shells`, { node_id: nodeId, cols, rows })),
  readFile: (id: string, agent: string, file: string) => request<ConfigFile>('GET', `/api/v1/nodes/${id}/files/${agent}/${file}`),
  writeFile: (id: string, agent: string, file: string, content: string, base: string) =>
    withPermit(id, { op: 'config.write', agent, file }, () =>
      request<ConfigFile>('PUT', `/api/v1/nodes/${id}/files/${agent}/${file}`, { content, base_sha256: base })),
  /** Sends a message; without a topic it starts one (its id is the message id). */
  send: (threadId: string, content: string, context?: MessageContext, attachments?: string[], topicId = '') =>
    request<{ message_id: string; seq: number; topic_id: string }>('POST', `/api/v1/threads/${threadId}/messages`, {
      content, context, attachments, ...(topicId && { topic_id: topicId }),
    }),
  topics: (threadId: string) =>
    request<{ topics: Topic[]; earlier: { count: number; last?: Message } }>('GET', `/api/v1/threads/${threadId}/topics`),
  /** Meetings: an independent thread that ends with minutes back in its parent. */
  createMeeting: (input: { parent_thread_id: string; parent_topic_id?: string; question: string; note_ids?: string[]; project_id?: string; host?: MeetingHost }) =>
    request<{ meeting: Thread }>('POST', '/api/v1/meetings', input),
  meetings: (parentThreadId = '') =>
    request<{ meetings: Thread[] }>('GET', `/api/v1/meetings${parentThreadId ? `?parent_thread_id=${encodeURIComponent(parentThreadId)}` : ''}`),
  meeting: (id: string) => request<MeetingDetail>('GET', `/api/v1/meetings/${id}`),
  closeMeeting: (id: string) => request<{ queued: boolean }>('POST', `/api/v1/meetings/${id}/close`, {}),
  reopenMeeting: (id: string) => request<{ ok: boolean }>('POST', `/api/v1/meetings/${id}/reopen`, {}),
  postMinutes: (id: string, content = '') => request<{ message_id: string }>('POST', `/api/v1/meetings/${id}/minutes`, content ? { content } : {}),
  setMeetingHost: (id: string, host: MeetingHost | null) => request<Thread>('PATCH', `/api/v1/meetings/${id}`, { host }),
  /** Talks to one agent directly inside a project chat (decision 0028). */
  dm: (threadId: string, content: string, to: { run_id?: string; task_id?: string; consult?: boolean }, topicId = '') =>
    request<{ message_id: string }>('POST', `/api/v1/threads/${threadId}/dm`, { content, to, ...(topicId && { topic_id: topicId }) }),
  topic: (id: string) => request<Topic>('GET', `/api/v1/topics/${encodeURIComponent(id)}`),
  /** A topic's messages; '' is the thread's messages from before topics. */
  topicMessages: (threadId: string, topicId: string, before = 0) =>
    request<{ messages: Message[]; last_seq: number }>(
      'GET',
      `/api/v1/threads/${threadId}/messages?limit=200&topic=${encodeURIComponent(topicId)}${before ? `&before=${before}` : ''}`,
    ),
  patchTopic: (id: string, patch: { title?: string; status?: 'open' | 'closed' }) =>
    request<Topic>('PATCH', `/api/v1/topics/${encodeURIComponent(id)}`, patch),
  /** Takes an attached message back into the topic it started. */
  moveMessage: (id: string, topicId: string) => request<Message>('POST', `/api/v1/messages/${encodeURIComponent(id)}/move`, { topic_id: topicId }),
  taskTalk: (id: string) => request<{ talk: Talk[] }>('GET', `/api/v1/tasks/${encodeURIComponent(id)}/talk`),
  /** Stores a file for a message to refer to; the Hub decides its type from the bytes. */
  upload: (file: Blob, name: string) => upload(file, name),

  systemGraph: () => request<SysGraph>('GET', '/api/v1/system/graph'),
  systemHistory: (since?: number) => request<SysHistory>('GET', `/api/v1/system/history${since === undefined ? '' : `?since=${since}`}`),
  systemTrace: (kind: 'task' | 'run' | 'thread', id: string) => request<SysTrace>('GET', `/api/v1/system/trace?${kind}_id=${encodeURIComponent(id)}`),

  // M5 workbench
  overview: () => request<Overview>('GET', '/api/v1/overview'),
  personas: () => request<{ personas: Persona[] }>('GET', '/api/v1/personas'),
  createPersona: (p: Partial<Persona>) => request<Persona>('POST', '/api/v1/personas', p),
  updatePersona: (id: string, p: Partial<Persona>) => request<Persona>('PATCH', `/api/v1/personas/${id}`, p),
  deletePersona: (id: string) => request<unknown>('DELETE', `/api/v1/personas/${id}`),
  repos: () => request<{ repos: RepoScan[] }>('GET', '/api/v1/repos'),
  setRole: (id: string, role: 'dev' | 'server') =>
    withPermit(id, { op: 'node.set_role' }, () => request<{ role: string }>('PUT', `/api/v1/nodes/${id}/role`, { role })),
  setWorkspaces: (id: string, workspaces: string[]) =>
    withPermit(id, { op: 'node.set_workspaces' }, () =>
      request<{ workspaces: string[]; repos: unknown[] }>('PUT', `/api/v1/nodes/${id}/workspaces`, { workspaces })),
  scanNode: (id: string) => request<{ workspaces: string[] }>('POST', `/api/v1/nodes/${id}/scan`),
  newProject: (b: { name: string; node_id?: string; layout?: 'wokspace' | 'plain'; owner?: string; private?: boolean; thread_id?: string }) =>
    request<{ project: Project; run: Run }>('POST', '/api/v1/projects/new', b),
  projectTree: (id: string, path = '') =>
    request<{ node_id: string; result: ProjectDir }>('GET', `/api/v1/projects/${id}/tree?${new URLSearchParams({ path })}`),
  projectFile: (id: string, path: string) =>
    request<{ node_id: string; result: ProjectFile }>('GET', `/api/v1/projects/${id}/file?${new URLSearchParams({ path })}`),
  writeProjectFile: (id: string, path: string, content: string, base: string) =>
    request<{ node_id: string; result: ProjectFile }>('PUT', `/api/v1/projects/${id}/file`, { path, content, base_sha256: base }),
  cloneProject: (id: string, nodeId = '') => request<{ run: Run }>('POST', `/api/v1/projects/${id}/clone`, { node_id: nodeId }),
}

export interface ProjectEntry { name: string; dir: boolean; size: number }
export interface ProjectDir { rel: string; entries: ProjectEntry[] }
export interface ProjectFile { rel: string; size: number; binary: boolean; content: string; sha256: string; data?: string }
