<script lang="ts">
  // One task: its pipeline, every run with all its steps, and the task's
  // form and actions. The agent's step-by-step work is shown here, not in
  // the chat.
  import { api } from '../lib/api'
  import { chat, go, openThread, ui, wb } from '../lib/app.svelte'
  import { runFromHistory } from '../lib/runs'
  import { avatarOf, kindText, looks, stages, taskLook } from '../lib/status'
  import { deployedAfter, personaFor } from '../lib/workbench'
  import type { Run, Task } from '../lib/types'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import RunCard from './RunCard.svelte'
  import StageBar from './StageBar.svelte'
  import TaskDetail from './TaskDetail.svelte'

  let { id }: { id: string } = $props()

  let task = $state<Task | null>(null)
  let runs = $state<Run[]>([])
  let missing = $state(false)
  let error = $state('')
  let showForm = $state(false)
  let note = $state('')

  async function load() {
    try {
      const d = await api.task(id)
      task = d.task
      runs = d.runs
      missing = false
      const need = d.runs.filter((r) => !chat.runs[r.id])
      const details = await Promise.all(need.map((r) => api.run(r.id)))
      for (const x of details) {
        if (!chat.runs[x.run.id]) chat.runs[x.run.id] = runFromHistory(x.run, x.events, x.approvals)
      }
      error = ''
    } catch (e) {
      if ((e as { status?: number }).status === 404) missing = true
      else error = (e as Error).message
    }
  }
  $effect(() => {
    void chat.tasksRev
    void id
    load()
  })

  const data = $derived(wb.data)
  const project = $derived(data?.projects.find((p) => p.id === task?.project_id))
  const persona = $derived(data && task ? personaFor(data, task) : undefined)
  const look = $derived(task ? taskLook(task) : 'planned')
  const views = $derived(runs.map((r) => chat.runs[r.id]).filter(Boolean).reverse())
  const active = $derived(views.find((v) => v.run.status === 'running'))
  const nodeName = (nid: string) => data?.nodes.find((n) => n.id === nid)?.name ?? '未知机器'
  const a = $derived(avatarOf(persona, task?.agent_pref))

  async function sendNote() {
    if (!note.trim() || !active) return
    try {
      await api.messageRun(active.run.id, note.trim())
      note = ''
    } catch (e) {
      error = (e as Error).message
    }
  }
</script>

<div class="page">
  {#if missing}
    <p class="muted">这个任务不存在，可能链接已经过期。</p>
  {:else if task}
    <header class="head">
      <button class="icon-btn" aria-label="返回项目" onclick={() => go(project ? { view: 'project', id: project.id } : { view: 'overview', id: '' })}><Icon name="back" /></button>
      <Avatar glyph={a.glyph} color={a.color} title={a.title} size={34} ring={looks[look].tone === 'neutral' ? '' : looks[look].tone} />
      <div class="ht">
        <h1>{task.title}</h1>
        <p class="sub">
          <span class="tone-{looks[look].tone} st"><Icon name={looks[look].icon} size={13} />{looks[look].text}</span>
          <span>{a.title}</span>
          {#if project}<button class="plink" onclick={() => go({ view: 'project', id: project.id })}>{project.name}</button>{/if}
          <span class="kind">{kindText[task.kind]}</span>
        </p>
      </div>
    </header>

    <StageBar stages={stages(task, { hasDeploy: !!project?.deploy, deployed: task.status === 'done' && !!data && deployedAfter(data.tasks, task.project_id, task.updated_at) })} labels />
    {#if task.status_reason}<p class="reason">{task.status_reason}</p>{/if}
    {#if task.pr_url}<p class="pr"><Icon name="branch" size={14} /><a href={task.pr_url} target="_blank" rel="noreferrer">{task.pr_url}</a><span class="muted">sous 不会合并，请你评审后合并</span></p>{/if}

    {#if active}
      <form class="note" onsubmit={(e) => { e.preventDefault(); sendNote() }}>
        <input bind:value={note} placeholder="给正在干活的 {a.title} 补充一句…" aria-label="追加指令" />
        <button class="icon-btn" type="submit" aria-label="发送" disabled={!note.trim()}><Icon name="send" /></button>
      </form>
    {/if}

    {#if error}<p class="err">{error}</p>{/if}

    <section class="runs" aria-label="执行记录">
      {#each views as v (v.run.id)}
        <RunCard view={v} {nodeName} full />
      {:else}
        <p class="muted">还没有执行过。</p>
      {/each}
    </section>

    <section class="detail">
      <button class="toggle" onclick={() => (showForm = !showForm)} aria-expanded={showForm}>
        <Icon name="chevron" size={14} />任务说明和设置
      </button>
      {#if showForm}
        <div class="embed">
          <TaskDetail {id} compact onClose={() => (showForm = false)} onOpenTask={(tid) => go({ view: 'task', id: tid })}
            onOpenThread={(tid) => { openThread(tid); ui.chatOpen = true }} />
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .page { padding: 16px 22px 28px; display: grid; gap: 12px; align-content: start; max-width: 900px; }
  .head { display: flex; align-items: center; gap: 10px; }
  .ht { min-width: 0; }
  h1 { margin: 0; font-size: 19px; line-height: 1.3; }
  .sub { margin: 2px 0 0; display: flex; flex-wrap: wrap; gap: 4px 12px; font-size: 13px; color: var(--muted); align-items: center; }
  .st { display: inline-flex; align-items: center; gap: 4px; font-weight: 600; }
  .plink { border: 0; background: none; padding: 0; color: var(--accent); font-size: 13px; }
  .kind { border: 1px solid var(--line); border-radius: 6px; padding: 0 5px; font-size: 11px; }
  .reason { margin: 0; color: var(--muted); font-size: 13px; overflow-wrap: anywhere; }
  .pr { margin: 0; display: flex; gap: 6px; align-items: center; flex-wrap: wrap; font-size: 13px; overflow-wrap: anywhere; }
  .pr a { color: var(--accent); }
  .note { display: flex; gap: 6px; border: 1px solid var(--line); background: var(--panel); border-radius: 999px; padding: 3px 4px 3px 14px; }
  .note input { flex: 1; border: 0; outline: 0; background: transparent; }
  .runs { display: grid; gap: 10px; }
  .detail { display: grid; gap: 8px; }
  .toggle { justify-self: start; display: inline-flex; gap: 4px; align-items: center; border: 0; background: none; color: var(--muted); font-size: 13px; padding: 0; }
  .toggle[aria-expanded='false'] :global(svg) { transform: rotate(-90deg); }
  .embed { border: 1px solid var(--line); border-radius: 14px; background: var(--panel); padding: 12px 14px; }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) { .page { padding: 12px 14px 24px; } }
</style>
