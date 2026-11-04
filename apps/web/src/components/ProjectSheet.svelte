<script lang="ts">
  // One project in a sheet: where it lives, every task, and the actions that
  // used to be on the project page (create, deploy, clone, edit).
  import { api } from '../lib/api'
  import { go, loadOverview, ui, wb } from '../lib/app.svelte'
  import { projectCards } from '../lib/workbench'
  import AgentCard from './AgentCard.svelte'
  import ApprovalCard from './ApprovalCard.svelte'
  import Icon from './Icon.svelte'
  import Locations from './Locations.svelte'
  import ProjectEditor from './ProjectEditor.svelte'
  import Sheet from './Sheet.svelte'
  import TaskRow from './TaskRow.svelte'

  let { id, onClose }: { id: string; onClose: () => void } = $props()

  let editing = $state(false)
  let cloneTo = $state('')
  let busy = $state(false)
  let error = $state('')

  const project = $derived(wb.data?.projects.find((p) => p.id === id))
  const card = $derived(wb.data ? projectCards(wb.data, wb.inbox?.tasks ?? []).find((c) => c.project.id === id) : undefined)
  const clonable = $derived(
    (wb.data?.nodes ?? []).filter(
      (n) => n.role !== 'server' && n.online && (n.workspaces?.length ?? 0) > 0 && !project?.nodes.some((p) => p.node_id === n.id),
    ),
  )
  const runIds = $derived(new Set(card?.allAgents.map((a) => a.run.id) ?? []))
  const approvals = $derived((wb.inbox?.approvals ?? []).filter((a) => a.review === 'user' && runIds.has(a.run_id)))
  const titleOf = (runId: string) => card?.allAgents.find((a) => a.run.id === runId)?.title ?? ''

  async function deploy() {
    if (!project?.deploy) return
    if (!confirm(`在服务器上部署“${project.name}”？\n\n会建一个运维任务，按部署命令执行；重启服务之类的操作会请你批准。`)) return
    busy = true
    error = ''
    try {
      const ops = wb.data?.personas.find((p) => p.kinds.includes('ops'))
      const t = await api.createTask({
        kind: 'ops', project_id: id, title: `部署 ${project.name}`, persona_id: ops?.id ?? '',
        detail: `把项目「${project.name}」部署到服务器：先拉取最新代码，再执行部署命令，最后做健康检查。`, dispatch: true,
      })
      go({ view: 'task', id: t.id })
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function clone() {
    if (!cloneTo) return
    busy = true
    error = ''
    try {
      await api.cloneProject(id, cloneTo)
      cloneTo = ''
      await loadOverview()
      ui.chatOpen = true
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }
</script>

<Sheet title={project ? project.name : '项目'} wide {onClose}>
  {#if !wb.data}
    <p class="muted">加载中…</p>
  {:else if !project}
    <p class="muted">这个项目不存在了。</p>
  {:else}
    <Locations locations={card?.locations ?? []} />
    {#if project.notes}<p class="notes">{project.notes}</p>{/if}
    {#if project.repo_url}
      <p class="repo"><Icon name="link" size={13} /><a href={project.repo_url} target="_blank" rel="noreferrer">{project.repo_url}</a></p>
    {/if}

    <div class="actions">
      <button class="btn small" onclick={() => (ui.newTask = project.id)}>新建任务</button>
      {#if project.deploy}<button class="btn small ghost" disabled={busy} onclick={deploy}>部署</button>{/if}
      <button class="btn small ghost" disabled={busy} onclick={() => (editing = true)}>编辑项目</button>
      {#if clonable.length}
        <span class="clone">
          <select bind:value={cloneTo} aria-label="clone 到">
            <option value="">clone 到…</option>
            {#each clonable as n}<option value={n.id}>{n.name}</option>{/each}
          </select>
          {#if cloneTo}<button class="link accent" disabled={busy} onclick={clone}>开始</button>{/if}
        </span>
      {/if}
    </div>

    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <p class="hint">sous 不会合并 PR；PR 合并后才部署。</p>

    {#if approvals.length}
      <section aria-label="需要你处理">
        <h3>需要你处理</h3>
        {#each approvals as a (a.id)}
          <ApprovalCard approval={a} context={titleOf(a.run_id)} />
        {/each}
      </section>
    {/if}

    {#if card?.allAgents.length}
      <section aria-label="正在工作">
        <h3>正在工作</h3>
        {#each card.allAgents as a (a.run.id)}
          <AgentCard agent={a} onOpen={() => (ui.runSheet = a.run.id)} />
        {/each}
      </section>
    {/if}

    <section aria-label="任务">
      <h3>任务</h3>
      {#each card?.allTasks ?? [] as t (t.task.id)}
        <TaskRow task={t} onOpen={() => go({ view: 'task', id: t.task.id })} />
      {:else}
        <p class="muted">还没有任务。</p>
      {/each}
    </section>
  {/if}
</Sheet>

{#if editing && project}<ProjectEditor {project} onClose={() => (editing = false)} />{/if}

<style>
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .notes { margin: 0; font-size: 13px; color: var(--muted); overflow-wrap: anywhere; }
  .repo { margin: 0; display: flex; gap: 5px; align-items: center; font-size: 12px; overflow-wrap: anywhere; }
  .repo a { color: var(--accent); }
  .actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .btn.small { padding: 6px 14px; font-size: 14px; }
  .clone { display: inline-flex; gap: 8px; align-items: center; }
  .clone select { border: 1px dashed var(--line); background: transparent; border-radius: 999px; padding: 3px 10px; font-size: 13px; color: var(--muted); }
  .hint { margin: 0; font-size: 12px; color: var(--muted); }
  .link { border: 0; background: none; padding: 0; font-size: 13px; color: var(--muted); }
  .link.accent { color: var(--accent); font-weight: 600; }
  section { display: grid; gap: 4px; }
  h3 { margin: 6px 0 2px; font-size: 14px; color: var(--muted); }
  .err { margin: 0; color: var(--danger); font-size: 13px; }
</style>
