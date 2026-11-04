<script lang="ts">
  // One project: where it lives, where it deploys, and its tasks as a board.
  import { api } from '../lib/api'
  import { chat, go, ui, wb } from '../lib/app.svelte'
  import { avatarOf, looks, stages, taskLook } from '../lib/status'
  import { board, columns } from '../lib/tasks'
  import { deployedAfter, personaFor } from '../lib/workbench'
  import type { Node, Task } from '../lib/types'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import ProjectEditor from './ProjectEditor.svelte'
  import StageBar from './StageBar.svelte'
  import TaskDetail from './TaskDetail.svelte'

  let { id }: { id: string } = $props()

  let tasks = $state<Task[]>([])
  let error = $state('')
  let editing = $state(false)
  let creating = $state(false)
  let cloneTo = $state('')
  let busy = $state(false)

  const data = $derived(wb.data)
  const project = $derived(data?.projects.find((p) => p.id === id))
  const nodeOf = (nid: string): Node | undefined => data?.nodes.find((n) => n.id === nid)
  const cols = $derived(board(tasks))
  const clonable = $derived(
    (data?.nodes ?? []).filter((n) => n.role !== 'server' && n.online && (n.workspaces?.length ?? 0) > 0 && !project?.nodes.some((p) => p.node_id === n.id)),
  )

  async function load() {
    try {
      tasks = (await api.tasks()).tasks.filter((t) => (t.project_id || 'misc') === id)
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }
  $effect(() => {
    void chat.tasksRev
    void id
    load()
  })

  async function deploy() {
    if (!project?.deploy || !confirm(`在服务器上部署“${project.name}”？\n\n会建一个运维任务，按部署命令执行；重启服务之类的操作会请你批准。`)) return
    busy = true
    try {
      const ops = data?.personas.find((p) => p.kinds.includes('ops'))
      const t = await api.createTask({ kind: 'ops', project_id: id, title: `部署 ${project.name}`, persona_id: ops?.id ?? '',
        detail: `把项目「${project.name}」部署到服务器：先拉取最新代码，再执行部署命令，最后做健康检查。`, dispatch: true })
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
    try {
      await api.cloneProject(id, cloneTo)
      cloneTo = ''
      ui.chatOpen = true
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  const visible = (k: string) => k !== 'closed' || cols.closed.length > 0
</script>

{#if editing && project}<ProjectEditor {project} onClose={() => (editing = false)} />{/if}
{#if creating}
  <TaskDetail id="new" projectId={id} onClose={() => (creating = false)} onOpenTask={(tid) => { creating = false; go({ view: 'task', id: tid }) }} onOpenThread={() => {}} />
{/if}

<div class="page">
  {#if !project}
    {#if data}<p class="muted">这个项目不存在了。</p>{/if}
  {:else}
    <header class="head">
      <button class="icon-btn" aria-label="返回总览" onclick={() => go({ view: 'overview', id: '' })}><Icon name="back" /></button>
      <h1>{project.name}</h1>
      {#if project.layout === 'wokspace'}<span class="tag" title="wokspace 结构"><Icon name="wok" size={14} />wokspace</span>{/if}
      {#if project.repo_url}<a class="repo" href={project.repo_url.startsWith('http') ? project.repo_url : undefined} target="_blank" rel="noreferrer" title={project.repo_url}><Icon name="link" size={14} /></a>{/if}
      <span class="sp"></span>
      {#if project.deploy}<button class="btn small ghost" disabled={busy} onclick={deploy}><Icon name="rocket" size={15} />部署</button>{/if}
      <button class="btn small" onclick={() => (creating = true)}><Icon name="plus" size={15} />任务</button>
      {#if project.id !== 'misc'}<button class="btn small ghost" onclick={() => (editing = true)}>设置</button>{/if}
    </header>

    {#if project.id !== 'misc'}
      <div class="where" aria-label="项目所在的机器">
        {#each project.nodes as pn (pn.node_id)}
          {@const n = nodeOf(pn.node_id)}
          <span class="place" class:off={!n?.online} title={pn.path}>
            <Icon name={n?.role === 'server' ? 'server' : 'laptop'} size={14} />{n?.name ?? '未知机器'}
            {#if pn.source === 'scan'}<span class="scan" title="在工作空间里自动找到"><Icon name="refresh" size={11} /></span>{/if}
          </span>
        {/each}
        {#if project.deploy}
          {@const d = nodeOf(project.deploy.node_id)}
          <span class="place deploy" title="{project.deploy.path} · {project.deploy.command}">
            <Icon name="rocket" size={14} />{d?.name ?? '服务器'}{#if project.deploy.auto_on_merge}<span class="auto" title="PR 合并后自动部署">auto</span>{/if}
          </span>
        {/if}
        {#if project.nodes.length === 0}<span class="muted">还不在任何机器上</span>{/if}
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
      {#if project.notes}<p class="notes">{project.notes}</p>{/if}
    {/if}

    {#if error}<p class="err">{error}</p>{/if}

    <div class="board">
      {#each columns.filter((c) => visible(c.key)) as col (col.key)}
        <section class="col" aria-label={col.title}>
          <h2>{col.title}<span class="n">{cols[col.key].length}</span></h2>
          {#each cols[col.key] as t (t.id)}
            {@const l = taskLook(t)}
            {@const a = avatarOf(data ? personaFor(data, t) : undefined, t.agent_pref)}
            <button class="card" onclick={() => go({ view: 'task', id: t.id })}>
              <span class="row1">
                <Avatar glyph={a.glyph} color={a.color} title={a.title} size={22} />
                <span class="title">{t.title}</span>
                <span class="tone-{looks[l].tone}" title={looks[l].text}><Icon name={looks[l].icon} size={14} /></span>
              </span>
              <StageBar stages={stages(t, { hasDeploy: !!project.deploy, deployed: t.status === 'done' && deployedAfter(tasks, id, t.updated_at) })} />
              {#if t.kind !== 'code'}<span class="kind">{t.kind === 'ops' ? '运维' : '一般'}</span>{/if}
            </button>
          {:else}
            <p class="none">—</p>
          {/each}
        </section>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page { padding: 16px 22px 28px; display: grid; gap: 12px; align-content: start; }
  .head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  h1 { margin: 0; font-size: 20px; }
  .tag { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: var(--muted); border: 1px solid var(--line); border-radius: 999px; padding: 1px 8px; }
  .repo { color: var(--muted); display: inline-grid; }
  .sp { flex: 1; }
  .btn.small { padding: 6px 14px; font-size: 14px; display: inline-flex; align-items: center; gap: 4px; }
  .where { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  .place {
    display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--line); background: var(--panel);
    border-radius: 999px; padding: 2px 10px 2px 8px; font-size: 13px;
  }
  .place.off { opacity: .55; }
  .place.deploy { border-style: dashed; }
  .scan { color: var(--muted); display: inline-grid; }
  .auto { font-size: 10px; color: var(--ok); border: 1px solid var(--ok); border-radius: 6px; padding: 0 4px; }
  .clone select { border: 1px dashed var(--line); background: transparent; border-radius: 999px; padding: 2px 10px; font-size: 13px; color: var(--muted); }
  .clone { display: inline-flex; gap: 8px; align-items: center; }
  .notes { margin: 0; color: var(--muted); font-size: 13px; max-width: 760px; }
  .board { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 12px; align-items: start; }
  .col { background: var(--sunken); border-radius: 14px; padding: 10px; display: grid; gap: 8px; }
  h2 { margin: 0 0 2px 4px; font-size: 13px; color: var(--muted); display: flex; gap: 6px; align-items: baseline; }
  .n { font-weight: 400; }
  .card {
    display: grid; gap: 8px; text-align: left; border: 1px solid var(--line); background: var(--panel);
    border-radius: 12px; padding: 10px 12px;
  }
  .card:hover { border-color: var(--accent); }
  .row1 { display: flex; align-items: center; gap: 8px; }
  .title { flex: 1; font-size: 14px; overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .kind { font-size: 11px; color: var(--muted); justify-self: start; border: 1px solid var(--line); border-radius: 6px; padding: 0 5px; }
  .none { margin: 0 4px; color: var(--line); }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
  .link { border: 0; background: none; padding: 0; font-size: 13px; color: var(--muted); }
  .link.accent { color: var(--accent); font-weight: 600; }
  @media (max-width: 760px) {
    .page { padding: 12px 14px 24px; }
  }
</style>
