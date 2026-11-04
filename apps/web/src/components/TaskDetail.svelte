<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { agentChoices, isOpen, modeText, statusText } from '../lib/tasks'
  import type { Node, Persona, Project, Run, Task } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { id, onClose, onOpenTask, onOpenThread, projectId: presetProject = '', compact = false }: {
    id: string // 'new' for a new task
    onClose: () => void
    onOpenTask: (id: string) => void
    onOpenThread: (id: string) => void
    /** A new task's project. */
    projectId?: string
    /** Embedded in the task page: no run list (the page shows the runs). */
    compact?: boolean
  } = $props()

  let task = $state<Task | null>(null)
  let runs = $state<Run[]>([])
  let subtasks = $state<Task[]>([])
  let projects = $state<Project[]>([])
  let nodes = $state<Node[]>([])
  let personas = $state<Persona[]>([])
  let error = $state('')
  let missing = $state(false)
  let busy = $state(false)

  // The form.
  let title = $state('')
  let detail = $state('')
  let projectId = $state('')
  let priority = $state(0)
  let agent = $state('')
  let node = $state('')
  let mode = $state('')
  let persona = $state('')
  let kind = $state<Task['kind'] | ''>('')
  let dirty = $state(false)

  const isNew = $derived(id === 'new')
  const agents = $derived(agentChoices(nodes))

  async function load() {
    try {
      const [p, n, ps] = await Promise.all([api.projects(), api.nodes(), api.personas()])
      projects = p.projects
      nodes = n.nodes
      personas = ps.personas
      if (isNew && !dirty && presetProject && !projectId && presetProject !== 'misc') projectId = presetProject
      if (!isNew) {
        const d = await api.task(id)
        task = d.task
        runs = d.runs
        subtasks = d.subtasks
        if (!dirty) fill(d.task)
      }
      error = ''
    } catch (e) {
      if ((e as { status?: number }).status === 404) missing = true
      else error = (e as Error).message
    }
  }

  function fill(t: Task) {
    title = t.title
    detail = t.detail
    projectId = t.project_id === 'misc' ? '' : t.project_id
    priority = t.priority
    agent = t.agent_pref
    node = t.node_pref
    mode = t.permission_mode
    persona = t.persona_id
    kind = t.kind
  }

  $effect(() => {
    void chat.tasksRev
    void id
    load()
  })

  async function act(fn: () => Promise<unknown>) {
    busy = true
    error = ''
    try {
      await fn()
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  // The "misc" project stands for "no repository".
  const pid = () => projectId || (projects.some((p) => p.id === 'misc') ? 'misc' : '')

  async function save(dispatch = false) {
    if (!title.trim()) {
      error = '标题不能为空'
      return
    }
    await act(async () => {
      if (isNew) {
        const t = await api.createTask({
          title, detail, project_id: pid(), priority, agent_pref: agent, node_pref: node,
          permission_mode: mode as Task['permission_mode'], persona_id: persona, dispatch,
          kind: kind || (projectId && projectId !== 'misc' ? 'code' : 'general'),
        })
        dirty = false
        onOpenTask(t.id)
        return
      }
      await api.patchTask(id, {
        title, detail, project_id: pid(), priority, agent_pref: agent, node_pref: node,
        permission_mode: mode as Task['permission_mode'], persona_id: persona,
      })
      dirty = false
      if (dispatch) await api.dispatchTask(id)
    })
  }

  async function cancel() {
    if (!confirm('取消这个任务？正在执行的 Run 会被停止，未完成的子任务也会一起取消。')) return
    await act(() => api.cancelTask(id))
  }

  const nodeName = (nid: string) => nodes.find((n) => n.id === nid)?.name ?? '未知机器'
  const purposeText: Record<string, string> = { work: '开发', rebase: 'rebase', deliver: '交付' }
  const runStatus: Record<string, string> = {
    queued: '排队中', running: '进行中', succeeded: '成功', failed: '失败', cancelled: '已取消', interrupted: '已中断',
  }
  const canDispatch = $derived(task && !['running', 'waiting_approval', 'review'].includes(task.status) && !(task.status === 'done' && task.pr_url))
  const when = (ts: number) => new Date(ts).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
</script>

<Sheet title={isNew ? '新任务' : '任务'} {onClose} wide>
  {#if missing}
    <p class="muted">这个任务不存在，可能链接已经过期。</p>
  {:else}
  {#if task}
    <div class="status-row">
      <span class="badge {task.status}">{statusText[task.status]}</span>
      {#if task.status_reason}<span class="reason">{task.status_reason}</span>{/if}
    </div>
    {#if task.pr_url}
      <p class="pr">PR：<a href={task.pr_url} target="_blank" rel="noreferrer">{task.pr_url}</a>（sous 不会合并，请你评审后合并）</p>
    {/if}
    {#if task.review_note && !task.status_reason.includes(task.review_note)}<p class="review"><strong>验收：</strong>{task.review_note}</p>{/if}
  {/if}

  <form class="form" oninput={() => (dirty = true)} onsubmit={(e) => { e.preventDefault(); save() }}>
    <label>标题<input bind:value={title} required /></label>
    <label>说明（交给 agent 的完整任务说明）<textarea bind:value={detail} rows="5"></textarea></label>
    <div class="grid">
      <label>项目
        <select bind:value={projectId} disabled={!isNew && !!task && task.status !== 'inbox' && task.status !== 'planned'}>
          <option value="">杂项（一般任务）</option>
          {#each projects.filter((p) => p.id !== 'misc') as p}<option value={p.id}>{p.name}</option>{/each}
        </select>
      </label>
      <label>岗位
        <select bind:value={persona}>
          <option value="">按项目默认</option>
          {#each personas as p}<option value={p.id}>{p.glyph} {p.name}</option>{/each}
        </select>
      </label>
      {#if isNew}
        <label>类型
          <select bind:value={kind}>
            <option value="">自动</option>
            <option value="code">代码任务</option>
            <option value="general">一般任务</option>
            <option value="ops">运维任务（服务器）</option>
          </select>
        </label>
      {/if}
      <label>优先级<input type="number" bind:value={priority} min="-10" max="10" /></label>
      <label>agent
        <select bind:value={agent}>
          <option value="">按项目或全局默认</option>
          {#each agents as a}<option value={a}>{a}</option>{/each}
        </select>
      </label>
      <label>机器
        <select bind:value={node}>
          <option value="">按项目的优先级</option>
          {#each nodes as n}<option value={n.id}>{n.name}{n.online ? '' : '（离线）'}</option>{/each}
        </select>
      </label>
      <label>权限模式
        <select bind:value={mode}>
          <option value="">按项目或全局默认</option>
          {#each Object.entries(modeText) as [k, v]}<option value={k}>{v}</option>{/each}
        </select>
      </label>
    </div>
    <div class="actions">
      {#if isNew}
        <button class="btn small" type="button" disabled={busy} onclick={() => save(true)}>创建并派发</button>
        <button class="btn small ghost" type="submit" disabled={busy}>只创建</button>
      {:else}
        {#if dirty}<button class="btn small ghost" type="submit" disabled={busy}>保存</button>{/if}
        {#if canDispatch}
          <button class="btn small" type="button" disabled={busy} onclick={() => save(true)}>
            {task?.status === 'failed' || task?.status === 'blocked' ? '重试' : task?.status === 'queued' ? '重新派发' : '派发'}
          </button>
        {/if}
        {#if task && isOpen(task)}<button class="link danger" type="button" disabled={busy} onclick={cancel}>取消任务</button>{/if}
        {#if task && !isOpen(task) && !task.dismissed}
          <button class="link" type="button" disabled={busy} onclick={() => act(() => api.patchTask(id, { dismissed: true }))}>标记已处理</button>
        {/if}
      {/if}
    </div>
  </form>
  {#if error}<p class="err" role="alert">{error}</p>{/if}

  {#if task}
    {#if subtasks.length}
      <section>
        <h3>子任务</h3>
        <ul class="rows">
          {#each subtasks as s (s.id)}
            <li><button class="row" onclick={() => onOpenTask(s.id)}><span class="st">{statusText[s.status]}</span>{s.title}</button></li>
          {/each}
        </ul>
      </section>
    {/if}
    {#if task.depends_on.length}
      <section>
        <h3>依赖</h3>
        <ul class="rows">
          {#each task.depends_on as d}<li><button class="row" onclick={() => onOpenTask(d)}>{d}</button></li>{/each}
        </ul>
      </section>
    {/if}
    {#if !compact}
    <section>
      <h3>执行记录</h3>
      {#if runs.length === 0}<p class="muted">还没有执行过。</p>{/if}
      <ul class="rows">
        {#each runs as r (r.id)}
          <li>
            <button class="row" onclick={() => { onClose(); onOpenThread(r.thread_id) }} title="在对话里查看">
              <span class="st">{purposeText[r.purpose] ?? r.purpose}</span>
              <span>{r.purpose === 'deliver' ? 'sous' : r.agent} @ {nodeName(r.node_id)} · {runStatus[r.status] ?? r.status}</span>
              <span class="muted">{when(r.created_at)} · {modeText[r.permission_mode] ?? r.permission_mode}</span>
              {#if r.error}<span class="err">{r.error}</span>{/if}
              {#if r.diffstat}<span class="muted">{r.diffstat}</span>{/if}
            </button>
          </li>
        {/each}
      </ul>
      {#if task.branch}<p class="muted">分支：{task.branch}{task.base_branch ? ` → ${task.base_branch}` : ''}</p>{/if}
    </section>
    {/if}
  {/if}
  {/if}
</Sheet>

<style>
  .status-row { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; }
  .badge { font-size: 13px; font-weight: 600; color: var(--warm); }
  .badge.blocked, .badge.failed { color: var(--danger); }
  .badge.done { color: #3a9d5d; }
  .reason { color: var(--muted); font-size: 13px; overflow-wrap: anywhere; }
  .pr, .review { margin: 0; font-size: 14px; overflow-wrap: anywhere; }
  .pr a { color: var(--accent); }
  .form { display: grid; gap: 10px; }
  label { display: grid; gap: 4px; font-size: 13px; color: var(--muted); }
  input, textarea, select {
    border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; color: var(--text);
  }
  textarea { resize: vertical; line-height: 1.5; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; }
  .actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
  .btn.small { padding: 6px 16px; font-size: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  h3 { margin: 4px 0 6px; font-size: 13px; color: var(--muted); }
  .rows { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; }
  .row {
    display: flex; flex-wrap: wrap; gap: 4px 10px; width: 100%; text-align: left; border: 1px solid var(--line);
    background: var(--panel); border-radius: 10px; padding: 7px 10px; font-size: 13px;
  }
  .row:hover { border-color: var(--accent); }
  .st { font-weight: 600; color: var(--warm); }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; overflow-wrap: anywhere; }
</style>
