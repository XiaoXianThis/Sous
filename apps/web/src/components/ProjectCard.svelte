<script lang="ts">
  // A project card: where it lives, the sub-agents working there now, and its
  // tasks as compact rows. Most needs-attention items stay on the card.
  import { ui, wb } from '../lib/app.svelte'
  import { taskStates } from '../lib/status'
  import type { ProjectCard as Card } from '../lib/workbench'
  import AgentCard from './AgentCard.svelte'
  import ApprovalCard from './ApprovalCard.svelte'
  import Icon from './Icon.svelte'
  import Locations from './Locations.svelte'
  import TaskRow from './TaskRow.svelte'

  let { card, onOpenTask, onOpenProject }: {
    card: Card
    onOpenTask: (id: string) => void
    onOpenProject: (id: string) => void
  } = $props()

  const idle = $derived(card.allAgents.length === 0 && card.allTasks.length === 0)
  const info = $derived(idle ? { text: '空闲', icon: 'circle', tone: 'neutral' as const } : taskStates[card.rank])
  const runIds = $derived(new Set(card.agents.map((a) => a.run.id)))
  const approvals = $derived((wb.inbox?.approvals ?? []).filter((a) => a.review === 'user' && runIds.has(a.run_id)))
  const counts = $derived([
    card.counts.active ? `${card.counts.active} 进行中` : '',
    card.counts.todo ? `${card.counts.todo} 待办` : '',
    card.counts.closed ? `${card.counts.closed} 已结束` : '',
  ].filter(Boolean).join(' · '))
  const hidden = $derived(card.hiddenTasks + card.hiddenAgents)
  const titleOf = (runId: string) => card.agents.find((a) => a.run.id === runId)?.title ?? ''
</script>

<article class="card" aria-label={`项目 ${card.project.name}`}>
  <header>
    <button class="pname" aria-label={`打开项目 ${card.project.name}`} onclick={() => onOpenProject(card.project.id)}>
      <span class="tone-{info.tone}"><Icon name={info.icon} size={14} /></span>
      <strong>{card.project.name}</strong>
    </button>
    <span class="sp"></span>
    {#if card.counts.attention}
      <span class="attn" title="需要你处理"><Icon name="hand" size={12} />{card.counts.attention}</span>
    {/if}
    <button class="icon-btn gear" title="项目设置" aria-label={`${card.project.name} 的设置`} onclick={() => onOpenProject(card.project.id)}>
      <Icon name="settings" size={14} />
    </button>
  </header>

  <Locations locations={card.locations} />
  {#if card.project.notes}<p class="notes">{card.project.notes}</p>{/if}

  {#each card.agents as a (a.run.id)}
    <AgentCard agent={a} onOpen={() => (ui.runSheet = a.run.id)} />
  {/each}

  {#if card.tasks.length}
    <div class="tasks" aria-label="任务">
      {#each card.tasks as t (t.task.id)}
        <TaskRow task={t} onOpen={() => onOpenTask(t.task.id)} />
      {/each}
    </div>
  {/if}

  {#if hidden > 0}
    <button class="more" onclick={() => onOpenProject(card.project.id)}>还有 {hidden} 项…</button>
  {/if}

  {#if approvals.length}
    <div class="approvals">
      {#each approvals as a (a.id)}
        <ApprovalCard approval={a} context={titleOf(a.run_id)} />
      {/each}
    </div>
  {/if}

  {#if idle}
    <p class="empty">还没有任务</p>
  {/if}

  <footer>
    <span class="counts">{counts || (idle ? '空闲' : '')}</span>
    <span class="sp"></span>
    <button class="link" onclick={() => (ui.newTask = card.project.id)}>新建任务</button>
    {#if card.project.deploy}<button class="link" onclick={() => onOpenProject(card.project.id)}>部署</button>{/if}
  </footer>
</article>

<style>
  .card {
    display: grid; gap: 7px; align-content: start; border: 1px solid var(--line); background: var(--panel);
    border-radius: 14px; padding: 11px 12px; min-width: 0; align-self: start;
  }
  .card:hover { border-color: color-mix(in srgb, var(--accent) 30%, var(--line)); }
  header { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .pname {
    display: inline-flex; align-items: center; gap: 6px; min-width: 0; border: 0; background: none;
    padding: 2px 4px; border-radius: 8px; text-align: left;
  }
  .pname:hover { background: var(--sunken); }
  .pname strong { font-size: 15px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sp { flex: 1; }
  .attn {
    display: inline-flex; align-items: center; gap: 3px; font-size: 12px; font-weight: 600; color: var(--warm);
    border: 1px solid color-mix(in srgb, var(--warm) 40%, var(--line)); border-radius: 999px; padding: 0 7px;
  }
  .gear { width: 28px; height: 28px; }
  .notes {
    margin: 0; font-size: 13px; color: var(--muted); display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2;
    -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere;
  }
  .tasks { display: grid; gap: 2px; border-top: 1px solid var(--line); padding-top: 5px; }
  .approvals { display: grid; gap: 6px; }
  .more {
    justify-self: start; border: 0; background: none; color: var(--muted); padding: 0 6px; font-size: 12px;
  }
  .more:hover { color: var(--accent); }
  .empty { margin: 0 6px; font-size: 13px; color: var(--muted); }
  footer { display: flex; align-items: center; gap: 10px; border-top: 1px solid var(--line); padding-top: 6px; }
  .counts { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 12px; white-space: nowrap; }
  .link:hover { color: var(--accent); }
</style>
