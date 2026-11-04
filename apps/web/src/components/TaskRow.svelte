<script lang="ts">
  // One task in a project card: state badge, title, one line of summary and a
  // compact stage bar while it is being worked on. The sheet has the rest.
  import { taskStates } from '../lib/status'
  import type { CardTask } from '../lib/workbench'
  import Icon from './Icon.svelte'
  import StageBar from './StageBar.svelte'

  let { task, onOpen }: { task: CardTask; onOpen: () => void } = $props()

  const info = $derived(taskStates[task.state])
  const showBar = $derived(task.state === 'active' || task.state === 'attention')
  const label = $derived(`${task.task.title}，${info.text}${task.detail && task.detail !== info.text ? '，' + task.detail : ''}`)
</script>

<button class="task" aria-label={label} onclick={onOpen}>
  <span class="line">
    <span class="tone-{info.tone}"><Icon name={info.icon} size={13} /></span>
    <span class="title">{task.task.title}</span>
    <span class="state tone-{info.tone}">{info.text}{task.detail && task.detail !== info.text ? ` · ${task.detail}` : ''}</span>
  </span>
  {#if task.summary}<span class="sum">{task.summary}</span>{/if}
  {#if showBar}<StageBar stages={task.stages} />{/if}
</button>

<style>
  .task {
    display: grid; gap: 3px; width: 100%; text-align: left; border: 0; background: none; padding: 6px 6px;
    border-radius: 9px; min-width: 0;
  }
  .task:hover { background: var(--sunken); }
  .line { display: flex; align-items: center; gap: 6px; min-width: 0; }
  .title { flex: 1; font-size: 14px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .state { font-size: 12px; font-weight: 600; white-space: nowrap; }
  .sum { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
