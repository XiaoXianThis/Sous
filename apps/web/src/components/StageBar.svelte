<script lang="ts">
  import type { Stage } from '../lib/status'

  let { stages, labels = false }: { stages: Stage[]; labels?: boolean } = $props()
</script>

<div class="bar" class:labels role="img" aria-label={stages.map((s) => `${s.text}${{ done: '完成', current: '进行中', todo: '', failed: '失败' }[s.state]}`).join('，')}>
  {#each stages as s (s.key)}
    <div class="seg">
      <span class="fill {s.state}"></span>
      {#if labels}<span class="lab {s.state}">{s.text}</span>{/if}
    </div>
  {/each}
</div>

<style>
  .bar { display: flex; gap: 3px; }
  .seg { flex: 1; display: grid; gap: 3px; min-width: 0; }
  .fill { height: 5px; border-radius: 3px; background: var(--line); }
  .labels .fill { height: 6px; }
  .fill.done { background: var(--ok); }
  .fill.current { background: var(--info); animation: pulse 1.6s ease-in-out infinite; }
  .fill.failed { background: var(--danger); }
  .lab { font-size: 11px; color: var(--muted); text-align: center; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .lab.current { color: var(--info); font-weight: 600; }
  .lab.failed { color: var(--danger); font-weight: 600; }
  @keyframes pulse { 50% { opacity: .55 } }
  @media (prefers-reduced-motion: reduce) { .fill.current { animation: none; } }
</style>
