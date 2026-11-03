<script lang="ts">
  import { onMount, tick } from 'svelte'
  import { chat, followHash, newThread, openThread, send, ui } from '../lib/app.svelte'
  import { isAnswering } from '../lib/reduce'
  import { api } from '../lib/api'
  import type { RunView } from '../lib/runs'
  import type { Message, Node } from '../lib/types'
  import Inbox from './Inbox.svelte'
  import Machines from './Machines.svelte'
  import Memories from './Memories.svelte'
  import MessageView from './Message.svelte'
  import Projects from './Projects.svelte'
  import RunCard from './RunCard.svelte'
  import Settings from './Settings.svelte'
  import TaskDetail from './TaskDetail.svelte'
  import Tasks from './Tasks.svelte'

  let { onLogout }: { onLogout: () => void } = $props()

  let draft = $state('')
  let sending = $state(false)
  let sendError = $state('')
  let drawer = $state(false)
  let scroller: HTMLElement | undefined = $state()
  let input: HTMLTextAreaElement | undefined = $state()
  let stick = true
  let showMemories = $state(false)
  let showMachines = $state(false)
  let showProjects = $state(false)
  let showSettings = $state(false)
  let nodes = $state<Node[]>([])

  // Links from push notifications.
  onMount(() => {
    followHash(location.hash)
    const onHash = () => followHash(location.hash)
    addEventListener('hashchange', onHash)
    // The service worker posts the link when a notification is clicked while the app is open.
    const onSW = (e: MessageEvent) => {
      if (e.data?.type === 'open' && typeof e.data.url === 'string') followHash(new URL(e.data.url, location.origin).hash)
    }
    navigator.serviceWorker?.addEventListener('message', onSW)
    return () => {
      removeEventListener('hashchange', onHash)
      navigator.serviceWorker?.removeEventListener('message', onSW)
    }
  })

  // Machine names for run cards; reloaded when a machine changes.
  $effect(() => {
    void chat.nodesRev
    api.nodes().then((r) => (nodes = r.nodes)).catch(() => {})
  })
  const nodeName = (id: string) => nodes.find((n) => n.id === id)?.name ?? '未知机器'

  type Item = { kind: 'message'; seq: number; message: Message } | { kind: 'run'; seq: number; run: RunView }
  const timeline = $derived.by((): Item[] => {
    const items: Item[] = messages.map((m) => ({ kind: 'message', seq: m.seq, message: m }))
    for (const r of Object.values(chat.runs)) {
      if (r.run.thread_id === ui.current) items.push({ kind: 'run', seq: r.seq || r.run.created_at, run: r })
    }
    return items.sort((a, b) => a.seq - b.seq)
  })
  let pendingApprovals = $state(0)
  $effect(() => {
    void chat.inboxRev
    api.approvals().then((r) => (pendingApprovals = r.approvals.filter((a) => a.review === 'user').length)).catch(() => {})
  })

  async function showInbox() {
    const main = chat.threads.find((t) => t.kind === 'main')
    if (main) await select(main.id)
    ui.focusInbox++
  }

  function showTasks() {
    drawer = false
    ui.view = 'tasks'
  }

  const thread = $derived(chat.threads.find((t) => t.id === ui.current))
  const messages = $derived(chat.messages[ui.current] ?? [])
  const answering = $derived(isAnswering(chat, ui.current))
  const lastLength = $derived(
    (messages.length ? messages[messages.length - 1].content.length : 0) +
      Object.values(chat.runs).reduce((n, r) => n + r.tools.length + r.approvals.length + (r.run.status.length), 0),
  )

  // Follow new content while the reader is at the bottom.
  $effect(() => {
    void messages.length
    void lastLength
    if (stick) tick().then(() => scroller?.scrollTo({ top: scroller.scrollHeight }))
  })

  function onScroll() {
    if (!scroller) return
    stick = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80
  }

  async function select(id: string) {
    drawer = false
    stick = true
    await openThread(id)
  }

  async function create() {
    drawer = false
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
      await tick() // let the textarea empty before measuring it
      resize()
    } catch (e) {
      sendError = (e as Error).message
    } finally {
      sending = false
      input?.focus()
    }
  }

  // On touch screens the return key breaks the line and the button sends.
  const touch = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches

  function onKey(e: KeyboardEvent) {
    // Enter sends; Shift+Enter breaks the line; never while an IME is composing.
    if (!touch && e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      submit()
    }
  }

  function resize() {
    if (!input) return
    input.style.height = 'auto'
    input.style.height = Math.min(input.scrollHeight, 200) + 'px'
  }

  const statusText = { connecting: '连接中', online: '在线', offline: '离线，正在重连' }
</script>

