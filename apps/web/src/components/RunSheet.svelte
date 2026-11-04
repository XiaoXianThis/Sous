<script lang="ts">
  // One Run in detail: its stored steps, the live activity, approvals and the
  // controls that used to live on the task page's Run cards.
  import { api } from '../lib/api'
  import { chat, go, ui, wb } from '../lib/app.svelte'
  import { isActive, runFromHistory, type RunView } from '../lib/runs'
  import { avatarOf } from '../lib/status'
  import { personaFor, runMeta } from '../lib/workbench'
  import type { Approval, HubEvent, Run } from '../lib/types'
  import ApprovalCard from './ApprovalCard.svelte'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'
  import Sheet from './Sheet.svelte'
  import TextViewer from './TextViewer.svelte'

  let { id, onClose }: { id: string; onClose: () => void } = $props()

  let detail = $state<{ run: Run; events: HubEvent[]; approvals: Approval[] } | null>(null)
  let view = $state<RunView | null>(null)
  let error = $state('')
  let busy = $state('')
  let viewer = $state<{ title: string; text: string; diff: boolean } | null>(null)

  const statusText: Record<string, string> = {
    queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断',
  }

  async function load() {
    try {
      const d = await api.run(id)
      detail = d
      view = runFromHistory(d.run, d.events, d.approvals)
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }

  $effect(() => {
    void id
    void chat.runsRev
    void chat.inboxRev
    load()
  })

  const data = $derived(wb.data)
  const run = $derived(detail?.run ?? data?.runs.find((r) => r.id === id))
  const task = $derived(run?.task_id ? data?.tasks.find((t) => t.id === run.task_id) : undefined)
  const persona = $derived(data && task ? personaFor(data, task, run) : undefined)
  const node = $derived(data?.nodes.find((n) => n.id === run?.node_id))
  const meta = $derived(run ? runMeta(run, node) : null)
  const metaText = $derived(
    [persona?.name ?? '未分配', meta?.agentTitle, meta?.model, meta?.effort ? `思考 ${meta.effort}` : '']
      .filter(Boolean)
      .join(' · '),
  )
  const a = $derived(avatarOf(persona, run?.agent))
  const acts = $derived(wb.live[id] ?? [])
  const approvals = $derived(view?.approvals ?? [])
  const pending = $derived(approvals.filter((x) => x.status === 'pending'))
  const liveIDs = $derived(new Set(acts.filter((x) => x.kind === 'tool').map((x) => x.id)))
  const steps = $derived((view?.tools ?? []).filter((t) => !liveIDs.has(t.id)))
  const plan = $derived(view?.plan ?? [])
  const runState = $derived(run ? (run.paused ? '已暂停' : statusText[run.status] ?? run.status) : '')
  const tone = $derived(run ? (run.paused ? 'warning' : run.status === 'running' ? 'accent' : run.status === 'succeeded' ? 'success' : run.status === 'failed' || run.status === 'interrupted' ? 'danger' : 'neutral') : 'neutral')
  const toolIcon: Record<string, string> = { completed: '✓', failed: '✕', in_progress: '…', pending: '·' }

  async function act(key: string, fn: () => Promise<unknown>) {
    busy = key
    error = ''
    try {
      await fn()
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  async function open(what: 'log' | 'diff') {
    if (!run) return
    await act(what, async () => {
      const text = await api.runText(run.id, what)
      viewer = { title: what === 'diff' ? `改动 · ${run.branch}` : '运行日志', text, diff: what === 'diff' }
    })
  }
</script>

<Sheet title={task?.title || (run?.persona ? `${run.persona} 的 Run` : '运行详情')} wide {onClose}>
  {#if !run}
    <p class="muted">{error || '这个 Run 不在了。'}</p>
  {:else}
    <header class="head">
      <Avatar glyph={a.glyph} color={a.color} title={a.title} size={30} ring={tone === 'neutral' ? '' : tone} />
      <div class="who">
        <p class="name">{metaText}</p>
        <p class="meta">
          {#if node}{node.name}{/if}{#if run.branch} · {run.branch}{/if}{#if run.pr_url} · <a href={run.pr_url} target="_blank" rel="noreferrer">PR</a>{/if}
        </p>
      </div>
      <span class="st tone-{tone}">{runState}</span>
    </header>

    {#if error}<p class="err" role="alert">{error}</p>{/if}
    {#if run.summary}<p class="summary">{run.summary}</p>{/if}
    {#if run.error}<p class="err">{run.error}</p>{/if}
    {#if run.diffstat}<p class="muted">{run.diffstat}</p>{/if}

    <div class="actions">
      {#if isActive(run) && run.paused}
        <button class="btn small" disabled={!!busy} onclick={() => act('resume', () => api.resumeRun(run.id))}>继续</button>
      {:else if run.status === 'running' && run.purpose !== 'deliver'}
        <button class="btn small ghost" disabled={!!busy} onclick={() => act('pause', () => api.pauseRun(run.id))}>暂停</button>
      {/if}
      {#if isActive(run)}
        <button class="link" disabled={!!busy} onclick={() => act('cancel', () => api.cancelRun(run.id))}>取消</button>
      {/if}
      {#if run.diffstat && run.purpose !== 'deliver'}<button class="link" disabled={!!busy} onclick={() => open('diff')}>改动</button>{/if}
      {#if run.status !== 'queued'}<button class="link" disabled={!!busy} onclick={() => open('log')}>日志</button>{/if}
      {#if task}<button class="link accent" onclick={() => { ui.runSheet = ''; go({ view: 'task', id: task.id }) }}>打开任务</button>{/if}
    </div>

    {#each pending as x (x.id)}
      <ApprovalCard approval={x} context={task?.title} />
    {/each}

    <section>
      <h3>活动</h3>
      {#if acts.length}
        <ol class="feed">
          {#each acts as x (x.id)}
            <li class={x.kind}>
              {#if x.kind === 'tool'}<Icon name="link" size={11} />{:else if x.kind === 'thought'}<Icon name="bulb" size={11} />{/if}
              <span class="txt">{x.text}</span>
              {#if x.status}<span class="ts">{toolIcon[x.status] ?? x.status}</span>{/if}
            </li>
          {/each}
        </ol>
      {:else if steps.length || plan.length}
        <p class="muted">没有实时输出，下面是已经记录下来的步骤。</p>
      {:else}
        <p class="muted">还没有输出。</p>
      {/if}
    </section>

    {#if plan.length}
      <section>
        <h3>计划</h3>
        <ul class="plan">
          {#each plan as x, i (i)}
            <li class={x.status}>{x.content}</li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if steps.length}
      <section>
        <h3>步骤</h3>
        <ul class="steps">
          {#each steps as t (t.id)}
            <li class={t.status}><span class="ico">{toolIcon[t.status] ?? '·'}</span>{t.title}</li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}
</Sheet>

{#if viewer}<TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => (viewer = null)} />{/if}

<style>
  .head { display: flex; align-items: center; gap: 9px; min-width: 0; }
  .who { min-width: 0; flex: 1; }
  .name { margin: 0; font-weight: 600; font-size: 14px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { margin: 0; font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta a { color: var(--accent); }
  .st { font-size: 13px; font-weight: 600; white-space: nowrap; }
  .summary { margin: 0; font-size: 14px; white-space: pre-wrap; overflow-wrap: anywhere; }
  .actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
  .btn.small { padding: 6px 14px; font-size: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.accent { color: var(--accent); font-weight: 600; }
  section { display: grid; gap: 5px; }
  h3 { margin: 4px 0 0; font-size: 13px; color: var(--muted); }
  .feed, .steps, .plan { list-style: none; margin: 0; padding: 0; display: grid; gap: 3px; font-size: 13px; }
  .feed li { display: flex; gap: 6px; align-items: baseline; overflow-wrap: anywhere; }
  .feed li .txt { flex: 1; min-width: 0; }
  .feed li.thought { color: var(--muted); }
  .feed li.permission, .feed li.status { color: var(--warm); }
  .ts { color: var(--muted); font-size: 12px; }
  .steps li { color: var(--muted); }
  .steps li.completed { color: var(--text); }
  .steps li.failed { color: var(--danger); }
  .ico { display: inline-block; width: 1.2em; }
  .plan li::before { content: '○ '; color: var(--muted); }
  .plan li.completed::before { content: '● '; color: var(--ok); }
  .plan li.in_progress::before { content: '◐ '; color: var(--warm); }
  .muted { margin: 0; color: var(--muted); font-size: 13px; }
  .err { margin: 0; color: var(--danger); font-size: 13px; overflow-wrap: anywhere; }
</style>
