<script lang="ts">
  // The app after login (work/specs/m5-workbench R5–R8): an icon rail, the
  // workbench in the middle, the chat with sous on the right. On a phone,
  // four bottom tabs: chat, overview, inbox, settings.
  import { onMount } from 'svelte'
  import { api } from '../lib/api'
  import { chat, followHash, go, loadOverview, ui, wb } from '../lib/app.svelte'
  import ChatPanel from './ChatPanel.svelte'
  import Icon from './Icon.svelte'
  import Inbox from './Inbox.svelte'
  import Overview from './Overview.svelte'
  import ProjectBoard from './ProjectBoard.svelte'
  import ProjectsList from './ProjectsList.svelte'
  import SettingsModal from './SettingsModal.svelte'
  import TaskView from './TaskView.svelte'

  let { onLogout }: { onLogout: () => void } = $props()

  onMount(() => {
    followHash(location.hash)
    const onHash = () => followHash(location.hash)
    addEventListener('hashchange', onHash)
    addEventListener('popstate', onHash)
    const onSW = (e: MessageEvent) => {
      if (e.data?.type === 'open' && typeof e.data.url === 'string') followHash(new URL(e.data.url, location.origin).hash)
    }
    navigator.serviceWorker?.addEventListener('message', onSW)
    return () => {
      removeEventListener('hashchange', onHash)
      removeEventListener('popstate', onHash)
      navigator.serviceWorker?.removeEventListener('message', onSW)
    }
  })

  // The overview data follows tasks, machines, approvals and runs.
  let timer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    void chat.tasksRev
    void chat.nodesRev
    void chat.inboxRev
    clearTimeout(timer)
    timer = setTimeout(loadOverview, 150)
    return () => clearTimeout(timer)
  })
  // Runs starting and ending change the cells too.
  const runKey = $derived(Object.values(chat.runs).map((r) => r.run.status + (r.run.paused ? 'p' : '')).join())
  $effect(() => {
    void runKey
    clearTimeout(timer)
    timer = setTimeout(loadOverview, 300)
  })

  let pending = $state(0)
  $effect(() => {
    void chat.inboxRev
    api.inbox().then((r) => (pending = r.approvals.filter((a) => a.review === 'user').length + r.tasks.length)).catch(() => {})
  })

  const view = $derived(ui.route.view)
  const rail = [
    { key: 'overview', icon: 'grid', text: '总览' },
    { key: 'projects', icon: 'folders', text: '项目' },
    { key: 'inbox', icon: 'inbox', text: '待处理' },
  ] as const
  const active = (k: string) => view === k || (k === 'projects' && (view === 'project' || view === 'task'))
</script>

