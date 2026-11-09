// Shapes from docs/api/core-hub-http.md.

export interface Thread {
  id: string
  title: string
  kind: 'main' | 'chat' | 'project'
  /** Set on a project chat: the project it is scoped to. */
  project_id?: string
  created_at: number
  updated_at: number
  archived: boolean
}

/** A file sent along with a user message. */
export interface Attachment {
  id: string
  name: string
  mime: string
  size: number
}

export interface Message {
  id: string
  thread_id: string
  role: 'user' | 'assistant'
  content: string
  error?: boolean
  reply_to?: number
  /**
   * "notice": a proactive notification; "silent": a turn that chose not to
   * reply; "report": a task's result posted by the task engine; "trigger":
   * input nobody typed (a schedule or a follow-up, role user); "inspector":
   * the inspector's note about a finding; "consult": a project consultant's
   * answer, shown as it is (decision 0022).
   */
  kind?: 'notice' | 'silent' | 'report' | 'trigger' | 'inspector' | 'step' | 'consult'
  seq: number
  created_at: number
  /** Set while the reply is still streaming; never sent by the server. */
  streaming?: boolean
  /** Reasoning text streamed before the answer, if the model sends it. */
  thinking?: string
  /** What the user was looking at on the workbench when they wrote it. */
  context?: MessageContext
  /** The files of a user message. */
  attachments?: Attachment[]
  /** Who wrote a consult message: the consultant's run, agent and project. */
  source?: MessageSource
  /** The supervisor's own tool calls after this message of its turn. */
  tools?: ToolNote[]
}

/** One tool call of the supervisor; the Hub summarizes and masks the arguments. */
export interface ToolNote {
  name: string
  args?: string
  /** First line of the error; absent when the call succeeded. */
  error?: string
}

export interface MessageSource {
  run_id: string
  agent: string
  project_id: string
  project?: string
  node_id?: string
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
  /** Commands sous runs for these actions; absent when there is none here. */
  install?: string
  upgrade?: string
  login?: string
  logout?: string
  /** Commands the agent needs that are missing on the machine (npx). */
  missing?: string[]
  /** Environment variables that hold an API key, and the ones set. */
  key_env?: string[]
  env_set?: string[]
  /** Session options the agent reported (models, reasoning levels). */
  options?: ConfigOption[]
  options_at?: number
  defaults?: Record<string, string>
  configs?: AgentConfigFile[]
  launch_override?: boolean
  /** From node.json (decision 0024): turned off on this machine, the
   *  machine's default agent, added in custom_agents. */
  disabled?: boolean
  default?: boolean
  custom?: boolean
  /** The install or login running for it now. */
  job?: JobStarted
}

export interface ConfigOption {
  id: string
  name: string
  description?: string
  category?: string
  current: string
  values: { value: string; name: string; description?: string; group?: string }[]
}

export interface AgentConfigFile {
  id: string
  path: string
  format: string
}

export interface ConfigFile {
  agent: string
  file: string
  path: string
  format: string
  exists: boolean
  content: string
  sha256: string
  backup?: string
}

export type JobKind = 'install' | 'upgrade' | 'login' | 'logout' | 'shell'

export interface JobStarted {
  job_id: string
  kind: JobKind
  agent: string
  command: string
}

export interface JobTail extends JobStarted {
  offset: number
  data: string
  running: boolean
  exit_code?: number
}

/** A terminal the current login session opened in a project directory. */
export interface Shell {
  node_id: string
  job_id: string
  root: string
  command: string
  started_at: number
  running: boolean
  exit_code?: number
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
  /** Remote administration: on, off (node.json), unsupported (old sous), or absent when offline. */
  admin?: 'on' | 'off' | 'unsupported'
  /** M5: dev machine or server, its workspaces, and the wok version ('' when missing). */
  role?: 'dev' | 'server'
  workspaces?: string[]
  wok?: string
}

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted'

