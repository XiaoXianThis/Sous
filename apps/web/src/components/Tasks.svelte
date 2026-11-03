<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { board, columns, isOpen, statusText, tone } from '../lib/tasks'
  import type { Project, Task } from '../lib/types'

  let { onOpenTask, onMenu }: { onOpenTask: (id: string) => void; onMenu: () => void } = $props()

  let tasks = $state<Task[]>([])
  let projects = $state<Project[]>([])
  let view = $state<'board' | 'list'>(localView())
  let project = $state('')
  let showClosed = $state(false)
  let error = $state('')
  let loaded = $state(false)

  function localView(): 'board' | 'list' {
    try {
      return localStorage.getItem('sous.tasks.view') === 'list' ? 'list' : 'board'
    } catch {
      return 'board'
    }
  }
  $effect(() => {
    try {
      localStorage.setItem('sous.tasks.view', view)
    } catch {}
  })

  async function load() {
    try {
      const [t, p] = await Promise.all([api.tasks(), api.projects()])
      tasks = t.tasks
      projects = p.projects
      error = ''
    } catch (e) {
      error = (e as Error).message
    } finally {
      loaded = true
    }
  }

  let timer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    void chat.tasksRev
    clearTimeout(timer)
    timer = setTimeout(load, 120)
    return () => clearTimeout(timer)
  })

  const shown = $derived(tasks.filter((t) => (!project || t.project_id === project) && (showClosed || isOpen(t) || view === 'board')))
  const cols = $derived(board(shown))
  const projectName = (id: string) => projects.find((p) => p.id === id)?.name ?? ''
  const childCount = (id: string) => tasks.filter((t) => t.parent_id === id).length
</script>

