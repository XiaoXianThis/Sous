<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { agentChoices, modeText } from '../lib/tasks'
  import type { Node, Project, ProjectNode } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let projects = $state<Project[]>([])
  let nodes = $state<Node[]>([])
  let error = $state('')
  let editing = $state<string | null>(null) // project id or 'new'
  let draft = $state({ name: '', repo_url: '', default_agent: '', default_mode: '', nodes: [] as ProjectNode[] })
  let saving = $state(false)

  async function load() {
    try {
      const [p, n] = await Promise.all([api.projects(), api.nodes()])
      projects = p.projects
      nodes = n.nodes
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }

  $effect(() => {
    void chat.tasksRev
    void chat.nodesRev
    load()
  })

  function edit(p?: Project) {
    editing = p?.id ?? 'new'
    draft = {
      name: p?.name ?? '', repo_url: p?.repo_url ?? '', default_agent: p?.default_agent ?? '', default_mode: p?.default_mode ?? '',
      nodes: (p?.nodes ?? []).map((n) => ({ ...n })),
    }
  }

  function move(i: number, d: number) {
    const j = i + d
    if (j < 0 || j >= draft.nodes.length) return
    const ns = [...draft.nodes]
    ;[ns[i], ns[j]] = [ns[j], ns[i]]
    draft.nodes = ns
  }

  function addNode(id: string) {
    if (!id || draft.nodes.some((n) => n.node_id === id)) return
    draft.nodes = [...draft.nodes, { node_id: id, path: '', priority: draft.nodes.length }]
  }

  async function save() {
    saving = true
    error = ''
    try {
      const body = { ...draft, default_mode: draft.default_mode as Project['default_mode'], nodes: draft.nodes.map((n, i) => ({ ...n, priority: i })) }
      if (editing === 'new') await api.createProject(body)
      else if (editing) await api.updateProject(editing, body)
      editing = null
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      saving = false
    }
  }

  async function remove(p: Project) {
    if (!confirm(`删除项目“${p.name}”？不会删除任何代码。`)) return
    try {
      await api.deleteProject(p.id)
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  const nodeName = (id: string) => nodes.find((n) => n.id === id)?.name ?? '已删除的机器'
  const agents = $derived(agentChoices(nodes))
</script>

<Sheet title="项目" {onClose}>
  <p class="hint">登记项目在哪些电脑的哪个路径。代码任务会按顺序选第一台在线、装了所需 agent 且有空闲的电脑。</p>

  {#if editing}
    <form class="form" onsubmit={(e) => { e.preventDefault(); save() }}>
      <label>名字<input bind:value={draft.name} required placeholder="比如 sous" /></label>
      <label>仓库地址（可选）<input bind:value={draft.repo_url} placeholder="https://github.com/…" /></label>
      <div class="grid">
        <label>默认 agent
          <select bind:value={draft.default_agent}>
            <option value="">按全局默认</option>
            {#each agents as a}<option value={a}>{a}</option>{/each}
          </select>
        </label>
        <label>默认权限模式
          <select bind:value={draft.default_mode}>
            <option value="">按全局默认</option>
            {#each Object.entries(modeText) as [k, v]}<option value={k}>{v}</option>{/each}
          </select>
        </label>
      </div>
      <div class="machines">
        <span class="label">所在机器（按优先级排列）</span>
        {#each draft.nodes as n, i (n.node_id)}
          <div class="machine">
            <span class="order">{i + 1}</span>
            <strong>{nodeName(n.node_id)}</strong>
            <input bind:value={n.path} placeholder="仓库在这台电脑上的绝对路径" aria-label="路径" required />
            <button type="button" class="icon-btn" aria-label="上移" disabled={i === 0} onclick={() => move(i, -1)}>↑</button>
            <button type="button" class="icon-btn" aria-label="下移" disabled={i === draft.nodes.length - 1} onclick={() => move(i, 1)}>↓</button>
            <button type="button" class="icon-btn" aria-label="移除" onclick={() => (draft.nodes = draft.nodes.filter((x) => x !== n))}>✕</button>
          </div>
        {/each}
        {#if nodes.some((n) => !draft.nodes.some((d) => d.node_id === n.id))}
          <select aria-label="添加机器" onchange={(e) => { addNode(e.currentTarget.value); e.currentTarget.value = '' }}>
            <option value="">＋ 添加机器</option>
            {#each nodes.filter((n) => !draft.nodes.some((d) => d.node_id === n.id)) as n}<option value={n.id}>{n.name}</option>{/each}
          </select>
        {:else if nodes.length === 0}
          <p class="muted">还没有配对机器，先去“机器”页面配对一台。</p>
        {/if}
      </div>
      <div class="actions">
        <button class="btn small" type="submit" disabled={saving}>保存</button>
        <button class="link" type="button" onclick={() => (editing = null)}>取消</button>
      </div>
    </form>
  {:else}
    <button class="btn small add" onclick={() => edit()}>登记项目</button>
  {/if}
  {#if error}<p class="err" role="alert">{error}</p>{/if}

  <ul>
    {#each projects as p (p.id)}
      <li>
        <div class="head"><strong>{p.name}</strong>
          {#if p.default_agent}<span class="sub">默认 {p.default_agent}</span>{/if}
          {#if p.default_mode}<span class="sub">{modeText[p.default_mode]}</span>{/if}
        </div>
        <ol class="where">
          {#each p.nodes as n}<li><span>{nodeName(n.node_id)}</span><code>{n.path}</code></li>{/each}
        </ol>
        {#if p.nodes.length === 0}<p class="muted">还没有登记机器，代码任务派不出去。</p>{/if}
        <div class="row-actions">
          <button class="link" onclick={() => edit(p)}>编辑</button>
          <button class="link danger" onclick={() => remove(p)}>删除</button>
        </div>
      </li>
    {:else}
      {#if !editing}<li class="empty">还没有项目。</li>{/if}
    {/each}
  </ul>
</Sheet>

<style>
  .hint { color: var(--muted); font-size: 13px; margin: 0; }
  .btn.small { padding: 7px 16px; font-size: 14px; }
  .add { justify-self: start; }
  .form { display: grid; gap: 10px; border: 1px dashed var(--accent); border-radius: 12px; padding: 12px; }
  label, .label { display: grid; gap: 4px; font-size: 13px; color: var(--muted); }
  input, select { border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; color: var(--text); min-width: 0; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .machines { display: grid; gap: 6px; }
  .machine { display: grid; grid-template-columns: auto auto 1fr auto auto auto; gap: 6px; align-items: center; }
  .order { width: 20px; height: 20px; border-radius: 50%; background: var(--sunken); display: grid; place-items: center; font-size: 12px; }
  .machine .icon-btn { width: 28px; height: 28px; }
  .actions { display: flex; gap: 14px; align-items: center; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
  li { border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; display: grid; gap: 6px; }
  li.empty { color: var(--muted); text-align: center; border-style: dashed; padding: 20px; }
  .head { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
  .sub { color: var(--muted); font-size: 12px; border: 1px solid var(--line); border-radius: 999px; padding: 0 8px; }
  .where { margin: 0; padding-left: 20px; font-size: 13px; display: grid; gap: 2px; }
  .where li { border: 0; padding: 0; display: list-item; }
  .where code { margin-left: 8px; color: var(--muted); font: 12px ui-monospace, Menlo, Consolas, monospace; overflow-wrap: anywhere; }
  .row-actions { display: flex; justify-content: flex-end; gap: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) {
    .grid { grid-template-columns: 1fr; }
    .machine { grid-template-columns: auto 1fr auto auto auto; }
    .machine strong { grid-column: 2 / -1; }
    .machine input { grid-column: 1 / 3; }
  }
</style>
