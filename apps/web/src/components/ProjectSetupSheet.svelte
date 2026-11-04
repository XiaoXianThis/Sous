<script lang="ts">
  // Creating a project on a machine, or registering a repository that already
  // exists (including the ones the machines discovered).
  import { api } from '../lib/api'
  import { go, loadOverview, ui, wb } from '../lib/app.svelte'
  import type { RepoScan } from '../lib/types'
  import Icon from './Icon.svelte'
  import ProjectEditor from './ProjectEditor.svelte'
  import Sheet from './Sheet.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let name = $state('')
  let nodeId = $state('')
  let layout = $state<'wokspace' | 'plain'>('wokspace')
  let owner = $state('')
  let isPrivate = $state(true)
  let error = $state('')
  let busy = $state(false)
  let editor = $state<null | { name: string; repo_url: string; node_id?: string; path?: string }>(null)

  const devs = $derived((wb.data?.nodes ?? []).filter((n) => n.role !== 'server' && n.online && (n.workspaces?.length ?? 0) > 0))
  const discovered = $derived(wb.data?.discovered ?? [])
  const validName = $derived(/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name))
  const nodeName = (id: string) => wb.data?.nodes.find((n) => n.id === id)?.name ?? '未知机器'
  const repoName = (r: RepoScan) => r.path.split(/[\\/]/).pop() ?? r.path

  async function create() {
    if (!validName) return
    busy = true
    error = ''
    try {
      const r = await api.newProject({
        name, node_id: nodeId || undefined, layout, owner: owner.trim() || undefined, private: isPrivate,
      })
      await loadOverview()
      ui.chatOpen = true
      onClose()
      go({ view: 'project', id: r.project.id })
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }
</script>

<Sheet title="新建或登记项目" wide {onClose}>
  <section>
    <h3>新建项目</h3>
    <p class="hint">在一台开发机的工作空间里创建。默认使用 <a href="https://github.com/XiaoXianThis/wokspace" target="_blank" rel="noreferrer">wokspace</a> 规范。</p>
    <form onsubmit={(e) => { e.preventDefault(); create() }}>
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
  </section>

  <section>
    <h3>登记已有仓库</h3>
    <p class="hint">项目已经在这台机器上时，登记它的远端地址或本机路径即可，不需要新建。</p>
    <button class="btn small ghost" onclick={() => (editor = { name: '', repo_url: '' })}>手动登记</button>
    {#if discovered.length}
      <div class="found">
        {#each discovered as r (r.node_id + r.path)}
          <div class="row">
            <span class="fn"><strong>{repoName(r)}</strong>{#if r.wokspace}<span class="tag" title="wokspace"><Icon name="wok" size={12} /></span>{/if}</span>
            <span class="sub" title={r.path}><Icon name="laptop" size={12} />{nodeName(r.node_id)} · {r.path}</span>
            <span class="sub">{r.remote || '没有远端'}</span>
            <button class="link accent" onclick={() => (editor = { name: repoName(r), repo_url: r.remote, node_id: r.node_id, path: r.path })}>登记</button>
          </div>
        {/each}
      </div>
    {:else}
      <p class="muted">机器上还没有发现未登记的仓库。</p>
    {/if}
  </section>
</Sheet>

{#if editor}<ProjectEditor preset={editor} onClose={() => (editor = null)} />{/if}

<style>
  section { display: grid; gap: 8px; }
  h3 { margin: 4px 0 0; font-size: 15px; }
  .hint { margin: 0; font-size: 13px; color: var(--muted); }
  .hint a { color: var(--accent); }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 10px; }
  label { display: grid; gap: 4px; font-size: 13px; color: var(--muted); }
  label.check { display: flex; gap: 6px; align-items: center; color: var(--text); }
  input:not([type='checkbox']), select {
    border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; color: var(--text); min-width: 0;
  }
  .btn.small { padding: 6px 14px; font-size: 14px; justify-self: start; }
  .found { display: grid; gap: 6px; }
  .row {
    display: grid; grid-template-columns: minmax(90px, 1fr) minmax(0, 1.6fr) minmax(0, 1.6fr) auto;
    gap: 10px; align-items: center; border: 1px solid var(--line); border-radius: 10px; padding: 7px 10px; background: var(--panel);
  }
  .fn { display: flex; gap: 5px; align-items: center; min-width: 0; }
  .tag { color: var(--muted); display: inline-grid; }
  .sub { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: inline-flex; gap: 4px; align-items: center; }
  .link { border: 0; background: none; padding: 0; font-size: 13px; color: var(--muted); }
  .link.accent { color: var(--accent); font-weight: 600; }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) {
    .row { grid-template-columns: 1fr; gap: 2px; }
  }
</style>
