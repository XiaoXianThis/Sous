<script lang="ts">
  // The workbench: one page of project cards. No project × machine matrix;
  // machines are a strip at the top and chips inside each card.
  import { go, ui, wb } from '../lib/app.svelte'
  import { columnCount, distribute, estimateHeight } from '../lib/masonry'
  import { projectCards } from '../lib/workbench'
  import Icon from './Icon.svelte'
  import ProjectCard from './ProjectCard.svelte'
  import ProjectSetupSheet from './ProjectSetupSheet.svelte'

  let setup = $state(false)
  let width = $state(0)

  const data = $derived(wb.data)
  const cards = $derived(data ? projectCards(data, wb.inbox?.tasks ?? []) : [])
  const attention = $derived(ui.route.view === 'inbox')
  const pending = $derived((wb.inbox?.approvals.filter((a) => a.review === 'user').length ?? 0) + (wb.inbox?.tasks.length ?? 0))
  const waitingRunIds = $derived(new Set((wb.inbox?.approvals ?? []).filter((a) => a.review === 'user').map((a) => a.run_id)))
  const shown = $derived(
    attention ? cards.filter((c) => c.rank === 'attention' || c.agents.some((a) => waitingRunIds.has(a.run.id))) : cards,
  )
  const cols = $derived(columnCount(width))
  const columns = $derived(distribute(shown, cols, estimateHeight))
  const busy = (nodeId: string) => data?.runs.filter((r) => r.node_id === nodeId).length ?? 0
  const filter = () => go({ view: attention ? 'overview' : 'inbox', id: '' })
</script>

<div class="page">
  <header class="head">
    <h1>工作台</h1>
    <span class="sp"></span>
    {#if pending > 0}
      <button class="pending" class:on={attention} aria-pressed={attention} onclick={filter}>
        <Icon name="hand" size={14} />需要你处理 {pending}
      </button>
    {:else if attention}
      <button class="pending" onclick={filter}>显示全部</button>
    {/if}
    <button class="btn small ghost" onclick={() => (setup = true)}><Icon name="plus" size={15} />新建 / 登记</button>
  </header>

  {#if data}
    <div class="machines" aria-label="机器">
      {#each data.nodes as n (n.id)}
        <button class="machine" class:off={!n.online} title="{n.name}{n.online ? '' : '（离线）'}" onclick={() => (ui.settings = 'machines')}>
          <Icon name={n.role === 'server' ? 'server' : 'laptop'} size={14} />
          <span class="mn">{n.name}</span>
          <span class="load">{busy(n.id)}/{n.concurrency}</span>
          <span class="dot" class:on={n.online}></span>
        </button>
      {/each}
      {#if !data.nodes.length}
        <button class="machine" onclick={() => (ui.settings = 'machines')}><Icon name="laptop" size={14} />还没有机器，去配对</button>
      {/if}
    </div>
  {/if}

  {#if wb.error && !data}
    <p class="err">{wb.error}</p>
  {:else if !data}
    <p class="muted">加载中…</p>
  {:else if attention && !shown.length}
    <div class="empty">
      <Icon name="check" size={36} />
      <p class="big">现在没有需要你处理的事。</p>
      <p>待办、进行中和已结束的任务都在项目卡片里。</p>
    </div>
  {:else if !cards.length}
    <div class="empty">
      <Icon name="folders" size={36} />
      <p class="big">还没有项目</p>
      <p>新建一个，或者登记工作空间里已有的仓库。</p>
      <button class="btn" onclick={() => (setup = true)}>新建或登记项目</button>
    </div>
  {:else}
    <div class="grid" bind:clientWidth={width} style="--cols: {cols}">
      {#each columns as column, i (i)}
        <div class="col">
          {#each column as card (card.project.id)}
            <ProjectCard {card} onOpenTask={(id) => go({ view: 'task', id })} onOpenProject={(id) => go({ view: 'project', id })} />
          {/each}
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if setup}<ProjectSetupSheet onClose={() => (setup = false)} />{/if}

<style>
  .page { padding: 16px 20px 30px; display: grid; gap: 12px; align-content: start; max-width: 1400px; }
  .head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  h1 { font-size: 20px; margin: 0; }
  .sp { flex: 1; }
  .pending {
    display: inline-flex; align-items: center; gap: 4px; border: 1px solid var(--line); background: var(--panel);
    border-radius: 999px; padding: 4px 12px; font-size: 13px; color: var(--warm);
  }
  .pending:hover { border-color: var(--warm); }
  .pending.on { background: color-mix(in srgb, var(--warm) 12%, var(--panel)); border-color: var(--warm); }
  .btn.small { padding: 6px 14px; font-size: 14px; display: inline-flex; align-items: center; gap: 4px; }
  .machines { display: flex; gap: 6px; flex-wrap: wrap; overflow-x: auto; }
  .machine {
    display: inline-flex; align-items: center; gap: 6px; border: 1px solid var(--line); background: var(--panel);
    border-radius: 999px; padding: 3px 10px; font-size: 12px; color: var(--muted); white-space: nowrap;
  }
  .machine:hover { border-color: var(--accent); color: var(--text); }
  .machine.off { opacity: .55; }
  .mn { color: var(--text); }
  .load { font-variant-numeric: tabular-nums; }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
  .dot.on { background: var(--ok); }
  .grid {
    display: grid; grid-template-columns: repeat(var(--cols, 1), minmax(0, 1fr)); gap: 14px;
    align-items: start;
  }
  .col { display: grid; gap: 14px; align-content: start; min-width: 0; }
  .muted { color: var(--muted); font-size: 14px; margin: 0; }
  .err { color: var(--danger); }
  .empty { display: grid; justify-items: center; gap: 6px; padding: 12vh 16px; color: var(--muted); text-align: center; }
  .empty .big { font-size: 18px; color: var(--text); font-weight: 600; margin: 6px 0 0; }
  .empty p { margin: 0; }
  .empty .btn { margin-top: 10px; }
  @media (max-width: 760px) {
    .page { padding: 12px 14px 24px; }
    .grid { grid-template-columns: 1fr; }
  }
</style>
