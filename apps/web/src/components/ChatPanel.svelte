<script lang="ts">
  // The conversation with sous, beside the workbench. Runs show as one-line
  // chips; only what needs the user now (approvals) shows in full.
  import { tick } from 'svelte'
  import { chat, newThread, openThread, send, ui, wb } from '../lib/app.svelte'
  import { isAnswering, visibleMessages } from '../lib/reduce'
  import type { RunView } from '../lib/runs'
  import type { Message } from '../lib/types'
  import ApprovalCard from './ApprovalCard.svelte'
  import Icon from './Icon.svelte'
  import MessageView from './Message.svelte'
  import RunChip from './RunChip.svelte'

  let { onCollapse }: { onCollapse?: () => void } = $props()

  let draft = $state('')
  let sending = $state(false)
  let sendError = $state('')
  let scroller: HTMLElement | undefined = $state()
  let input: HTMLTextAreaElement | undefined = $state()
  let stick = true
  let picking = $state(false)

  const nodeName = (id: string) => wb.data?.nodes.find((n) => n.id === id)?.name ?? '机器'
  const thread = $derived(chat.threads.find((t) => t.id === ui.current))
  const messages = $derived(chat.messages[ui.current] ?? [])
  const answering = $derived(isAnswering(chat, ui.current))

  type Item = { kind: 'message'; seq: number; message: Message } | { kind: 'run'; seq: number; run: RunView }
  const timeline = $derived.by((): Item[] => {
    const items: Item[] = visibleMessages(messages).map((m) => ({ kind: 'message', seq: m.seq, message: m }))
    for (const r of Object.values(chat.runs)) {
      if (r.run.thread_id === ui.current) items.push({ kind: 'run', seq: r.seq || r.run.created_at, run: r })
    }
    return items.sort((a, b) => a.seq - b.seq)
  })
  const lastLength = $derived(
    (messages.length ? messages[messages.length - 1].content.length : 0) +
      Object.values(chat.runs).reduce((n, r) => n + r.approvals.length + r.run.status.length, 0),
  )

  // What the next message is about: the project or task on the workbench.
  const context = $derived.by(() => {
    if (ui.dropContext || !wb.data) return null
    const r = ui.route
    if (r.view === 'task') {
      const t = wb.data.tasks.find((x) => x.id === r.id)
      return { icon: 'circle', text: t ? t.title : '这个任务' }
    }
    if (r.view === 'project') {
      const p = wb.data.projects.find((x) => x.id === r.id)
      return p ? { icon: 'folders', text: p.name } : null
    }
    return null
  })

  $effect(() => {
    void messages.length
    void lastLength
    if (stick) tick().then(() => scroller?.scrollTo({ top: scroller.scrollHeight }))
  })

  function onScroll() {
    if (scroller) stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80
  }

  async function pick(id: string) {
    picking = false
    stick = true
    await openThread(id)
  }

  async function create() {
    picking = false
    await newThread()
    input?.focus()
  }

  async function submit() {
    const text = draft.trim()
    if (!text || sending) return
    sending = true
    sendError = ''
    try {
      await send(text)
      draft = ''
      stick = true
      await tick()
      resize()
    } catch (e) {
      sendError = (e as Error).message
    } finally {
      sending = false
      input?.focus()
    }
  }

  const touch = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches
  function onKey(e: KeyboardEvent) {
    if (!touch && e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      submit()
    }
  }
  function resize() {
    if (!input) return
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, 180) + 'px'
  }
  const statusText = { connecting: '连接中', online: '在线', offline: '离线，正在重连' }
</script>

