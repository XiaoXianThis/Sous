<script lang="ts">
  // A running Run inside a project card: the task, who is doing it with which
  // agent/model/machine, and the last two lines of activity.
  import { wb } from '../lib/app.svelte'
  import { oneLine, tail } from '../lib/live'
  import { avatarOf, looks } from '../lib/status'
  import { runMeta, type CardAgent } from '../lib/workbench'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  let { agent, onOpen }: { agent: CardAgent; onOpen: () => void } = $props()

  const run = $derived(agent.run)
  const node = $derived(wb.data?.nodes.find((n) => n.id === run.node_id))
  const meta = $derived(runMeta(run, node))
  const metaText = $derived(
    [agent.persona?.name ?? '未分配', meta.agentTitle, meta.model, meta.effort ? `思考 ${meta.effort}` : '', node ? `@ ${node.name}` : '']
      .filter(Boolean)
      .join(' · '),
  )
  const acts = $derived(tail(wb.live[run.id] ?? [], 2))
  const a = $derived(avatarOf(agent.persona, run.agent))
  const look = $derived(looks[agent.look])
  const label = $derived(`${agent.title}，${look.text}，${agent.persona?.name ?? '未分配'}，${meta.agentTitle}`)
</script>

<button class="agent" aria-label={label} onclick={onOpen}>
  <span class="head">
    <Avatar glyph={a.glyph} color={a.color} title={a.title} size={22} ring={look.tone === 'neutral' ? '' : look.tone} />
    <span class="title">{agent.title}</span>
    <span class="st tone-{look.tone}"><Icon name={look.icon} size={12} />{look.text}</span>
  </span>
  <span class="meta">{metaText}</span>
  <span class="acts">
    {#each acts as act (act.id)}
      <span class="act {act.kind}">
        {#if act.kind === 'tool'}<span class="toolmark {act.status ?? ''}"></span>{:else if act.kind === 'thought'}<Icon name="bulb" size={10} />{/if}
        <span class="txt">{oneLine(act)}</span>
      </span>
    {:else}
      <span class="act muted">等待输出…</span>
    {/each}
  </span>
</button>

<style>
  .agent {
    display: grid; gap: 3px; width: 100%; text-align: left; border: 1px solid var(--line); background: var(--panel);
    border-radius: 11px; padding: 8px 9px; min-width: 0;
  }
  .agent:hover { border-color: var(--accent); }
  .head { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .title { flex: 1; font-size: 14px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .st { display: inline-flex; align-items: center; gap: 3px; font-size: 12px; font-weight: 600; white-space: nowrap; }
  .meta { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .acts {
    display: grid; grid-auto-rows: 17px; height: 34px; overflow: hidden; align-content: start;
    font-size: 12px; color: var(--muted); margin-top: 1px;
  }
  .act { display: flex; align-items: center; gap: 4px; min-width: 0; line-height: 17px; }
  .act .txt { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .act.thought { color: color-mix(in srgb, var(--muted) 80%, transparent); }
  .act.status, .act.permission { color: var(--warm); }
  .act.muted { color: var(--muted); font-style: italic; }
  .toolmark { width: 5px; height: 5px; border-radius: 50%; background: var(--muted); flex: none; }
  .toolmark.completed { background: var(--ok); }
  .toolmark.failed { background: var(--danger); }
  .toolmark.in_progress { background: var(--info); }
</style>
