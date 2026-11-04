<script lang="ts">
  // A run in the chat: one line — who, how it is going, how far. The steps
  // are on the task page.
  import { go, wb } from '../lib/app.svelte'
  import type { RunView } from '../lib/runs'
  import { avatarOf, looks, runLook } from '../lib/status'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  let { view, nodeName }: { view: RunView; nodeName: (id: string) => string } = $props()

  const run = $derived(view.run)
  const waiting = $derived(view.approvals.some((a) => a.status === 'pending' && a.review === 'user'))
  const look = $derived(runLook(run, waiting))
  const persona = $derived(wb.data?.personas.find((p) => p.name === run.persona))
  const a = $derived(run.purpose === 'deliver' || run.purpose === 'clone' || run.purpose === 'create'
    ? { glyph: 's', color: 'gray' as const, title: 'sous' }
    : avatarOf(persona, run.agent))
  const what: Record<string, string> = { deliver: '交付 PR', rebase: 'rebase', clone: 'clone 项目', create: '新建项目' }
  const steps = $derived(view.tools.length)

  function open() {
    if (run.task_id) go({ view: 'task', id: run.task_id })
  }
</script>

<button class="chip" onclick={open} disabled={!run.task_id} title="{a.title} @ {nodeName(run.node_id)} · {looks[look].text}{run.title ? ' · ' + run.title : ''}">
  <Avatar glyph={a.glyph} color={a.color} size={22} ring={looks[look].tone === 'neutral' ? '' : looks[look].tone} />
  <span class="tone-{looks[look].tone} ic"><Icon name={looks[look].icon} size={12} /></span>
  <span class="t">{what[run.purpose] ?? run.title ?? ''}</span>
  <span class="m"><Icon name="laptop" size={11} />{nodeName(run.node_id)}{steps ? ` · ${steps} 步` : ''}</span>
  {#if run.task_id}<span class="go"><Icon name="chevron" size={12} /></span>{/if}
</button>

<style>
  .chip {
    display: inline-flex; align-items: center; gap: 6px; max-width: 100%; min-width: 0; justify-self: start; overflow: hidden;
    border: 1px solid var(--line); background: var(--panel); border-radius: 999px; padding: 3px 10px 3px 4px; font-size: 12px;
  }
  .chip:not(:disabled):hover { border-color: var(--accent); }
  .chip:disabled { opacity: 1; cursor: default; }
  .go { display: grid; transform: rotate(-90deg); color: var(--muted); }
  .ic { display: grid; }
  .t { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1 1 auto; }
  .m { display: inline-flex; gap: 3px; align-items: center; color: var(--muted); white-space: nowrap; }
</style>
