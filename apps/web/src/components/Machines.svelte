<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { jobTitle, latestJob } from '../lib/jobs'
  import type { AgentInfo, Node, Updates } from '../lib/types'
  import AgentPanel from './AgentPanel.svelte'
  import ConfigEditor from './ConfigEditor.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let nodes = $state<Node[]>([])
  let loading = $state(true)
  let error = $state('')
  let code = $state<{ code: string; expires_at: number } | null>(null)
  let renaming = $state<string | null>(null)
  let draftName = $state('')
  let updates = $state<Updates | null>(null)
  let updating = $state<Record<string, string>>({})

  async function loadUpdates(refresh = false) {
    try {
      updates = await api.updates(refresh)
    } catch (e) {
      error = (e as Error).message
    }
  }
  loadUpdates()

  async function update(n: Node) {
    const tag = updates?.latest?.tag ?? ''
    if (!confirm(`把“${n.name}”上的 sous 更新到 ${tag}？\n\n它会下载新版本、替换自己并重启；正在执行的任务会在重启后接着做。`)) return
    try {
      const r = await api.updateNode(n.id)
      updating[n.id] = `正在更新到 ${r.version}…`
    } catch (e) {
      error = (e as Error).message
    }
  }
  const nodeVersion = (id: string) => updates?.nodes.find((x) => x.id === id)

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

  // M5: role and workspaces.
  let wsEdit = $state<string | null>(null)
  let wsDraft = $state('')
  let wsBusy = $state(false)
  async function saveWorkspaces(n: Node) {
    wsBusy = true
    try {
      await api.setWorkspaces(n.id, wsDraft.split(/[\n,]/).map((x) => x.trim()).filter(Boolean))
      wsEdit = null
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      wsBusy = false
    }
  }
  async function makeServer(n: Node) {
    if (!confirm(`把“${n.name}”设为服务器？\n\n服务器只接运维任务（部署、重启、查服务），不能用完全开放模式，改文件、删除、启停服务都要你批准。\n改回开发机只能在那台机器的 node.json 里删掉 "role"。`)) return
    try {
      await api.setRole(n.id, 'server')
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  let panel = $state<{ node: string; agent: string } | null>(null)
  let nodeFile = $state<string | null>(null)
  let detecting = $state<Record<string, boolean>>({})
  const panelNode = $derived(panel ? nodes.find((n) => n.id === panel!.node) : undefined)
  const fileNode = $derived(nodeFile ? nodes.find((n) => n.id === nodeFile) : undefined)

  async function detect(n: Node) {
    detecting[n.id] = true
    try {
      await api.detectAgents(n.id)
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      detecting[n.id] = false
    }
  }

  // Installed agents first, then the ones that are not.
  const sortAgents = (as: AgentInfo[]) => [...as].sort((a, b) => Number(b.installed) - Number(a.installed))
  function summary(a: AgentInfo) {
    if (!a.installed || !a.defaults) return ''
    const parts: string[] = []
    for (const o of a.options ?? []) {
      const v = a.defaults[o.id]
      if (v && (o.category === 'model' || o.category === 'thought_level')) parts.push(o.values.find((x) => x.value === v)?.name ?? v)
    }
    return parts.length ? `· ${parts.join(' · ')}` : ''
  }
  // A job this page saw start, or one the machine reports (started before the page loaded).
  const jobOf = (n: Node, a: AgentInfo) => {
    const j = latestJob(chat.jobs, n.id, a.name)
    if (j) return j.running ? j : undefined
    return a.job
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

<svelte:window onkeydown={(e) => e.key === 'Escape' && !panel && !nodeFile && onClose()} />

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
      <p class="sub">服务器加上 <code>--role server</code>；加上 <code>--workspace ~/Code</code> 指定放项目的目录。</p>
      <div class="row">
        <button class="btn small ghost" onclick={() => navigator.clipboard?.writeText(pairCommand)}>复制命令</button>
        <button class="link" onclick={() => (code = null)}>完成</button>
      </div>
    </div>
  {:else}
    <button class="btn small add" onclick={pair}>配对新机器</button>
  {/if}

  {#if error}<p class="error" role="alert">{error}</p>{/if}

  {#if updates}
    <p class="versions">
      Hub {updates.hub.version}
      {#if updates.latest} · 最新发布 <a href={updates.latest.url} target="_blank" rel="noreferrer">{updates.latest.tag}</a>{:else if updates.error} · 检查更新失败：{updates.error}{/if}
      <button class="link" onclick={() => loadUpdates(true)}>检查更新</button>
    </p>
    {#if updates.hub.update_available}<p class="sub">Hub 有新版本：在服务器上按部署指南替换 sous-hub 并重启服务。</p>{/if}
  {/if}

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
            <span class="sub">{osName[n.os] ?? n.os} · {n.online ? '在线' : `离线 ${ago(n.last_seen)}`}{n.version ? ` · sous ${n.version}` : ''}</span>
          {/if}
        </div>
        {#if n.agents?.length}
          <table>
            <tbody>
              {#each sortAgents(n.agents) as a (a.name)}
                <tr class:absent={!a.installed}>
                  <td>{a.title}</td>
                  <td class="ver">{a.installed ? (a.version ?? '') : ''}{summary(a) ? ` ${summary(a)}` : ''}</td>
                  <td class={!a.installed ? 'muted' : a.logged_in === false ? 'bad' : a.logged_in ? 'good' : 'muted'}>
                    {!a.installed ? '未安装' : a.logged_in === false ? '未登录' : a.logged_in ? '已登录' : '登录状态未知'}
                  </td>
                  <td class="act">
                    {#if jobOf(n, a)}<span class="sub">正在{jobTitle[jobOf(n, a)?.kind ?? '']}…</span>{/if}
                    <button class="link accent" onclick={() => (panel = { node: n.id, agent: a.name })}>
                      {n.admin === 'on' && n.online && !a.installed && a.install ? '安装' : n.admin === 'on' && n.online && a.installed && a.logged_in === false && a.login ? '登录' : '管理'}
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else if n.online}
          <p class="sub">还没有收到这台机器的 agent 清单</p>
        {/if}
        <div class="ws">
          <span class="role" class:server={n.role === 'server'}>{n.role === 'server' ? '服务器' : '开发机'}</span>
          {#if wsEdit === n.id}
            <textarea bind:value={wsDraft} rows="2" placeholder="~/Code（每行一个）" aria-label="工作空间"></textarea>
            <button class="link accent" disabled={wsBusy} onclick={() => saveWorkspaces(n)}>保存</button>
            <button class="link" onclick={() => (wsEdit = null)}>取消</button>
          {:else}
            <span class="sub" title="sous 在这些目录里找 git 仓库，新项目也放在第一个目录里">工作空间：{n.workspaces?.length ? n.workspaces.join('、') : '未设置'}</span>
            {#if n.online && n.admin === 'on'}<button class="link" onclick={() => { wsEdit = n.id; wsDraft = (n.workspaces ?? []).join('\n') }}>修改</button>{/if}
            {#if n.role !== 'server'}<span class="sub">{n.wok ? `wok ${n.wok}` : '没装 wok'}</span>{/if}
          {/if}
          {#if n.role !== 'server' && n.online && n.admin === 'on' && wsEdit !== n.id}<button class="link" onclick={() => makeServer(n)}>设为服务器</button>{/if}
        </div>
        {#if n.online && n.admin === 'off'}<p class="sub">这台机器关闭了远程管理（node.json 里的 remote_admin）。</p>{/if}
        {#if n.online && n.admin === 'unsupported'}<p class="sub">更新这台机器上的 sous 后，就能在这里安装、登录和配置 agent。</p>{/if}
        <div class="row-actions">
          {#if n.online && n.admin === 'on'}
            <button class="link" disabled={detecting[n.id]} onclick={() => detect(n)}>{detecting[n.id] ? '正在检测…' : '重新检测'}</button>
            <button class="link" onclick={() => (nodeFile = n.id)}>node.json</button>
          {/if}
          {#if updating[n.id]}<span class="sub">{updating[n.id]}</span>
          {:else if nodeVersion(n.id)?.update_available && n.online}
            <button class="link accent" onclick={() => update(n)}>更新到 {updates?.latest?.tag}</button>
          {/if}
          <button class="link" onclick={() => { renaming = n.id; draftName = n.name }}>改名</button>
          <button class="link danger" onclick={() => remove(n)}>删除</button>
        </div>
      </li>
    {:else}
      {#if !loading}<li class="empty">还没有机器。点“配对新机器”，然后在电脑上安装 sous 客户端并运行配对命令。</li>{/if}
    {/each}
  </ul>
</div>

<!-- Opened from a machine, on top of this sheet. -->
{#if panel && panelNode}
  <AgentPanel node={panelNode} name={panel.agent} onClose={() => (panel = null)} />
{/if}
{#if nodeFile && fileNode}
  <ConfigEditor nodeId={fileNode.id} agent="sous" file="node" title={`${fileNode.name} · node.json`} onClose={() => (nodeFile = null)} />
{/if}

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
  table { width: 100%; }
  td { padding: 3px 12px 3px 0; }
  td.act { text-align: right; padding-right: 0; white-space: nowrap; }
  td.act .sub { margin-right: 8px; }
  tr.absent td:first-child { color: var(--muted); }
  .link:disabled { opacity: .5; }
  .ver { color: var(--muted); }
  .good { color: #3a9d5d; }
  .bad { color: var(--danger); }
  .muted { color: var(--muted); }
  .row-actions { display: flex; justify-content: flex-end; gap: 14px; }
  .ws { display: flex; flex-wrap: wrap; gap: 6px 12px; align-items: center; font-size: 13px; }
  .ws textarea { flex: 1; min-width: 200px; border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 4px 8px; font-size: 13px; }
  .role { font-size: 12px; border: 1px solid var(--line); border-radius: 6px; padding: 0 6px; }
  .role.server { border-style: dashed; color: var(--warm); border-color: var(--warm); }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  .link.accent { color: var(--accent); font-weight: 600; }
  .versions { margin: 0; font-size: 13px; color: var(--muted); display: flex; gap: 6px; flex-wrap: wrap; align-items: baseline; }
  .versions a { color: var(--accent); }
  .error { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) {
    .sheet { top: auto; bottom: 0; left: 0; transform: none; width: 100%; max-height: 90vh; border-radius: 18px 18px 0 0; padding-bottom: calc(16px + env(safe-area-inset-bottom)); }
  }
</style>
