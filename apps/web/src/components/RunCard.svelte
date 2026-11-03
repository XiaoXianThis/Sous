<script lang="ts">
  import { api } from '../lib/api'
  import { isActive, type RunView } from '../lib/runs'
  import TextViewer from './TextViewer.svelte'

  let { view, nodeName }: { view: RunView; nodeName: (id: string) => string } = $props()

  const run = $derived(view.run)
  const pending = $derived(view.approvals.filter((a) => a.status === 'pending'))
  const statusText: Record<string, string> = {
    queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断',
  }
  const toolIcon: Record<string, string> = { completed: '✓', failed: '✕', in_progress: '…', pending: '·' }
  let busy = $state('')
  let error = $state('')
  let viewer = $state<{ title: string; text: string; diff: boolean } | null>(null)
  let showOutput = $state(false)

  async function act(key: string, fn: () => Promise<unknown>) {
    busy = key
    error = ''
    try {
      await fn()
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  async function open(what: 'log' | 'diff') {
    await act(what, async () => {
      const text = await api.runText(run.id, what)
      viewer = { title: what === 'diff' ? `改动 · ${run.branch}` : '运行日志', text, diff: what === 'diff' }
    })
  }

  function optionClass(kind: string) {
    return kind.startsWith('allow') ? 'btn small' : 'btn small ghost'
  }
</script>

<section class="run {run.status}" data-testid="run-card">
  <header>
    <span class="badge">{statusText[run.status] ?? run.status}</span>
    <strong>{run.agent}</strong>
    <span class="where">@ {nodeName(run.node_id)}{#if run.branch} · {run.branch}{/if}</span>
  </header>
  <p class="prompt" title={run.prompt}>{run.prompt}</p>
  {#if run.repo_path}<p class="meta">仓库：{run.repo_path}</p>{/if}

  {#if view.plan.length}
    <ul class="plan">
      {#each view.plan as e}<li class={e.status}>{e.content}</li>{/each}
    </ul>
  {/if}
  {#if view.tools.length}
    <ul class="tools">
      {#each view.tools.slice(-8) as t (t.id)}
        {@const done = t.status === 'completed' || t.status === 'failed'}
        <li class={done || isActive(run) ? t.status : 'stopped'}>
          <span class="ico">{done || isActive(run) ? (toolIcon[t.status] ?? '·') : '–'}</span>{t.title}
        </li>
      {/each}
      {#if view.tools.length > 8}<li class="more">还有 {view.tools.length - 8} 个更早的步骤</li>{/if}
    </ul>
  {/if}
  {#if view.statusLine && isActive(run)}<p class="status-line">{view.statusLine}</p>{/if}

  {#each pending as a (a.id)}
    <div class="approval" role="group" aria-label="审批">
      <p class="ask">需要你批准：<strong>{a.title}</strong></p>
      {#if a.detail}<pre class="detail">{a.detail}</pre>{/if}
      <div class="options">
        {#each a.options as o}
          <button class={optionClass(o.kind)} disabled={!!busy} onclick={() => act(a.id, () => api.decide(a.id, o.option_id))}>{o.name}</button>
        {/each}
      </div>
    </div>
  {/each}

  {#if view.output && isActive(run)}
    <button class="link" onclick={() => (showOutput = !showOutput)}>{showOutput ? '收起' : '查看'} agent 的输出</button>
    {#if showOutput}<pre class="output">{view.output}</pre>{/if}
  {/if}

  {#if !isActive(run)}
    {#if run.summary}<p class="summary">{run.summary}</p>{/if}
    {#if run.error}<p class="err">{run.error}</p>{/if}
    {#if run.diffstat}<p class="meta">{run.diffstat}</p>{/if}
  {/if}

  <footer>
    {#if isActive(run)}
      <button class="link" disabled={!!busy} onclick={() => act('cancel', () => api.cancelRun(run.id))}>取消</button>
    {/if}
    {#if run.diffstat}<button class="link" disabled={!!busy} onclick={() => open('diff')}>查看改动</button>{/if}
    {#if run.status !== 'queued'}<button class="link" disabled={!!busy} onclick={() => open('log')}>日志</button>{/if}
  </footer>
  {#if error}<p class="err" role="alert">{error}</p>{/if}
</section>

{#if viewer}<TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => (viewer = null)} />{/if}

<style>
  .run {
    border: 1px solid var(--line); border-radius: 14px; background: var(--panel); padding: 12px 14px;
    display: grid; gap: 6px; border-left: 3px solid var(--muted);
  }
  .run.running, .run.queued { border-left-color: var(--accent); }
  .run.succeeded { border-left-color: #3a9d5d; }
  .run.failed, .run.interrupted { border-left-color: var(--danger); }
  header { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; }
  .badge { font-size: 12px; font-weight: 600; color: var(--warm); }
  .succeeded .badge { color: #3a9d5d; }
  .failed .badge, .interrupted .badge { color: var(--danger); }
  .where { color: var(--muted); font-size: 13px; overflow-wrap: anywhere; }
  .prompt { margin: 0; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
  .meta { margin: 0; font-size: 13px; color: var(--muted); overflow-wrap: anywhere; }
  ul { margin: 0; padding: 0; list-style: none; display: grid; gap: 2px; font-size: 13px; }
  .plan li::before { content: '○ '; color: var(--muted); }
  .plan li.completed::before { content: '● '; color: #3a9d5d; }
  .plan li.in_progress::before { content: '◐ '; color: var(--warm); }
  .tools li { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tools li.completed { color: var(--text); }
  .tools li.failed { color: var(--danger); }
  .ico { display: inline-block; width: 1.2em; }
  .more { font-style: italic; }
  .status-line { margin: 0; font-size: 13px; color: var(--muted); }
  .approval {
    border: 1px solid var(--warm); border-radius: 12px; padding: 10px 12px; display: grid; gap: 8px;
    background: color-mix(in srgb, var(--warm) 8%, var(--panel));
  }
  .ask { margin: 0; }
  .detail, .output {
    margin: 0; max-height: 200px; overflow: auto; font: 12px/1.45 ui-monospace, Menlo, Consolas, monospace;
    background: var(--bg); border-radius: 8px; padding: 8px 10px; white-space: pre-wrap; overflow-wrap: anywhere;
  }
  .options { display: flex; gap: 8px; flex-wrap: wrap; }
  .btn.small { padding: 6px 14px; font-size: 14px; }
  .summary { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
  .err { margin: 0; color: var(--danger); font-size: 13px; overflow-wrap: anywhere; }
  footer { display: flex; gap: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; justify-self: start; }
  .link:hover { color: var(--text); }
</style>