export type RunPurpose = 'work' | 'rebase' | 'deliver' | 'clone' | 'create' | 'review' | 'git' | 'consult'

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
  kind: 'code' | 'general' | 'ops' | 'in_place'
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
  paused?: boolean
  pause_reason?: string
  archived?: boolean
  recovery_note?: string
  last_event?: number
  /** M5: the persona's name and session options, and what a setup run does. */
  persona?: string
  options?: Record<string, string>
  setup?: { name: string; remote?: string; layout?: string; owner?: string; private?: boolean }
  /** Decision 0022: git runs, consult runs, where a task branch started. */
  base_from?: string
  base_commit?: string
  git?: GitOp
  git_result?: GitResult
  project_id?: string
  session?: string
  resume_session?: string
  /** How the run ended beyond its status (end-to-end review 4.1, 4.2, 4.14). */
  outcome?: 'no_action' | 'work_produced' | 'needs_confirmation'
  error_kind?: 'agent_quota' | 'agent_rate_limit' | 'agent_not_logged_in' | 'agent_unavailable'
  refused?: string[]
}

export interface GitOp {
  op: 'commit' | 'merge' | 'push' | 'pr' | 'undo' | 'discard' | 'restore'
  into?: string
  base?: string
  branch?: string
  remote?: string
  squash?: boolean
  in_place?: boolean
}

export interface GitResult {
  op: string
  branch?: string
  before?: string
  after?: string
  commits?: number
  conflicts?: string[]
  pr_url?: string
  message: string
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
  kind: 'code' | 'general' | 'ops'
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
  persona_id: string
  depends_on: string[]
  created_at: number
  updated_at: number
  /** Decision 0022: where it works, how it is reviewed, its branch's fate. */
  place: TaskPlace | ''
  review: TaskReview | ''
  on_fail: 'rework' | 'ask' | ''
  base_from: 'local' | 'remote' | ''
  base_commit: string
  git_state: GitTaskState | ''
  deploy_commit?: string
  /** Who did the latest work run, and on which machine (end-to-end review 4.4). */
  last_agent?: string
  last_node?: string
  /** The task's own session settings over its preset's (decision 0024). */
  options?: { model?: string; effort?: string }
}

export type TaskPlace = 'worktree' | 'in_place' | 'scratch'
export type TaskReview = 'none' | 'model' | 'agent' | 'user'
export type GitTaskState = 'unmerged' | 'merged' | 'pr' | 'discarded' | 'restored'

export interface ProjectNode {
  node_id: string
  path: string
  priority: number
  /** "scan": found in the machine's workspaces; "manual": entered by hand. */
  source?: 'scan' | 'manual'
  /** Where the checkout stands, from the machine's last scan. */
  git?: GitState
}

export interface GitState {
  head?: string
  upstream?: string
  ahead: number
  behind: number
  changes: number
  untracked: number
  fetched_at?: number
}

export interface RepoResult {
  outcome: 'up_to_date' | 'fast_forwarded' | 'dirty' | 'diverged' | 'no_upstream' | 'pushed' | 'nothing'
  message: string
  branch?: string
  commit?: string
  git?: GitState
}

export interface ModelProvider {
  id: string
  name: string
  kind: string
  base_url: string
  api_key_env: string
  models: string[]
  key_set: boolean
  env_set: boolean
  default_url: string
  created_at: number
  updated_at: number
}

export interface ModelRole {
  provider_id: string
  model: string
  effort?: string
  /** The model's context window in tokens; 0 or missing means 128000. */
  context_window?: number
  /** No choice of its own: uses the supervisor's. */
  inherited?: boolean
}

/** A thread's context (GET /threads/{id}/context, long-main-thread). */
export interface ThreadContext {
  /** 0 before the thread's first turn. */
  segment: number
  /** Messages up to this seq are folded into the summary. */
  covers_up_to: number
  summary: string
  profile: string[]
  reason?: string
  compacted_at?: number
  /** Estimated input tokens of the next turn. */
  used: number
  window: number
  soft: number
  hard: number
  cache: { calls: number; input_tokens: number; cached_tokens: number; last_input: number }
}