<section class="tasks">
  <header class="top">
    <button class="icon-btn menu" aria-label="对话列表" onclick={onMenu}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 7h16M4 12h16M4 17h16"/></svg>
    </button>
    <h2>任务</h2>
    <div class="seg" role="group" aria-label="视图">
      <button class:on={view === 'board'} onclick={() => (view = 'board')}>看板</button>
      <button class:on={view === 'list'} onclick={() => (view = 'list')}>列表</button>
    </div>
    <button class="btn small" onclick={() => onOpenTask('new')}>新任务</button>
  </header>

  <div class="filters">
    <select bind:value={project} aria-label="项目">
      <option value="">全部项目</option>
      {#each projects as p}<option value={p.id}>{p.name}</option>{/each}
    </select>
    {#if view === 'list'}
      <label class="check"><input type="checkbox" bind:checked={showClosed} /> 显示已结束</label>
    {/if}
  </div>
  {#if error}<p class="err" role="alert">{error}</p>{/if}

  {#if loaded && tasks.length === 0}
    <div class="empty">
      <p class="big">还没有任务</p>
      <p>在对话里告诉 sous 要做什么，它会自己建任务；也可以点“新任务”手动添加。</p>
    </div>
  {:else if view === 'board'}
    <div class="board">
      {#each columns as c (c.key)}
        <div class="col">
          <h3>{c.title} <span>{cols[c.key].length}</span></h3>
          {#each cols[c.key].slice(0, c.key === 'closed' ? 20 : 100) as t (t.id)}
            <button class="card {tone(t.status)}" onclick={() => onOpenTask(t.id)} data-testid="task-card">
              <span class="st">{statusText[t.status]}{t.priority ? ` · P${t.priority}` : ''}</span>
              <span class="tt">{t.title}</span>
              {#if t.status_reason && isOpen(t)}<span class="why">{t.status_reason}</span>{/if}
              <span class="meta">
                {projectName(t.project_id) || (t.kind === 'general' ? '一般任务' : '')}
                {#if childCount(t.id)} · {childCount(t.id)} 个子任务{/if}
                {#if t.pr_url} · PR{/if}
              </span>
            </button>
          {/each}
        </div>
      {/each}
    </div>
  {:else}
    <table class="list">
      <thead><tr><th>任务</th><th>状态</th><th>项目</th><th>更新</th></tr></thead>
      <tbody>
        {#each shown as t (t.id)}
          <tr onclick={() => onOpenTask(t.id)} class={tone(t.status)}>
            <td class="tt">{t.parent_id ? '↳ ' : ''}{t.title}</td>
            <td class="st">{statusText[t.status]}</td>
            <td>{projectName(t.project_id) || '—'}</td>
            <td class="when">{new Date(t.updated_at).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<style>
  .tasks { display: flex; flex-direction: column; min-width: 0; min-height: 0; height: 100%; background: var(--bg); }
  .top {
    display: flex; align-items: center; gap: 10px; padding: 10px 16px;
    padding-top: calc(10px + env(safe-area-inset-top)); border-bottom: 1px solid var(--line);
  }
  .top h2 { margin: 0; font-size: 16px; flex: 1; }
  .menu { display: none; }
  .seg { display: inline-flex; border: 1px solid var(--line); border-radius: 999px; overflow: hidden; }
  .seg button { border: 0; background: transparent; padding: 4px 12px; font-size: 13px; color: var(--muted); }
  .seg button.on { background: var(--sunken); color: var(--text); font-weight: 600; }
  .btn.small { padding: 6px 14px; font-size: 14px; }
  .filters { display: flex; gap: 12px; align-items: center; padding: 10px 16px 0; font-size: 13px; }
  select { border: 1px solid var(--line); background: var(--panel); border-radius: 8px; padding: 4px 8px; }
  .check { display: flex; gap: 6px; align-items: center; color: var(--muted); }
  .err { color: var(--danger); font-size: 13px; padding: 0 16px; }
  .empty { text-align: center; color: var(--muted); padding: 16vh 16px 0; }
  .empty .big { font-size: 20px; color: var(--text); font-weight: 600; margin: 0 0 6px; }

  .board {
    flex: 1; min-height: 0; overflow: auto; display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 12px; padding: 12px 16px 16px; align-items: start; align-content: start;
  }
  .col { display: grid; gap: 8px; background: var(--sunken); border-radius: 14px; padding: 10px; }
  .col h3 { margin: 0 2px 2px; font-size: 13px; color: var(--muted); }
  .col h3 span { font-weight: 400; }
  .card {
    display: grid; gap: 3px; text-align: left; border: 1px solid var(--line); border-left: 3px solid var(--muted);
    background: var(--panel); border-radius: 10px; padding: 8px 10px; width: 100%;
  }
  .card:hover { border-color: var(--accent); }
  .card.active { border-left-color: var(--accent); }
  .card.attention { border-left-color: var(--danger); }
  .card.done { border-left-color: #3a9d5d; }
  .st { font-size: 12px; color: var(--warm); font-weight: 600; }
  .attention .st { color: var(--danger); }
  .done .st { color: #3a9d5d; }
  .tt { overflow-wrap: anywhere; }
  .why { font-size: 12px; color: var(--muted); display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .meta { font-size: 12px; color: var(--muted); }

  .list { margin: 12px 16px; border-collapse: collapse; font-size: 14px; }
  .list th { text-align: left; font-size: 12px; color: var(--muted); font-weight: 600; padding: 6px 8px; border-bottom: 1px solid var(--line); }
  .list td { padding: 8px; border-bottom: 1px solid var(--line); cursor: pointer; }
  .list tr:hover td { background: var(--sunken); }
  .list .when { color: var(--muted); white-space: nowrap; font-size: 13px; }

  @media (max-width: 760px) {
    .menu { display: inline-grid; margin-left: -8px; }
    .board { grid-template-columns: 1fr; }
    .list th:nth-child(3), .list td:nth-child(3), .list th:nth-child(4), .list td:nth-child(4) { display: none; }
    .list { margin: 12px; }
  }
</style>
