<script lang="ts">
  // All projects, the repositories the machines found that are not projects
  // yet, and creating a new project (a wokspace by default).
  import { api } from '../lib/api'
  import { go, loadOverview, ui, wb } from '../lib/app.svelte'
  import { looks } from '../lib/status'
  import { rows } from '../lib/workbench'
  import type { RepoScan } from '../lib/types'
  import Icon from './Icon.svelte'
  import ProjectEditor from './ProjectEditor.svelte'

  let editor = $state<null | { name: string; repo_url: string; node_id?: string; path?: string }>(null)
  let creating = $state(false)
  let name = $state('')
  let nodeId = $state('')
  let layout = $state<'wokspace' | 'plain'>('wokspace')
  let owner = $state('')
  let isPrivate = $state(true)
  let error = $state('')
  let busy = $state(false)

  const data = $derived(wb.data)
  const all = $derived(data ? rows({ ...data, tasks: data.tasks }) : [])
  const projects = $derived(data?.projects.filter((p) => p.id !== 'misc') ?? [])
  const devs = $derived((data?.nodes ?? []).filter((n) => n.role !== 'server' && n.online && (n.workspaces?.length ?? 0) > 0))
  const nodeName = (id: string) => data?.nodes.find((n) => n.id === id)?.name ?? '未知机器'
  const repoName = (r: RepoScan) => r.path.split(/[\\/]/).pop() ?? r.path
  const lookOf = (id: string) => all.find((r) => r.project.id === id)?.look
  const validName = $derived(/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name))

  async function create() {
    busy = true
    error = ''
    try {
      const r = await api.newProject({ name, node_id: nodeId || undefined, layout, owner: owner.trim() || undefined, private: isPrivate })
      creating = false
      name = owner = ''
      await loadOverview()
      ui.chatOpen = true
      go({ view: 'project', id: r.project.id })
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }
</script>

{#if editor}<ProjectEditor preset={editor} onClose={() => (editor = null)} />{/if}

<div class="page">
  <header class="head">
    <h1>项目</h1>
    <span class="sp"></span>
    <button class="btn small" onclick={() => (creating = !creating)}><Icon name="plus" size={15} />新建项目</button>
    <button class="btn small ghost" onclick={() => (editor = { name: '', repo_url: '' })}>登记已有仓库</button>
  </header>

  {#if creating}
    <form class="new" onsubmit={(e) => { e.preventDefault(); create() }}>
      <p class="hint">在一台开发机的工作空间里创建。默认使用 <a href="https://github.com/XiaoXianThis/wokspace" target="_blank" rel="noreferrer">wokspace</a> 规范。</p>
      <div class="grid">
        <label>项目名<input bind:value={name} placeholder="my-app" aria-invalid={!!name && !validName} /></label>
        <label>机器
          <select bind:value={nodeId}>
            <option value="">自动选一台</option>
            {#each devs as n}<option value={n.id}>{n.name}{n.wok ? '' : '（没装 wok）'}</option>{/each}
          </select>
        </label>
        <label>结构
          <select bind:value={layout}>
            <option value="wokspace">wokspace</option>
            <option value="plain">只 git init</option>
          </select>
        </label>
        <label>GitHub 用户或组织（可选）<input bind:value={owner} placeholder="不填就不建远端仓库" /></label>
      </div>
      {#if owner.trim()}<label class="check"><input type="checkbox" bind:checked={isPrivate} />私有仓库</label>{/if}
      {#if name && !validName}<p class="err">只能用小写字母、数字和连字符，比如 my-app</p>{/if}
      {#if devs.length === 0}<p class="err">没有在线并设置了工作空间的开发机：先在“设置 → 机器”里给一台机器设置工作空间。</p>{/if}
      {#if error}<p class="err" role="alert">{error}</p>{/if}
      <div><button class="btn small" type="submit" disabled={busy || !validName}>创建</button></div>
    </form>
  {/if}

  <div class="grid-cards">
    {#each projects as p (p.id)}
      {@const l = lookOf(p.id)}
      <button class="card" onclick={() => go({ view: 'project', id: p.id })}>
        <span class="ct">
          {#if l}<span class="tone-{looks[l].tone}"><Icon name={looks[l].icon} size={14} /></span>{/if}
          <strong>{p.name}</strong>
          {#if p.layout === 'wokspace'}<span class="tag" title="wokspace"><Icon name="wok" size={13} /></span>{/if}
        </span>
        <span class="cm">
          {#each p.nodes as pn (pn.node_id)}
            {@const n = data?.nodes.find((x) => x.id === pn.node_id)}
            <span class="chip" class:off={!n?.online}><Icon name={n?.role === 'server' ? 'server' : 'laptop'} size={12} />{n?.name ?? '?'}</span>
          {/each}
          {#if p.deploy}<span class="chip"><Icon name="rocket" size={12} />{nodeName(p.deploy.node_id)}</span>{/if}
          {#if !p.nodes.length && !p.deploy}<span class="muted">不在任何机器上</span>{/if}
        </span>
      </button>
    {:else}
      <p class="muted">还没有项目。</p>
    {/each}
  </div>

  {#if data?.discovered.length}
    <section>
      <h2><Icon name="branch" size={14} />工作空间里发现的仓库</h2>
      <ul class="found">
        {#each data.discovered as r (r.node_id + r.path)}
          <li>
            <span class="fn"><strong>{repoName(r)}</strong>{#if r.wokspace}<span class="tag" title="wokspace"><Icon name="wok" size={12} /></span>{/if}</span>
            <span class="muted path" title={r.path}><Icon name="laptop" size={12} />{nodeName(r.node_id)} · {r.path}</span>
            <span class="muted">{r.remote || '没有远端'}</span>
            <button class="link accent" onclick={() => (editor = { name: repoName(r), repo_url: r.remote, node_id: r.node_id, path: r.path })}>登记为项目</button>
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</div>

<style>
  .page { padding: 18px 22px 28px; display: grid; gap: 14px; align-content: start; max-width: 1100px; }
  .head { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  h1 { margin: 0; font-size: 20px; }
  .sp { flex: 1; }
  .btn.small { padding: 6px 14px; font-size: 14px; display: inline-flex; gap: 4px; align-items: center; }
  .new { border: 1px dashed var(--accent); border-radius: 14px; padding: 12px 14px; display: grid; gap: 10px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 10px; }
  label { display: grid; gap: 4px; font-size: 13px; color: var(--muted); }
  label.check { display: flex; gap: 6px; align-items: center; color: var(--text); }
  input:not([type='checkbox']), select { border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; color: var(--text); min-width: 0; }
  .hint { margin: 0; font-size: 13px; color: var(--muted); }
  .hint a { color: var(--accent); }
  .grid-cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(230px, 1fr)); gap: 10px; }
  .card { display: grid; gap: 8px; text-align: left; border: 1px solid var(--line); background: var(--panel); border-radius: 14px; padding: 12px; }
  .card:hover { border-color: var(--accent); }
  .ct { display: flex; gap: 6px; align-items: center; }
  .cm { display: flex; flex-wrap: wrap; gap: 5px; }
  .chip { display: inline-flex; align-items: center; gap: 4px; border: 1px solid var(--line); border-radius: 999px; padding: 1px 8px; font-size: 12px; color: var(--muted); }
  .chip.off { opacity: .55; }
  .tag { color: var(--muted); display: inline-grid; }
  h2 { font-size: 14px; margin: 6px 0 8px; display: flex; gap: 6px; align-items: center; color: var(--muted); }
  .found { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
  .found li { display: grid; grid-template-columns: minmax(100px, 1fr) minmax(0, 2fr) minmax(0, 2fr) auto; gap: 10px; align-items: center; border: 1px solid var(--line); border-radius: 12px; padding: 8px 12px; background: var(--panel); }
  .fn { display: flex; gap: 6px; align-items: center; }
  .path { display: inline-flex; gap: 4px; align-items: center; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .muted { color: var(--muted); font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .link { border: 0; background: none; padding: 0; font-size: 13px; }
  .link.accent { color: var(--accent); font-weight: 600; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) {
    .page { padding: 14px 16px 24px; }
    .found li { grid-template-columns: 1fr; gap: 2px; }
  }
</style>
