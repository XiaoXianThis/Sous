<script lang="ts">
  import { api } from '../lib/api'
  import { categoryText } from '../lib/tasks'
  import type { Approval } from '../lib/types'

  let { approval, context = '' }: { approval: Approval; context?: string } = $props()

  let busy = $state(false)
  let error = $state('')
  let note = $state('')
  let withNote = $state(false)

  async function decide(optionId: string) {
    busy = true
    error = ''
    try {
      await api.decide(approval.id, optionId, note.trim())
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }
</script>

<div class="approval" class:ai={approval.review === 'ai'} role="group" aria-label="审批" data-testid="approval">
  <p class="ask">
    {#if approval.category}<span class="cat">{categoryText[approval.category] ?? approval.category}</span>{/if}
    {approval.review === 'ai' ? '审批 agent 正在判断：' : '需要你批准：'}<strong>{approval.title}</strong>
  </p>
  {#if context}<p class="ctx">{context}</p>{/if}
  {#if approval.escalation}<p class="esc">审批 agent 交给你：{approval.escalation}</p>{/if}
  {#if approval.detail}<pre class="detail">{approval.detail}</pre>{/if}
  {#if withNote}
    <input class="note" bind:value={note} placeholder="附一句话（会一起转给 agent）" aria-label="说明" />
  {/if}
  <div class="options">
    {#each approval.options as o}
      <button class={o.kind.startsWith('allow') ? 'btn small' : 'btn small ghost'} disabled={busy} onclick={() => decide(o.option_id)}>{o.name}</button>
    {/each}
    {#if !withNote}<button class="link" onclick={() => (withNote = true)}>附言</button>{/if}
  </div>
  {#if error}<p class="err" role="alert">{error}</p>{/if}
</div>

<style>
  .approval {
    border: 1px solid var(--warm); border-radius: 12px; padding: 10px 12px; display: grid; gap: 8px;
    background: color-mix(in srgb, var(--warm) 8%, var(--panel));
  }
  .approval.ai { border-style: dashed; }
  .ask, .ctx, .esc { margin: 0; overflow-wrap: anywhere; }
  .ctx { font-size: 13px; color: var(--muted); }
  .esc { font-size: 13px; color: var(--warm); }
  .cat {
    display: inline-block; font-size: 11px; font-weight: 600; border: 1px solid var(--line); border-radius: 999px;
    padding: 0 7px; margin-right: 6px; color: var(--muted); vertical-align: 1px;
  }
  .detail {
    margin: 0; max-height: 200px; overflow: auto; font: 12px/1.45 ui-monospace, Menlo, Consolas, monospace;
    background: var(--bg); border-radius: 8px; padding: 8px 10px; white-space: pre-wrap; overflow-wrap: anywhere;
  }
  .note { border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; }
  .options { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .btn.small { padding: 6px 14px; font-size: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .err { margin: 0; color: var(--danger); font-size: 13px; }
</style>
