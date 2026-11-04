<script lang="ts">
  // The app after login: an icon rail, the workbench (the only page), and the
  // chat with sous on the right. Task and project details open as sheets.
  // On a phone: three bottom tabs (workbench, chat, settings).
  import { onMount } from 'svelte'
  import { chat, closeSheet, followHash, go, loadInbox, loadOverview, openThread, ui, wb } from '../lib/app.svelte'
  import ChatPanel from './ChatPanel.svelte'
  import Icon from './Icon.svelte'
  import ProjectSheet from './ProjectSheet.svelte'
  import RunSheet from './RunSheet.svelte'
  import SettingsModal from './SettingsModal.svelte'
  import TaskDetail from './TaskDetail.svelte'
  import TaskSheet from './TaskSheet.svelte'
  import Workbench from './Workbench.svelte'

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

  // The workbench follows tasks, machines, approvals and Run lifecycle events.
  let timer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    void chat.tasksRev
    void chat.nodesRev
    void chat.inboxRev
    void chat.runsRev
    clearTimeout(timer)
    timer = setTimeout(() => {
      void loadOverview()
      void loadInbox()
    }, 150)
    return () => clearTimeout(timer)
  })

  const view = $derived(ui.route.view)
  const pending = $derived((wb.inbox?.approvals.filter((a) => a.review === 'user').length ?? 0) + (wb.inbox?.tasks.length ?? 0))
</script>

<div class="shell" class:chat-closed={!ui.chatOpen} data-tab={ui.tab}>
  <nav class="rail" aria-label="主导航">
    <span class="brand" title="sous"><Icon name="wok" size={22} /></span>
    <button class="ri on" title="工作台" aria-label="工作台" aria-current="page" onclick={() => go({ view: 'overview', id: '' })}>
      <Icon name="grid" size={20} />
      {#if pending > 0}<span class="badge">{pending}</span>{/if}
    </button>
    {#if !ui.chatOpen}
      <button class="ri" title="打开对话" aria-label="打开对话" onclick={() => (ui.chatOpen = true)}><Icon name="chat" size={20} /></button>
    {/if}
    <span class="sp"></span>
    <button class="ri" title="设置" aria-label="设置" onclick={() => (ui.settings = 'machines')}><Icon name="settings" size={20} /></button>
  </nav>

  <main class="work" aria-label="工作台">
    <Workbench />
  </main>

  <aside class="side">
    <ChatPanel onCollapse={() => (ui.chatOpen = false)} />
  </aside>

  <nav class="tabs" aria-label="主导航">
    <button class:on={ui.tab === 'work'} onclick={() => (ui.tab = 'work')}>
      <Icon name="grid" size={20} /><span>工作台</span>{#if pending > 0}<span class="badge">{pending}</span>{/if}
    </button>
    <button class:on={ui.tab === 'chat'} onclick={() => (ui.tab = 'chat')}><Icon name="chat" size={20} /><span>对话</span></button>
    <button onclick={() => (ui.settings = 'machines')}><Icon name="settings" size={20} /><span>设置</span></button>
  </nav>
</div>

{#if view === 'task'}<TaskSheet id={ui.route.id} onClose={closeSheet} />
{:else if view === 'project'}<ProjectSheet id={ui.route.id} onClose={closeSheet} />
{/if}

{#if ui.newTask}
  <TaskDetail id="new" projectId={ui.newTask} onClose={() => (ui.newTask = '')}
    onOpenTask={(id) => { ui.newTask = ''; go({ view: 'task', id }) }}
    onOpenThread={(tid) => { ui.newTask = ''; openThread(tid); ui.chatOpen = true }} />
{/if}

{#if ui.runSheet}<RunSheet id={ui.runSheet} onClose={() => (ui.runSheet = '')} />{/if}
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
      display: grid; grid-template-columns: repeat(3, 1fr); border-top: 1px solid var(--line); background: var(--sunken);
      padding-bottom: env(safe-area-inset-bottom);
    }
    .tabs button { position: relative; border: 0; background: none; display: grid; justify-items: center; gap: 1px; padding: 7px 0 6px; color: var(--muted); font-size: 11px; }
    .tabs button.on { color: var(--accent); }
    .tabs .badge { top: 2px; right: calc(50% - 22px); }
  }
</style>
