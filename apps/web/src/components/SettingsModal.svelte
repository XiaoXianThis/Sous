<script lang="ts">
  // Everything configured once and changed now and then, in one full-screen
  // dialog. The panels are the same components that used to be separate
  // dialogs; here they render in place.
  import { ui } from '../lib/app.svelte'
  import Icon from './Icon.svelte'
  import Machines from './Machines.svelte'
  import Memories from './Memories.svelte'
  import Personas from './Personas.svelte'
  import Schedules from './Schedules.svelte'
  import Settings from './Settings.svelte'
  import Usage from './Usage.svelte'

  let { onLogout }: { onLogout: () => void } = $props()

  const tabs = [
    { key: 'machines', text: '机器', icon: 'laptop' },
    { key: 'personas', text: '岗位', icon: 'user' },
    { key: 'schedules', text: '定时任务', icon: 'calendar' },
    { key: 'memories', text: '记忆', icon: 'bulb' },
    { key: 'usage', text: '用量', icon: 'chart' },
    { key: 'settings', text: '通知与默认', icon: 'bell' },
    { key: 'account', text: '账号', icon: 'user' },
  ]
  const close = () => (ui.settings = '')
  const statusText = { connecting: '连接中', online: '已连接', offline: '离线，正在重连' }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && !document.querySelector('.embed > .backdrop:not(:first-child)') && close()} />

<div class="modal" role="dialog" aria-modal="true" aria-label="设置">
  <nav aria-label="设置分类">
    <header>
      <h2>设置</h2>
      <button class="icon-btn mobile-close" aria-label="关闭设置" onclick={close}><Icon name="x" /></button>
    </header>
    {#each tabs as t}
      <button class="tab" class:on={ui.settings === t.key} aria-current={ui.settings === t.key ? 'page' : undefined} onclick={() => (ui.settings = t.key)}>
        <Icon name={t.icon} size={16} />{t.text}
      </button>
    {/each}
  </nav>
  <div class="body">
    <button class="icon-btn close" aria-label="关闭设置" onclick={close}><Icon name="x" /></button>
    <h2 class="title">{tabs.find((t) => t.key === ui.settings)?.text}</h2>
    <div class="embed">
      {#if ui.settings === 'machines'}<Machines onClose={close} />
      {:else if ui.settings === 'personas'}<Personas onClose={close} />
      {:else if ui.settings === 'schedules'}<Schedules onClose={close} />
      {:else if ui.settings === 'memories'}<Memories onClose={close} />
      {:else if ui.settings === 'usage'}<Usage onClose={close} />
      {:else if ui.settings === 'settings'}<Settings onClose={close} />
      {:else}
        <div class="account">
          <p><span class="dot {ui.status}"></span>{statusText[ui.status]}</p>
          <button class="btn ghost" onclick={onLogout}><Icon name="logout" size={16} />退出登录</button>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .modal {
    position: fixed; inset: 0; z-index: 28; background: var(--bg); display: grid; grid-template-columns: 220px 1fr;
  }
  nav { background: var(--sunken); border-right: 1px solid var(--line); padding: 14px 10px; display: grid; align-content: start; gap: 2px; padding-top: calc(14px + env(safe-area-inset-top)); }
  nav header { display: flex; align-items: center; justify-content: space-between; padding: 0 6px 10px 10px; }
  nav h2 { margin: 0; font-size: 18px; }
  .tab { display: flex; gap: 10px; align-items: center; border: 0; background: none; text-align: left; padding: 9px 12px; border-radius: 10px; color: var(--muted); }
  .tab:hover { background: var(--line); color: var(--text); }
  .tab.on { background: var(--panel); color: var(--text); font-weight: 600; box-shadow: 0 0 0 1px var(--line); }
  .body { position: relative; overflow-y: auto; padding: 18px 28px 40px; padding-top: calc(18px + env(safe-area-inset-top)); }
  .title { margin: 0 0 14px; font-size: 20px; }
  .close { position: absolute; top: calc(12px + env(safe-area-inset-top)); right: 14px; }
  .mobile-close { display: none; }
  .embed { max-width: 760px; }
  .account { display: grid; gap: 12px; justify-items: start; }
  .account p { margin: 0; display: flex; gap: 8px; align-items: center; }
  .account .btn { display: inline-flex; gap: 6px; align-items: center; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--muted); }
  .dot.online { background: var(--ok); }
  .dot.offline { background: var(--danger); }
  @media (max-width: 760px) {
    .modal { grid-template-columns: 1fr; grid-template-rows: auto 1fr; }
    nav { display: flex; flex-wrap: nowrap; overflow-x: auto; gap: 4px; padding: 10px; padding-top: calc(10px + env(safe-area-inset-top)); border-right: 0; border-bottom: 1px solid var(--line); align-items: center; }
    nav header { padding: 0 8px 0 4px; flex: none; }
    nav h2 { display: none; }
    .mobile-close { display: inline-grid; }
    .tab { flex: none; padding: 6px 10px; }
    .body { padding: 14px 16px 30px; }
    .close, .title { display: none; }
  }
</style>
