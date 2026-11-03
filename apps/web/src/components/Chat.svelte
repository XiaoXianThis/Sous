<script lang="ts">
  import { tick } from 'svelte'
  import { chat, newThread, openThread, send, ui } from '../lib/app.svelte'
  import { isAnswering } from '../lib/reduce'
  import MessageView from './Message.svelte'

  let { onLogout }: { onLogout: () => void } = $props()

  let draft = $state('')
  let sending = $state(false)
  let sendError = $state('')
  let drawer = $state(false)
  let scroller: HTMLElement | undefined = $state()
  let input: HTMLTextAreaElement | undefined = $state()
  let stick = true

  const thread = $derived(chat.threads.find((t) => t.id === ui.current))
  const messages = $derived(chat.messages[ui.current] ?? [])
  const answering = $derived(isAnswering(chat, ui.current))
  const lastLength = $derived(messages.length ? messages[messages.length - 1].content.length : 0)

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
          class:active={t.id === ui.current}
          aria-current={t.id === ui.current ? 'page' : undefined}
          aria-label={t.title}
          onclick={() => select(t.id)}
        >
          {#if t.kind === 'main'}<span class="dot" aria-hidden="true"></span>{/if}
          <span class="title">{t.title}</span>
        </button>
      {/each}
    </nav>
    <footer class="side-foot">
      <span class="status {ui.status}">{statusText[ui.status]}</span>
      <button class="link" onclick={onLogout}>退出</button>
    </footer>
  </aside>
  <button class="scrim" aria-label="关闭列表" onclick={() => (drawer = false)}></button>

  <section class="main">
    <header class="top">
      <button class="icon-btn menu" aria-label="对话列表" onclick={() => (drawer = true)}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 7h16M4 12h16M4 17h16"/></svg>
      </button>
      <h2>{thread?.title ?? ''}</h2>
      <span class="status-dot {ui.status}" title={statusText[ui.status]}></span>
    </header>

    <div class="messages" bind:this={scroller} onscroll={onScroll}>
      <div class="inner">
        {#if messages.length === 0}
          <div class="empty">
            {#if thread?.kind === 'main'}
              <p class="big">有什么要交给我的？</p>
              <p>这里是主线：日常对话、主动汇报和系统消息都会出现在这里。</p>
            {:else}
              <p class="big">新对话</p>
            {/if}
          </div>
        {/if}
        {#each messages as m (m.id)}
          <MessageView message={m} />
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
