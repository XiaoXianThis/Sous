<script lang="ts">
  import type { Message } from '../lib/types'

  let { message }: { message: Message } = $props()
  let showThinking = $state(false)
  const time = $derived(
    new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }),
  )
</script>

{#if message.kind === 'silent'}
  <p class="silent" title={message.content || undefined}>已读，不需要回复{message.content ? `：${message.content}` : ''}</p>
{:else}
<article
  class="msg {message.role}"
  class:error={message.error}
  class:notice={message.kind === 'notice'}
  data-testid="message-{message.kind ?? message.role}"
>
  {#if message.kind === 'notice'}<span class="badge">提醒</span>{/if}
  {#if message.thinking}
    <button class="thinking-toggle" onclick={() => (showThinking = !showThinking)}>
      {showThinking ? '收起思考过程' : '查看思考过程'}
    </button>
    {#if showThinking}<div class="thinking">{message.thinking}</div>{/if}
  {/if}
  <div class="body">{message.content}{#if message.streaming}<span class="caret"></span>{/if}</div>
  {#if !message.streaming}<time>{time}</time>{/if}
</article>
{/if}

<style>
  .msg { display: grid; gap: 4px; max-width: 100%; }
  .msg.user { justify-items: end; }
  .body { white-space: pre-wrap; overflow-wrap: anywhere; }
  .user .body {
    background: var(--user-bubble); color: var(--user-text); padding: 9px 14px;
    border-radius: 18px 18px 4px 18px; max-width: min(85%, 560px);
  }
  .assistant .body { padding: 2px 2px; }
  .error .body { color: var(--danger); }
  .notice {
    border: 1px solid var(--line); border-left: 3px solid var(--warm); border-radius: 12px;
    background: var(--panel); padding: 10px 14px;
  }
  .badge {
    justify-self: start; font-size: 12px; font-weight: 600; color: var(--warm);
  }
  .silent {
    margin: -6px 0 0; font-size: 12px; color: var(--muted); padding: 0 4px;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  time { font-size: 12px; color: var(--muted); padding: 0 4px; }
  .caret {
    display: inline-block; width: 8px; height: 1em; margin-left: 2px; vertical-align: -2px;
    background: var(--accent); animation: caret 1s steps(2) infinite;
  }
  @keyframes caret { 50% { opacity: 0 } }
  .thinking-toggle {
    justify-self: start; border: 0; background: var(--sunken); color: var(--muted);
    font-size: 12px; padding: 3px 10px; border-radius: 999px;
  }
  .thinking {
    white-space: pre-wrap; font-size: 13px; color: var(--muted);
    border-left: 2px solid var(--line); padding: 2px 0 2px 10px;
  }
</style>
