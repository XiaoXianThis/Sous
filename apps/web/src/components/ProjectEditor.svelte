<script lang="ts">
  // Registers or changes a project: its repository, notes, default persona,
  // where it lives on each machine, and where it deploys.
  import { api } from '../lib/api'
  import { go, loadOverview, wb } from '../lib/app.svelte'
  import { modeText } from '../lib/tasks'
  import type { Deploy, Project, ProjectNode } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { project = null, preset = null, onClose }: {
    project?: Project | null
    /** A new project from a discovered repository. */
    preset?: { name: string; repo_url: string; node_id?: string; path?: string } | null
    onClose: () => void
  } = $props()

  const isNew = $derived(!project)
  // svelte-ignore state_referenced_locally
  let name = $state(project?.name ?? preset?.name ?? '')
  // svelte-ignore state_referenced_locally
  let repo = $state(project?.repo_url ?? preset?.repo_url ?? '')
  // svelte-ignore state_referenced_locally
  let notes = $state(project?.notes ?? '')
  // svelte-ignore state_referenced_locally
  let persona = $state(project?.default_persona ?? '')
  // svelte-ignore state_referenced_locally
  let mode = $state<string>(project?.default_mode ?? '')
  // svelte-ignore state_referenced_locally
  let places = $state<ProjectNode[]>(
    project ? project.nodes.map((n) => ({ ...n })) : preset?.node_id && preset.path && !preset.repo_url ? [{ node_id: preset.node_id, path: preset.path, priority: 0 }] : [],
  )
  // svelte-ignore state_referenced_locally
  let deploy = $state<Deploy>(project?.deploy ? { ...project.deploy } : { node_id: '', path: '', command: '', health: '', auto_on_merge: false })
  let error = $state('')
  let busy = $state(false)

  const nodes = $derived(wb.data?.nodes ?? [])
  const personas = $derived(wb.data?.personas ?? [])
  const servers = $derived(nodes.filter((n) => n.role === 'server'))

  function addPlace() {
    const free = nodes.find((n) => !places.some((p) => p.node_id === n.id))
    if (free) places.push({ node_id: free.id, path: '', priority: places.length, source: 'manual' })
  }

  async function save() {
    busy = true
    error = ''
    try {
      const body: Partial<Project> = {
        name, repo_url: repo, notes, default_persona: persona, default_mode: mode as Project['default_mode'],
        nodes: places.filter((p) => p.path.trim()),
        deploy: deploy.node_id ? deploy : null,
      }
      const p = isNew ? await api.createProject(body) : await api.updateProject(project!.id, body)
      await loadOverview()
      onClose()
      if (isNew) go({ view: 'project', id: p.id })
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function remove() {
    if (!project || !confirm(`删除项目“${project.name}”？机器上的代码不会被删除。`)) return
    try {
      await api.deleteProject(project.id)
      await loadOverview()
      onClose()
      go({ view: 'projects', id: '' })
    } catch (e) {
      error = (e as Error).message
    }
  }
</script>

<Sheet title={isNew ? '登记项目' : `项目设置 · ${project?.name}`} {onClose} wide>
  <form class="form" onsubmit={(e) => { e.preventDefault(); save() }}>
    <div class="grid">
      <label>名字<input bind:value={name} required /></label>
      <label>仓库地址<input bind:value={repo} placeholder="https://github.com/you/app" /></label>
    </div>
    <p class="hint">同一个仓库在各台机器的工作空间里会被自动找到，不用手填路径。</p>
    <label>项目说明（会写进 agent 的提示）<textarea bind:value={notes} rows="2"></textarea></label>
    <div class="grid">
      <label>默认岗位
        <select bind:value={persona}>
          <option value="">不指定</option>
          {#each personas as p}<option value={p.id}>{p.glyph} {p.name}</option>{/each}
        </select>
      </label>
      <label>权限模式
        <select bind:value={mode}>
          <option value="">按全局默认</option>
          {#each Object.entries(modeText) as [k, v]}<option value={k}>{v}</option>{/each}
        </select>
      </label>
    </div>

    <fieldset>
      <legend>在哪些机器上（越靠前越优先）</legend>
      {#each places as p, i (i)}
        <div class="place">
          <select bind:value={p.node_id} aria-label="机器">
            {#each nodes as n}<option value={n.id}>{n.name}{n.role === 'server' ? '（服务器）' : ''}</option>{/each}
          </select>
          <input bind:value={p.path} placeholder="仓库在这台机器上的路径" aria-label="路径" />
          {#if p.source === 'scan'}<span class="scan" title="在工作空间里自动找到">自动</span>{/if}
          <button type="button" class="link" onclick={() => places.splice(i, 1)}>移除</button>
        </div>
      {/each}
      {#if nodes.length > places.length}<button type="button" class="link accent" onclick={addPlace}>添加机器</button>{/if}
    </fieldset>

    <fieldset>
      <legend>部署目标</legend>
      {#if servers.length === 0}
        <p class="hint">还没有服务器。在“设置 → 机器”里把一台机器设为服务器后，就能在这里选它部署。</p>
      {:else}
        <div class="grid">
          <label>服务器
            <select bind:value={deploy.node_id}>
              <option value="">不部署</option>
              {#each servers as n}<option value={n.id}>{n.name}</option>{/each}
            </select>
          </label>
          {#if deploy.node_id}<label>目录<input bind:value={deploy.path} placeholder="/srv/app" /></label>{/if}
        </div>
        {#if deploy.node_id}
          <div class="grid">
            <label>部署命令<input bind:value={deploy.command} placeholder="./deploy.sh" /></label>
            <label>健康检查（可选）<input bind:value={deploy.health} placeholder="curl -fsS localhost:8080/health" /></label>
          </div>
          <label class="check"><input type="checkbox" bind:checked={deploy.auto_on_merge} />sous 开的 PR 合并后自动部署</label>
        {/if}
      {/if}
    </fieldset>

    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <div class="actions">
      <button class="btn small" type="submit" disabled={busy || !name.trim()}>保存</button>
      {#if !isNew && project?.id !== 'misc'}<button type="button" class="link danger" onclick={remove}>删除项目</button>{/if}
    </div>
  </form>
</Sheet>

<style>
  .form { display: grid; gap: 10px; }
  label { display: grid; gap: 4px; font-size: 13px; color: var(--muted); }
  label.check { display: flex; gap: 6px; align-items: center; color: var(--text); }
  input:not([type='checkbox']), textarea, select { border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; color: var(--text); min-width: 0; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 10px; }
  fieldset { border: 1px solid var(--line); border-radius: 12px; padding: 8px 12px 12px; display: grid; gap: 8px; }
  legend { font-size: 13px; color: var(--muted); padding: 0 4px; }
  .place { display: flex; gap: 6px; align-items: center; }
  .place select { flex: none; width: 140px; }
  .place input { flex: 1; }
  .scan { font-size: 11px; color: var(--muted); border: 1px solid var(--line); border-radius: 6px; padding: 0 4px; }
  .hint { margin: 0; font-size: 12px; color: var(--muted); }
  .actions { display: flex; gap: 14px; align-items: center; }
  .btn.small { padding: 6px 16px; font-size: 14px; }
  .link { border: 0; background: none; padding: 0; font-size: 13px; color: var(--muted); justify-self: start; }
  .link.accent { color: var(--accent); font-weight: 600; }
  .link.danger:hover { color: var(--danger); }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
</style>
