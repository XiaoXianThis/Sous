<script module lang="ts">
  // The sheets currently mounted, oldest first. Escape only closes the top
  // one, so a nested sheet does not take its parent down with it.
  const sheets: symbol[] = []
</script>

<script lang="ts">
  import type { Snippet } from 'svelte'

  let { title, onClose, wide = false, children }: { title: string; onClose: () => void; wide?: boolean; children: Snippet } = $props()

  const me = Symbol('sheet')
  $effect(() => {
    const opener = typeof document !== 'undefined' ? (document.activeElement as HTMLElement | null) : null
    sheets.push(me)
    return () => {
      const i = sheets.lastIndexOf(me)
      if (i >= 0) sheets.splice(i, 1)
      if (opener && opener.isConnected && typeof opener.focus === 'function') requestAnimationFrame(() => opener.focus())
    }
  })

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && sheets[sheets.length - 1] === me) onClose()
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="backdrop" role="presentation" onclick={onClose}></div>
<div class="sheet" class:wide role="dialog" aria-modal="true" aria-label={title}>
  <header>
    <h2>{title}</h2>
    <button class="icon-btn" aria-label="关闭" onclick={onClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>
  <div class="content">{@render children()}</div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / .35); z-index: 30; }
  .sheet {
    position: fixed; z-index: 31; top: 6vh; left: 50%; transform: translateX(-50%);
    width: min(640px, calc(100vw - 32px)); max-height: 88vh; display: flex; flex-direction: column;
    background: var(--panel); border: 1px solid var(--line); border-radius: 18px; padding: 16px 18px;
  }
  .sheet.wide { width: min(760px, calc(100vw - 32px)); }
  header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
  h2 { margin: 0; font-size: 18px; }
  .content { overflow-y: auto; display: grid; gap: 12px; align-content: start; min-height: 0; padding-bottom: 4px; }
  @media (max-width: 760px) {
    .sheet, .sheet.wide {
      top: auto; bottom: 0; left: 0; transform: none; width: 100%; max-height: 92vh;
      border-radius: 18px 18px 0 0; padding-bottom: calc(16px + env(safe-area-inset-bottom));
    }
  }
</style>
