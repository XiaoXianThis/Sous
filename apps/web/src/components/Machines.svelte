<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import type { Node } from '../lib/types'

  let { onClose }: { onClose: () => void } = $props()

  let nodes = $state<Node[]>([])
  let loading = $state(true)
  let error = $state('')
  let code = $state<{ code: string; expires_at: number } | null>(null)
  let renaming = $state<string | null>(null)
  let draftName = $state('')

  async function load() {
    try {
      nodes = (await api.nodes()).nodes
      error = ''
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }

  $effect(() => {
    void chat.nodesRev
    load()
  })

  async function pair() {
    try {
      code = await api.pairingCode()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function rename(n: Node) {
    if (!draftName.trim()) return
    try {
      await api.renameNode(n.id, draftName.trim())
      renaming = null
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function remove(n: Node) {
    if (!confirm(`删除机器“${n.name}”？\n\n它会立刻断开，之后需要重新配对才能使用。`)) return
    try {
      await api.removeNode(n.id)
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  const pairCommand = $derived(code ? `sous pair ${location.origin} ${code.code}` : '')
  const osName: Record<string, string> = { darwin: 'macOS', linux: 'Linux', windows: 'Windows' }
  function ago(ts: number) {
    if (!ts) return ''
    const m = Math.round((Date.now() - ts) / 60000)
    if (m < 1) return '刚刚'
    if (m < 60) return `${m} 分钟前`
    if (m < 1440) return `${Math.round(m / 60)} 小时前`
    return `${Math.round(m / 1440)} 天前`
  }
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onClose()} />

<div class="backdrop" role="presentation" onclick={onClose}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label="机器">
  <header>
    <h2>机器</h2>
    <button class="icon-btn" aria-label="关闭" onclick={onClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
    </button>
  </header>
  <p class="hint">装了 sous 客户端的电脑。sous 会把编程任务派到这些电脑上的 agent 去做。</p>

  {#if code}
    <div class="pair">
      <p>在要添加的电脑上运行（10 分钟内有效）：</p>
      <code class="cmd">{pairCommand}</code>
      <div class="row">
        <button class="btn small ghost" onclick={() => navigator.clipboard?.writeText(pairCommand)}>复制命令</button>
        <button class="link" onclick={() => (code = null)}>完成</button>
      </div>
    </div>
  {:else}
    <button class="btn small add" onclick={pair}>配对新机器</button>
  {/if}

  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <ul>
    {#each nodes as n (n.id)}
      <li>
        <div class="head">
          <span class="dot" class:on={n.online}></span>
          {#if renaming === n.id}
            <input bind:value={draftName} aria-label="机器名" onkeydown={(e) => e.key === 'Enter' && rename(n)} />
            <button class="link" onclick={() => rename(n)}>保存</button>
            <button class="link" onclick={() => (renaming = null)}>取消</button>
          {:else}
            <strong>{n.name}</strong>
            <span class="sub">{osName[n.os] ?? n.os} · {n.online ? '在线' : `离线 ${ago(n.last_seen)}`}</span>
          {/if}
        </div>
        {#if n.agents?.some((a) => a.installed)}
          <table>
            <tbody>
              {#each n.agents.filter((a) => a.installed) as a}
                <tr>
                  <td>{a.title}</td>
                  <td class="ver">{a.version ?? ''}</td>
                  <td class={a.logged_in === false ? 'bad' : a.logged_in ? 'good' : 'muted'}>
                    {a.logged_in === false ? '未登录' : a.logged_in ? '已登录' : '登录状态未知'}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else if n.online}
          <p class="sub">没有检测到可用的编程 agent</p>
        {/if}
        <div class="row-actions">
          <button class="link" onclick={() => { renaming = n.id; draftName = n.name }}>改名</button>
          <button class="link danger" onclick={() => remove(n)}>删除</button>
        </div>
      </li>
    {:else}
      {#if !loading}<li class="empty">还没有机器。点“配对新机器”，然后在电脑上安装 sous 客户端并运行配对命令。</li>{/if}
    {/each}
  </ul>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / .35); z-index: 30; }
  .sheet {
    position: fixed; z-index: 31; top: 6vh; left: 50%; transform: translateX(-50%);
    width: min(640px, calc(100vw - 32px)); max-height: 88vh; display: flex; flex-direction: column; gap: 10px;
    background: var(--panel); border: 1px solid var(--line); border-radius: 18px; padding: 16px 18px;
  }
  header { display: flex; align-items: center; justify-content: space-between; }
  h2 { margin: 0; font-size: 18px; }
  .hint { color: var(--muted); font-size: 13px; margin: 0; }
  .btn.small { padding: 7px 16px; font-size: 14px; }
  .add { justify-self: start; align-self: flex-start; }
  .pair { border: 1px dashed var(--accent); border-radius: 12px; padding: 10px 12px; display: grid; gap: 8px; }
  .pair p { margin: 0; font-size: 14px; }
  .cmd { font: 13px ui-monospace, Menlo, Consolas, monospace; background: var(--bg); padding: 8px 10px; border-radius: 8px; overflow-wrap: anywhere; user-select: all; }
  .row { display: flex; gap: 12px; align-items: center; }
  ul { list-style: none; margin: 0; padding: 0; overflow-y: auto; display: grid; gap: 8px; }
  li { border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; display: grid; gap: 6px; }
  li.empty { color: var(--muted); text-align: center; border-style: dashed; padding: 24px 12px; }
  .head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .head input { flex: 1; min-width: 0; border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 4px 8px; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--muted); flex: none; }
  .dot.on { background: #3a9d5d; }
  .sub { color: var(--muted); font-size: 13px; margin: 0; }
  table { border-collapse: collapse; font-size: 13px; }
  td { padding: 2px 12px 2px 0; }
  .ver { color: var(--muted); }
  .good { color: #3a9d5d; }
  .bad { color: var(--danger); }
  .muted { color: var(--muted); }
  .row-actions { display: flex; justify-content: flex-end; gap: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  .error { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) {
    .sheet { top: auto; bottom: 0; left: 0; transform: none; width: 100%; max-height: 90vh; border-radius: 18px 18px 0 0; padding-bottom: calc(16px + env(safe-area-inset-bottom)); }
  }
</style>
