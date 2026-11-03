<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import type { Memory, MemoryKind } from '../lib/types'

  let { onClose }: { onClose: () => void } = $props()

  const kindNames: Record<MemoryKind, string> = { profile: '关于你', project: '项目', lesson: '经验', note: '其他' }
  const kinds = Object.keys(kindNames) as MemoryKind[]

  let memories = $state<Memory[]>([])
  let query = $state('')
  let filter = $state<MemoryKind | ''>('')
  let loading = $state(true)
  let error = $state('')
  let editing = $state<string | null>(null) // memory id, or 'new'
  let draftKind = $state<MemoryKind>('profile')
  let draftContent = $state('')
  let saving = $state(false)

  async function load() {
    try {
      error = ''
      const res = await api.memories(query.trim(), query.trim() ? '' : filter)
      memories = filter && query.trim() ? res.memories.filter((m) => m.kind === filter) : res.memories
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }

  // Reload when the filter, the search, or any memory changes.
  let timer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    void filter
    void query
    void chat.memoryRev
    clearTimeout(timer)
    timer = setTimeout(load, 150)
    return () => clearTimeout(timer)
  })

  function startEdit(m?: Memory) {
    editing = m?.id ?? 'new'
    draftKind = m?.kind ?? (filter || 'profile')
    draftContent = m?.content ?? ''
  }

  async function save() {
    if (!draftContent.trim() || saving) return
    saving = true
    try {
      if (editing === 'new') await api.createMemory(draftKind, draftContent)
      else if (editing) await api.updateMemory(editing, draftKind, draftContent)
      editing = null
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      saving = false
    }
  }

  async function remove(m: Memory) {
    if (!confirm(`删除这条记忆？\n\n${m.content}`)) return
    try {
      await api.deleteMemory(m.id)
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      if (editing) editing = null
      else onClose()
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="backdrop" role="presentation" onclick={onClose}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label="记忆">
  <header>
    <h2>记忆</h2>
    <button class="icon-btn" aria-label="关闭" onclick={onClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>
  <p class="hint">sous 在对话中自动记下的长期信息。每次对话时，会带上“关于你”的全部记忆和其他相关记忆。</p>

  <div class="tools">
    <input type="search" placeholder="搜索记忆" bind:value={query} aria-label="搜索记忆" />
    <button class="btn small" onclick={() => startEdit()}>新增</button>
  </div>
  <div class="tabs" role="tablist">
    <button role="tab" aria-selected={filter === ''} class:on={filter === ''} onclick={() => (filter = '')}>全部</button>
    {#each kinds as k}
      <button role="tab" aria-selected={filter === k} class:on={filter === k} onclick={() => (filter = k)}>{kindNames[k]}</button>
    {/each}
  </div>

  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <ul>
    {#if editing === 'new'}
      <li class="edit">{@render editor()}</li>
    {/if}
    {#each memories as m (m.id)}
      <li class:edit={editing === m.id}>
        {#if editing === m.id}
          {@render editor()}
        {:else}
          <span class="kind">{kindNames[m.kind] ?? m.kind}</span>
          <p class="content">{m.content}</p>
          <div class="row-actions">
            <button class="link" onclick={() => startEdit(m)}>编辑</button>
            <button class="link danger" onclick={() => remove(m)}>删除</button>
          </div>
        {/if}
      </li>
    {:else}
      {#if !loading && editing !== 'new'}
        <li class="empty">{query ? '没有找到相关记忆' : '还没有记忆。和 sous 聊聊你的习惯和项目，它会自己记下来。'}</li>
      {/if}
    {/each}
  </ul>
</div>

{#snippet editor()}
  <select bind:value={draftKind} aria-label="类型">
    {#each kinds as k}<option value={k}>{kindNames[k]}</option>{/each}
  </select>
  <textarea rows="3" bind:value={draftContent} placeholder="一条记忆只写一件事" aria-label="记忆内容"></textarea>
  <div class="row-actions">
    <button class="link" onclick={() => (editing = null)}>取消</button>
    <button class="btn small" disabled={!draftContent.trim() || saving} onclick={save}>保存</button>
  </div>
{/snippet}

<style>
  .backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / .35); z-index: 30; }
  .sheet {
    position: fixed; z-index: 31; top: 6vh; left: 50%; transform: translateX(-50%);
    width: min(640px, calc(100vw - 32px)); max-height: 88vh; display: flex; flex-direction: column;
    background: var(--panel); border: 1px solid var(--line); border-radius: 18px; padding: 16px 18px;
  }
  header { display: flex; align-items: center; justify-content: space-between; }
  h2 { margin: 0; font-size: 18px; }
  .hint { color: var(--muted); font-size: 13px; margin: 4px 0 12px; }
  .tools { display: flex; gap: 8px; }
  input[type='search'] {
    flex: 1; min-width: 0; border: 1px solid var(--line); background: var(--bg); border-radius: 10px; padding: 8px 12px; outline: 0;
  }
  input[type='search']:focus, textarea:focus, select:focus { border-color: var(--accent); }
  .btn.small { padding: 7px 16px; font-size: 14px; }
  .tabs { display: flex; gap: 4px; margin: 10px 0 6px; overflow-x: auto; }
  .tabs button {
    border: 0; background: transparent; color: var(--muted); padding: 5px 12px; border-radius: 999px; white-space: nowrap; font-size: 14px;
  }
  .tabs button.on { background: var(--sunken); color: var(--text); font-weight: 600; }
  ul { list-style: none; margin: 0; padding: 0; overflow-y: auto; display: grid; gap: 8px; }
  li { border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; display: grid; gap: 4px; }
  li.edit { gap: 8px; }
  li.empty { color: var(--muted); text-align: center; border-style: dashed; padding: 24px 12px; }
  .kind { font-size: 12px; color: var(--warm); font-weight: 600; }
  .content { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
  .row-actions { display: flex; justify-content: flex-end; gap: 14px; align-items: center; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  textarea, select {
    width: 100%; border: 1px solid var(--line); background: var(--bg); border-radius: 10px; padding: 8px 10px; outline: 0; resize: vertical;
  }
  select { width: auto; justify-self: start; }
  .error { color: var(--danger); font-size: 13px; margin: 0 0 8px; }
  @media (max-width: 760px) {
    .sheet { top: auto; bottom: 0; left: 0; transform: none; width: 100%; max-height: 90vh; border-radius: 18px 18px 0 0; padding-bottom: calc(16px + env(safe-area-inset-bottom)); }
  }
</style>