<div class="shell" class:chat-closed={!ui.chatOpen} data-tab={ui.tab}>
  <nav class="rail" aria-label="主导航">
    <span class="brand" title="sous"><Icon name="wok" size={22} /></span>
    {#each rail as r}
      <button class="ri" class:on={active(r.key)} aria-current={active(r.key) ? 'page' : undefined} title={r.text} aria-label={r.text}
        onclick={() => go({ view: r.key, id: '' })}>
        <Icon name={r.icon} size={20} />
        {#if r.key === 'inbox' && pending > 0}<span class="badge">{pending}</span>{/if}
      </button>
    {/each}
    <span class="sp"></span>
    {#if !ui.chatOpen}
      <button class="ri" title="打开对话" aria-label="打开对话" onclick={() => (ui.chatOpen = true)}><Icon name="chat" size={20} /></button>
    {/if}
    <button class="ri" title="设置" aria-label="设置" onclick={() => (ui.settings = 'machines')}><Icon name="settings" size={20} /></button>
  </nav>

  <main class="work" aria-label="工作台">
    {#key view + ui.route.id}
      {#if view === 'project'}<ProjectBoard id={ui.route.id} />
      {:else if view === 'task'}<TaskView id={ui.route.id} />
      {:else if view === 'projects'}<ProjectsList />
      {:else if view === 'inbox'}
        <div class="inbox-page">
          <h1>待处理</h1>
          <Inbox onOpenTask={(id) => go({ view: 'task', id })} />
          {#if pending === 0}<p class="calm"><Icon name="check" size={18} />现在没有需要你处理的事。</p>{/if}
        </div>
      {:else}<Overview />{/if}
    {/key}
  </main>

  <aside class="side">
    <ChatPanel onCollapse={() => (ui.chatOpen = false)} />
  </aside>

  <nav class="tabs" aria-label="主导航">
    <button class:on={ui.tab === 'chat'} onclick={() => (ui.tab = 'chat')}><Icon name="chat" size={20} /><span>对话</span></button>
    <button class:on={ui.tab === 'work' && view !== 'inbox'} onclick={() => { ui.tab = 'work'; if (view === 'inbox') go({ view: 'overview', id: '' }) }}><Icon name="grid" size={20} /><span>总览</span></button>
    <button class:on={ui.tab === 'inbox' || (ui.tab === 'work' && view === 'inbox')} onclick={() => go({ view: 'inbox', id: '' })}>
      <Icon name="inbox" size={20} /><span>待处理</span>{#if pending > 0}<span class="badge">{pending}</span>{/if}
    </button>
    <button onclick={() => (ui.settings = 'machines')}><Icon name="settings" size={20} /><span>设置</span></button>
  </nav>
</div>

{#if ui.settings}<SettingsModal {onLogout} />{/if}
{#if wb.error && !wb.data}<p class="toast" role="alert">加载工作台失败：{wb.error}</p>{/if}

<style>
  .shell { height: 100%; display: grid; grid-template-columns: 56px minmax(0, 1fr) minmax(320px, 400px); }
  .shell.chat-closed { grid-template-columns: 56px minmax(0, 1fr) 0; }
  .shell.chat-closed .side { display: none; }
  .rail {
    background: var(--sunken); border-right: 1px solid var(--line); display: flex; flex-direction: column; align-items: center;
    gap: 6px; padding: 12px 0 calc(12px + env(safe-area-inset-bottom)); padding-top: calc(12px + env(safe-area-inset-top));
  }
  .brand { color: var(--accent); margin-bottom: 10px; }
  .ri { position: relative; width: 40px; height: 40px; border-radius: 12px; border: 0; background: none; color: var(--muted); display: grid; place-items: center; }
  .ri:hover { background: var(--line); color: var(--text); }
  .ri.on { background: var(--panel); color: var(--text); box-shadow: 0 0 0 1px var(--line); }
  .badge {
    position: absolute; top: 2px; right: 0; min-width: 16px; height: 16px; border-radius: 8px; padding: 0 4px;
    background: var(--danger); color: var(--bg); font-size: 11px; line-height: 16px; text-align: center; font-weight: 600;
  }
  .sp { flex: 1; }
  .work { overflow-y: auto; min-width: 0; min-height: 0; background: var(--bg); padding-top: env(safe-area-inset-top); }
  .side { border-left: 1px solid var(--line); min-height: 0; min-width: 0; }
  .tabs { display: none; }
  .inbox-page { padding: 18px 22px 28px; display: grid; gap: 12px; max-width: 820px; }
  .inbox-page h1 { margin: 0; font-size: 20px; }
  .calm { display: flex; gap: 8px; align-items: center; color: var(--muted); margin: 0; }
  .toast { position: fixed; bottom: 16px; left: 50%; transform: translateX(-50%); background: var(--panel); border: 1px solid var(--danger); color: var(--danger); padding: 8px 14px; border-radius: 10px; margin: 0; z-index: 40; }

  @media (max-width: 1100px) {
    .shell { grid-template-columns: 56px minmax(0, 1fr) minmax(300px, 340px); }
  }
  @media (max-width: 760px) {
    .shell, .shell.chat-closed { grid-template-columns: 1fr; grid-template-rows: 1fr auto; }
    .rail { display: none; }
    .work, .side { grid-row: 1; grid-column: 1; border: 0; }
    .shell .side { display: block; }
    .shell[data-tab='chat'] .work { display: none; }
    .shell:not([data-tab='chat']) .side { display: none; }
    .shell .side :global(.icon-btn[aria-label='收起对话']) { display: none; }
    .tabs {
      display: grid; grid-template-columns: repeat(4, 1fr); border-top: 1px solid var(--line); background: var(--sunken);
      padding-bottom: env(safe-area-inset-bottom);
    }
    .tabs button { position: relative; border: 0; background: none; display: grid; justify-items: center; gap: 1px; padding: 7px 0 6px; color: var(--muted); font-size: 11px; }
    .tabs button.on { color: var(--accent); }
    .tabs .badge { top: 2px; right: calc(50% - 22px); }
    .inbox-page { padding: 14px 16px 24px; }
  }
</style>