export interface ModelsView {
  providers: ModelProvider[]
  roles: Record<string, ModelRole>
  role_order: string[]
  kinds: string[]
  efforts: string[]
}

export interface McpKey { name: string; set: boolean }

export interface McpTool {
  name: string
  description: string
  disabled: boolean
  read_only: boolean
  /** The name the model sees. */
  exposed?: string
}

/** An MCP server as the Hub returns it. Secret values never come back, only their names. */
export interface McpServer {
  id: string
  name: string
  slug: string
  transport: 'stdio' | 'http'
  command: string
  args: string[]
  url: string
  enabled: boolean
  /** The agent proposed it and the user has not approved it: it does not run. */
  pending: boolean
  source: 'user' | 'agent'
  note: string
  state: 'off' | 'connecting' | 'ready' | 'error'
  error?: string
  tools: McpTool[]
  disabled_tools: string[]
  env_keys: McpKey[]
  header_names: McpKey[]
  /** Secrets still waiting for a value. */
  missing: string[]
}

export interface McpView {
  servers: McpServer[]
  stdio_allowed: boolean
}

export interface Deploy {
  node_id: string
  path: string
  command: string
  health?: string
  /** The deploy branch: new commits on origin/<branch> get deployed. */
  branch?: string
  /** Deploy every new commit of branch automatically. */
  auto_on_merge?: boolean
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
  remote_key?: string
  default_persona?: string
  notes?: string
  layout?: 'wokspace' | ''
  deploy?: Deploy | null
  /** When the project was archived (unix ms); 0 or missing means active. */
  archived_at?: number
  /** The deploy branch's commit that is deployed (or the baseline). */
  deployed_commit?: string
  /** When the project or any of its tasks last changed. */
  touched_at?: number
}

export type PersonaColor = 'purple' | 'teal' | 'coral' | 'pink' | 'blue' | 'green' | 'amber' | 'gray'

export interface Persona {
  id: string
  name: string
  glyph: string
  color: PersonaColor
  agent: string
  options: Record<string, string>
  brief: string
  default_mode: PermissionMode | ''
  kinds: ('code' | 'general' | 'ops')[]
  builtin: boolean
  created_at: number
  updated_at: number
}

export interface RepoScan {
  node_id: string
  path: string
  remote: string
  remote_key: string
  branch: string
  dirty: boolean
  wokspace: boolean
  seen_at: number
  project_id: string
}

export interface Overview {
  projects: Project[]
  nodes: Node[]
  personas: Persona[]
  tasks: Task[]
  runs: Run[]
  approvals: Approval[]
  discovered: RepoScan[]
}

export interface MessageContext {
  project_id?: string
  task_id?: string
  /** Projects the user referenced with @ in the message. */
  mentions?: Mention[]
}

/** A project referenced with @; `name` is what the message text shows after the @. */
export interface Mention {
  project_id: string
  name: string
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

export interface Schedule {
  id: string
  title: string
  cron: string
  tz: string
  prompt: string
  thread_id: string
  enabled: boolean
  last_run: number
  next_run: number
  created_at: number
  updated_at: number
}

export interface UsageRow {
  provider: string
  model: string
  role: string
  calls: number
  input_tokens: number
  output_tokens: number
  cached_tokens: number
}

export interface UsageDay {
  day: string
  role: string
  calls: number
  input_tokens: number
  output_tokens: number
  cached_tokens: number
}

export interface Usage {
  days: number
  since: number
  usage: UsageRow[]
  daily: UsageDay[]
  /** By project; project_id '' is the main conversation and the rest. */
  by_project?: { project_id: string; name: string; calls: number; input_tokens: number; output_tokens: number; cached_tokens: number }[]
}

export interface NodeVersion {
  id: string
  name: string
  version: string
  online: boolean
  os: string
  arch: string
  update_available: boolean
}

export interface Updates {
  hub: { version: string; update_available?: boolean }
  latest?: { tag: string; name: string; url: string; published_at: string; notes: string }
  error?: string
  checked_at: number
  repo: string
  nodes: NodeVersion[]
}