{#if showMemories}<Memories onClose={() => (showMemories = false)} />{/if}
{#if showMachines}<Machines onClose={() => (showMachines = false)} />{/if}
{#if showProjects}<Projects onClose={() => (showProjects = false)} />{/if}
{#if showSettings}<Settings onClose={() => (showSettings = false)} />{/if}
{#if ui.task}
  {#key ui.task}
    <TaskDetail id={ui.task} onClose={() => (ui.task = '')} onOpenTask={(id) => (ui.task = id)} onOpenThread={(id) => select(id)} />
  {/key}
{/if}

<div class="layout" class:drawer>
  <aside>
    <header class="side-head">
      <strong>sous</strong>
      <button class="icon-btn" title="新对话" aria-label="新对话" onclick={create}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>
      </button>
    </header>
    <nav>
      {#each chat.threads as t (t.id)}
        <button
          class="thread"
          class:active={ui.view === 'chat' && t.id === ui.current}
          aria-current={ui.view === 'chat' && t.id === ui.current ? 'page' : undefined}
          aria-label={t.title}
          onclick={() => select(t.id)}
        >
          {#if t.kind === 'main'}<span class="dot" aria-hidden="true"></span>{/if}
          <span class="title">{t.title}</span>
        </button>
      {/each}
    </nav>
    <div class="side-links">
      <button class="side-link" class:active={ui.view === 'tasks'} onclick={showTasks}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="7" height="16" rx="1.5"/><rect x="14" y="4" width="7" height="10" rx="1.5"/></svg>
        任务
      </button>
      <button class="side-link" onclick={() => { drawer = false; showProjects = true }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
        项目
      </button>
      <button class="side-link" onclick={() => { drawer = false; showMachines = true }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg>
        机器
      </button>
      <button class="side-link" onclick={() => { drawer = false; showMemories = true }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a6 6 0 0 0-3.5 10.9V17h7v-3.1A6 6 0 0 0 12 3zM9.5 21h5"/></svg>
        记忆
      </button>
      <button class="side-link" onclick={() => { drawer = false; showSettings = true }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>
        设置
      </button>
    </div>
    <footer class="side-foot">
      <span class="status {ui.status}">{statusText[ui.status]}</span>
      <button class="link" onclick={onLogout}>退出</button>
    </footer>
  </aside>
  <button class="scrim" aria-label="关闭列表" onclick={() => (drawer = false)}></button>

  {#if ui.view === 'tasks'}
    <Tasks onOpenTask={(id) => (ui.task = id)} onMenu={() => (drawer = true)} />
  {:else}
  <section class="main">
    <header class="top">
      <button class="icon-btn menu" aria-label="对话列表" onclick={() => (drawer = true)}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 7h16M4 12h16M4 17h16"/></svg>
      </button>
      <h2>{thread?.title ?? ''}</h2>
      {#if pendingApprovals > 0}<button class="pending" title="等你审批的操作" onclick={showInbox}>待审批 {pendingApprovals}</button>{/if}
      <span class="status-dot {ui.status}" title={statusText[ui.status]}></span>
    </header>

    <div class="messages" bind:this={scroller} onscroll={onScroll}>
      <div class="inner">
        {#if thread?.kind === 'main'}
          {#key ui.focusInbox}<Inbox onOpenTask={(id) => (ui.task = id)} focus={ui.focusInbox > 0} />{/key}
        {/if}
        {#if timeline.length === 0}
          <div class="empty">
            {#if thread?.kind === 'main'}
              <p class="big">有什么要交给我的？</p>
              <p>这里是主线：日常对话、主动汇报和系统消息都会出现在这里。</p>
            {:else}
              <p class="big">新对话</p>
            {/if}
          </div>
        {/if}
        {#each timeline as item (item.kind === 'message' ? item.message.id : item.run.run.id)}
          {#if item.kind === 'message'}
            <MessageView message={item.message} />
          {:else}
            <RunCard view={item.run} {nodeName} />
          {/if}
        {/each}
        {#if answering && !messages.some((m) => m.streaming)}
          <div class="typing" aria-label="正在思考"><span></span><span></span><span></span></div>
        {/if}
      </div>
    </div>

    <form class="composer" onsubmit={(e) => { e.preventDefault(); submit() }}>
      {#if sendError}<p class="error" role="alert">发送失败：{sendError}</p>{/if}
      <div class="box">
        <textarea
          bind:this={input}
          bind:value={draft}
          rows="1"
          placeholder="说点什么…"
          title={touch ? undefined : 'Enter 发送，Shift+Enter 换行'}
          oninput={resize}
          onkeydown={onKey}
          aria-label="消息"
        ></textarea>
        <button class="send" type="submit" disabled={!draft.trim() || sending} aria-label="发送">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg>
        </button>
      </div>
    </form>
  </section>
  {/if}
</div>

<style>
  .layout { height: 100%; display: grid; grid-template-columns: 260px 1fr; }
  aside {
    background: var(--sunken); border-right: 1px solid var(--line);
    display: flex; flex-direction: column; min-height: 0;
  }
  .side-head { display: flex; align-items: center; justify-content: space-between; padding: 14px 12px 8px 18px; }
  .side-head strong { font-size: 18px; letter-spacing: .02em; }
  nav { flex: 1; overflow-y: auto; padding: 4px 8px; display: grid; align-content: start; gap: 2px; }
  .thread {
    display: flex; align-items: center; gap: 8px; width: 100%; text-align: left;
    border: 0; background: transparent; padding: 9px 12px; border-radius: 10px; color: var(--text);
  }
  .thread:hover { background: var(--line); }
  .thread.active { background: var(--panel); box-shadow: 0 0 0 1px var(--line); font-weight: 600; }
  .thread .title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--warm); flex: none; }
  .side-links { padding: 4px 8px; display: grid; gap: 2px; border-top: 1px solid var(--line); }
  .side-link {
    display: flex; align-items: center; gap: 8px; border: 0; background: transparent; color: var(--muted);
    padding: 8px 12px; border-radius: 10px; text-align: left; font-size: 14px;
  }
  .side-link:hover { background: var(--line); color: var(--text); }
  .side-link.active { background: var(--panel); color: var(--text); box-shadow: 0 0 0 1px var(--line); font-weight: 600; }
  .side-foot {
    display: flex; justify-content: space-between; align-items: center;
    padding: 12px 18px calc(12px + env(safe-area-inset-bottom)); font-size: 13px; color: var(--muted);
  }
  .link { border: 0; background: none; color: var(--muted); padding: 0; }
  .link:hover { color: var(--text); }
  .status::before { content: ''; display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-right: 6px; background: var(--muted); vertical-align: 1px; }
  .status.online::before, .status-dot.online { background: #3a9d5d; }
  .status.offline::before, .status-dot.offline { background: var(--danger); }
  .scrim { display: none; }

  .main { display: flex; flex-direction: column; min-width: 0; min-height: 0; background: var(--bg); }
  .top {
    display: flex; align-items: center; gap: 8px; padding: 10px 16px;
    padding-top: calc(10px + env(safe-area-inset-top)); border-bottom: 1px solid var(--line);
  }
  .top h2 { margin: 0; font-size: 16px; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .menu { display: none; }
  .status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--muted); }
  .pending { font-size: 12px; font-weight: 600; color: var(--warm); border: 1px solid var(--warm); background: transparent; border-radius: 999px; padding: 1px 8px; white-space: nowrap; }

  .messages { flex: 1; overflow-y: auto; overscroll-behavior: contain; }
  .inner { max-width: 760px; margin: 0 auto; padding: 20px 16px 8px; display: grid; gap: 14px; }
  .empty { text-align: center; color: var(--muted); padding: 18vh 8px 0; }
  .empty .big { font-size: 22px; color: var(--text); margin: 0 0 6px; font-weight: 600; }
  .typing { display: flex; gap: 4px; padding: 10px 2px; }
  .typing span { width: 7px; height: 7px; border-radius: 50%; background: var(--muted); animation: blink 1.2s infinite ease-in-out; }
  .typing span:nth-child(2) { animation-delay: .15s; }
  .typing span:nth-child(3) { animation-delay: .3s; }
  @keyframes blink { 0%, 80%, 100% { opacity: .25 } 40% { opacity: 1 } }

  .composer { padding: 8px 16px calc(14px + env(safe-area-inset-bottom)); max-width: 792px; width: 100%; margin: 0 auto; }
  .box {
    display: flex; align-items: flex-end; gap: 8px; background: var(--panel);
    border: 1px solid var(--line); border-radius: 22px; padding: 6px 6px 6px 16px;
  }
  .box:focus-within { border-color: var(--accent); }
  textarea {
    flex: 1; border: 0; outline: 0; resize: none; background: transparent;
    padding: 8px 0; max-height: 200px; line-height: 1.5;
  }
  .send {
    width: 36px; height: 36px; border-radius: 50%; border: 0; flex: none;
    background: var(--accent); color: var(--accent-text); display: grid; place-items: center;
  }
  .error { color: var(--danger); font-size: 13px; margin: 0 0 6px 8px; }

  @media (max-width: 760px) {
    .layout { grid-template-columns: 1fr; }
    aside {
      position: fixed; inset: 0 auto 0 0; width: min(82vw, 300px); z-index: 20;
      transform: translateX(-100%); transition: transform .2s ease; padding-top: env(safe-area-inset-top);
    }
    .scrim { position: fixed; inset: 0; z-index: 10; border: 0; background: rgb(0 0 0 / .35); }
    .layout.drawer aside { transform: none; }
    .layout.drawer .scrim { display: block; }
    .menu { display: inline-grid; margin-left: -8px; }
    .inner { padding: 16px 12px 8px; }
    .composer { padding-left: 12px; padding-right: 12px; }
  }
</style>
