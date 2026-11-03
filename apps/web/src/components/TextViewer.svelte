<script lang="ts">
  let { title, text, diff = false, onClose }: { title: string; text: string; diff?: boolean; onClose: () => void } = $props()

  const lines = $derived(text.split('\n'))
  function cls(line: string): string {
    if (!diff) return ''
    if (line.startsWith('+++') || line.startsWith('---')) return 'file'
    if (line.startsWith('diff --git')) return 'head'
    if (line.startsWith('@@')) return 'hunk'
    if (line.startsWith('+')) return 'add'
    if (line.startsWith('-')) return 'del'
    return ''
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onClose()} />

<div class="backdrop" role="presentation" onclick={onClose}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label={title}>
  <header>
    <h2>{title}</h2>
    <button class="icon-btn" aria-label="关闭" onclick={onClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>
  {#if !text.trim()}
    <p class="empty">（空）</p>
  {:else}
    <pre>{#each lines as line}<span class={cls(line)}>{line}</span>{/each}</pre>
  {/if}
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / .35); z-index: 40; }
  .sheet {
    position: fixed; z-index: 41; top: 5vh; left: 50%; transform: translateX(-50%);
    width: min(900px, calc(100vw - 32px)); max-height: 90vh; display: flex; flex-direction: column;
    background: var(--panel); border: 1px solid var(--line); border-radius: 16px; padding: 14px 16px;
  }
  header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
  h2 { margin: 0; font-size: 16px; }
  pre {
    margin: 0; overflow: auto; font: 12.5px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    background: var(--bg); border: 1px solid var(--line); border-radius: 10px; padding: 10px 12px;
  }
  span { display: block; white-space: pre; min-height: 1.5em; }
  .add { background: color-mix(in srgb, #3a9d5d 16%, transparent); }
  .del { background: color-mix(in srgb, var(--danger) 14%, transparent); }
  .hunk { color: var(--muted); }
  .head, .file { font-weight: 600; }
  .empty { color: var(--muted); }
  @media (max-width: 760px) {
    .sheet { top: auto; bottom: 0; left: 0; transform: none; width: 100%; border-radius: 16px 16px 0 0; max-height: 92vh; }
  }
</style>