<section class="chat" aria-label="和 sous 对话">
  <header class="top">
    <span class="logo" aria-hidden="true"><Icon name="wok" size={18} /></span>
    <div class="picker">
      <button class="title" onclick={() => (picking = !picking)} aria-expanded={picking} aria-haspopup="listbox">
        <span>{thread?.kind === 'main' ? 'sous' : (thread?.title ?? 'sous')}</span><Icon name="chevron" size={14} />
      </button>
      {#if picking}
        <div class="menu" role="listbox" aria-label="对话">
          {#each chat.threads as t (t.id)}
            <button role="option" aria-selected={t.id === ui.current} class:on={t.id === ui.current} onclick={() => pick(t.id)}>
              {#if t.kind === 'main'}<span class="dot"></span>主线{:else}{t.title}{/if}
            </button>
          {/each}
          <button class="new" onclick={create}><Icon name="plus" size={14} />新对话</button>
        </div>
      {/if}
    </div>
    <span class="status-dot {ui.status}" title={statusText[ui.status]}></span>
    <span class="sp"></span>
    {#if onCollapse}<button class="icon-btn" aria-label="收起对话" title="收起对话" onclick={onCollapse}><Icon name="panel" /></button>{/if}
  </header>

  <div class="messages" bind:this={scroller} onscroll={onScroll}>
    <div class="inner">
      {#if timeline.length === 0}
        <div class="empty">
          <p class="big">有什么要交给我的？</p>
          <p>告诉我要做什么，我来安排岗位、挑机器。</p>
        </div>
      {/if}
      {#each timeline as item (item.kind === 'message' ? item.message.id : item.run.run.id)}
        {#if item.kind === 'message'}
          <MessageView message={item.message} />
        {:else}
          <RunChip view={item.run} {nodeName} />
          {#each item.run.approvals.filter((a) => a.status === 'pending') as a (a.id)}
            <ApprovalCard approval={a} context={item.run.run.title} />
          {/each}
        {/if}
      {/each}
      {#if answering && !messages.some((m) => m.streaming)}
        <div class="typing" aria-label="正在思考"><span></span><span></span><span></span></div>
      {/if}
    </div>
  </div>

  <form class="composer" onsubmit={(e) => { e.preventDefault(); submit() }}>
    {#if sendError}<p class="error" role="alert">发送失败：{sendError}</p>{/if}
    {#if context}
      <span class="ctx" title="sous 会知道你在看这个">
        <Icon name={context.icon} size={12} />{context.text}
        <button type="button" aria-label="不带上这个" onclick={() => (ui.dropContext = true)}><Icon name="x" size={11} /></button>
      </span>
    {/if}
    <div class="box">
      <textarea bind:this={input} bind:value={draft} rows="1" placeholder="和 sous 说…" title={touch ? undefined : 'Enter 发送，Shift+Enter 换行'}
        oninput={resize} onkeydown={onKey} aria-label="消息"></textarea>
      <button class="send" type="submit" disabled={!draft.trim() || sending} aria-label="发送"><Icon name="send" /></button>
    </div>
  </form>
</section>

<svelte:window onclick={(e) => picking && !(e.target as HTMLElement).closest('.picker') && (picking = false)} />

<style>
  .chat { display: flex; flex-direction: column; min-height: 0; height: 100%; background: var(--bg); }
  .top { display: flex; align-items: center; gap: 6px; padding: 10px 10px 8px 14px; border-bottom: 1px solid var(--line); padding-top: calc(10px + env(safe-area-inset-top)); }
  .logo { color: var(--accent); display: grid; }
  .picker { position: relative; }
  .title { display: inline-flex; align-items: center; gap: 4px; border: 0; background: none; font-weight: 600; padding: 2px 4px; border-radius: 8px; }
  .title:hover { background: var(--sunken); }
  .menu {
    position: absolute; top: 100%; left: 0; z-index: 25; margin-top: 4px; min-width: 220px; max-height: 50vh; overflow-y: auto;
    background: var(--panel); border: 1px solid var(--line); border-radius: 12px; padding: 4px; display: grid; box-shadow: 0 8px 24px rgb(0 0 0 / .12);
  }
  .menu button { border: 0; background: none; text-align: left; padding: 7px 10px; border-radius: 8px; display: flex; gap: 6px; align-items: center; font-size: 14px; }
  .menu button:hover, .menu button.on { background: var(--sunken); }
  .menu .new { color: var(--accent); border-top: 1px solid var(--line); border-radius: 0 0 8px 8px; }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--warm); }
  .status-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); }
  .status-dot.online { background: var(--ok); }
  .status-dot.offline { background: var(--danger); }
  .sp { flex: 1; }
  .messages { flex: 1; overflow-y: auto; overscroll-behavior: contain; }
  .inner { padding: 14px 14px 8px; display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .empty { text-align: center; color: var(--muted); padding: 14vh 8px 0; font-size: 14px; }
  .empty .big { font-size: 18px; color: var(--text); margin: 0 0 4px; font-weight: 600; }
  .empty p { margin: 0; }
  .typing { display: flex; gap: 4px; padding: 8px 2px; }
  .typing span { width: 6px; height: 6px; border-radius: 50%; background: var(--muted); animation: blink 1.2s infinite ease-in-out; }
  .typing span:nth-child(2) { animation-delay: .15s; }
  .typing span:nth-child(3) { animation-delay: .3s; }
  @keyframes blink { 0%, 80%, 100% { opacity: .25 } 40% { opacity: 1 } }
  .composer { padding: 6px 12px calc(12px + env(safe-area-inset-bottom)); display: grid; gap: 6px; }
  .ctx { justify-self: start; display: inline-flex; gap: 4px; align-items: center; font-size: 12px; color: var(--muted); background: var(--sunken); border-radius: 999px; padding: 2px 4px 2px 9px; max-width: 100%; }
  .ctx button { border: 0; background: none; color: var(--muted); padding: 2px; display: grid; border-radius: 50%; }
  .ctx button:hover { color: var(--text); background: var(--line); }
  .box { display: flex; align-items: flex-end; gap: 8px; background: var(--panel); border: 1px solid var(--line); border-radius: 20px; padding: 5px 5px 5px 14px; }
  .box:focus-within { border-color: var(--accent); }
  textarea { flex: 1; border: 0; outline: 0; resize: none; background: transparent; padding: 7px 0; max-height: 180px; line-height: 1.5; }
  .send { width: 34px; height: 34px; border-radius: 50%; border: 0; flex: none; background: var(--accent); color: var(--accent-text); display: grid; place-items: center; }
  .error { color: var(--danger); font-size: 13px; margin: 0 0 0 8px; }
</style>
