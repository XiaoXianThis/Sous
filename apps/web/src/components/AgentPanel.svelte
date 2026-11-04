<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { jobTitle, latestJob } from '../lib/jobs'
  import type { AgentInfo, ConfigOption, Node } from '../lib/types'
  import ConfigEditor from './ConfigEditor.svelte'
  import JobLog from './JobLog.svelte'
  import Sheet from './Sheet.svelte'

  let { node, name, onClose }: { node: Node; name: string; onClose: () => void } = $props()

  const agent = $derived<AgentInfo | undefined>(node.agents?.find((a) => a.name === name))
  // A job started before this page loaded is known from the inventory;
  // JobLog then fetches its output so far.
  $effect(() => {
    const j = agent?.job
    if (j && !chat.jobs[j.job_id]) {
      chat.jobs[j.job_id] = { node_id: node.id, job_id: j.job_id, kind: j.kind, agent: j.agent, command: j.command, data: '', end: 0, epoch: 0, running: true }
    }
  })
  const job = $derived(latestJob(chat.jobs, node.id, name))
  const canAdmin = $derived(node.online && node.admin === 'on')

  let error = $state('')
  let probing = $state(false)
  let probed = $state(false)
  let editing = $state<{ agent: string; file: string; title: string } | null>(null)
  let draft = $state<Record<string, string>>({})
  let draftFor = ''
  let savingDefaults = $state(false)
  let savedDefaults = $state(false)
  let keyDraft = $state<Record<string, string>>({})

  // Start the draft from the machine's defaults once per agent.
  $effect(() => {
    if (agent && draftFor !== agent.name) {
      draftFor = agent.name
      draft = { ...(agent.defaults ?? {}) }
    }
  })

  const order: Record<string, number> = { model: 0, thought_level: 1, model_config: 2 }
  const options = $derived<ConfigOption[]>(
    [...(agent?.options ?? [])].sort((a, b) => (order[a.category ?? ''] ?? 9) - (order[b.category ?? ''] ?? 9)),
  )
  const defaultsDirty = $derived(
    JSON.stringify(Object.entries(draft).filter(([, v]) => v).sort()) !==
      JSON.stringify(Object.entries(agent?.defaults ?? {}).filter(([, v]) => v).sort()),
  )

  function groups(o: ConfigOption) {
    const out: { name: string; values: ConfigOption['values'] }[] = []
    for (const v of o.values) {
      const g = v.group ?? ''
      let last = out[out.length - 1]
      if (!last || last.name !== g) out.push((last = { name: g, values: [] }))
      last.values.push(v)
    }
    return out
  }
  const valueName = (o: ConfigOption, v: string) => o.values.find((x) => x.value === v)?.name ?? v

  async function probe() {
    probing = true
    error = ''
    try {
      const r = await api.probeAgent(node.id, name)
      // Show them now; the inventory with the same options follows.
      if (agent) {
        agent.options = r.options ?? []
        agent.options_at = r.at
      }
      probed = true
    } catch (e) {
      error = (e as Error).message
    } finally {
      probing = false
    }
  }

  async function saveDefaults() {
    savingDefaults = true
    try {
      const clean = Object.fromEntries(Object.entries(draft).filter(([, v]) => v))
      await api.setAgentDefaults(node.id, name, clean)
      if (agent) agent.defaults = clean
      savedDefaults = true
      setTimeout(() => (savedDefaults = false), 2000)
      error = ''
    } catch (e) {
      error = (e as Error).message
    } finally {
      savingDefaults = false
    }
  }

  async function start(what: 'install' | 'upgrade' | 'login' | 'logout') {
    if (!agent) return
    const cmd = { install: agent.install, upgrade: agent.upgrade, login: agent.login, logout: agent.logout }[what]
    const why = {
      install: '会从官方渠道下载并安装。',
      upgrade: '会更新到最新版本。',
      login: '按终端里的提示完成登录：打开链接，需要时把页面上给出的授权码粘贴回来。',
      logout: '',
    }[what]
    if (!confirm(`在“${node.name}”上${jobTitle[what]} ${agent.title}？\n\n将运行：\n${cmd}\n\n${why}`)) return
    try {
      await api.agentJob(node.id, name, what === 'upgrade' ? 'install' : what, { cols: 80, rows: 24 }, what === 'upgrade')
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function setKey(env: string, clear = false) {
    const value = clear ? '' : (keyDraft[env] ?? '').trim()
    if (!clear && !value) return
    if (clear && !confirm(`清除 ${env}？`)) return
    try {
      await api.setAgentEnv(node.id, name, env, value)
      keyDraft[env] = ''
      if (agent) {
        const set = new Set(agent.env_set ?? [])
        if (clear) set.delete(env)
        else set.add(env)
        agent.env_set = [...set].sort()
      }
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }

  const status = $derived(
    !agent ? '' : !agent.installed ? '未安装' : agent.logged_in === false ? '未登录' : agent.logged_in ? '已登录' : '登录状态未知',
  )
  const statusClass = $derived(!agent?.installed ? 'muted' : agent.logged_in === false ? 'bad' : agent.logged_in ? 'good' : 'muted')
  // Model-related parameters keep the agent's own name (such as "Fast mode").
  const catName: Record<string, string> = { model: '模型', thought_level: '思考强度' }
</script>

{#if editing}
  <ConfigEditor nodeId={node.id} agent={editing.agent} file={editing.file} title={editing.title} onClose={() => (editing = null)} />
{:else}
  <Sheet title={`${agent?.title ?? name} · ${node.name}`} {onClose} wide>
    {#if !agent}
      <p class="sub">这台机器没有报告这个 agent。</p>
    {:else}
      <section class="head">
        <span class={statusClass}>{status}</span>
        {#if agent.version}<span class="sub">{agent.version}</span>{/if}
        {#if agent.note}<span class="sub">{agent.note}</span>{/if}
      </section>
      {#if agent.missing?.length}
        <p class="warn">这台机器缺少 {agent.missing.join('、')}{agent.missing.includes('npx') ? '：需要先安装 Node.js（nodejs.org），sous 通过 npx 启动它的 ACP 适配器' : ''}。</p>
      {/if}
      {#if !node.online}
        <p class="warn">机器离线，连上之后才能管理。</p>
      {:else if node.admin === 'off'}
        <p class="warn">这台机器在 node.json 里关闭了远程管理（remote_admin: false），只能在那台电脑上修改。</p>
      {:else if node.admin === 'unsupported'}
        <p class="warn">这台机器上的 sous 版本太旧，不支持远程管理，请先更新 sous。</p>
      {/if}

      <section class="actions">
        {#if !agent.installed}
          {#if agent.install}<button class="btn small" disabled={!canAdmin || job?.running} onclick={() => start('install')}>安装</button>
          {:else}<span class="sub">sous 还不能在这个系统上自动安装它，请按官网说明手动安装。</span>{/if}
        {:else}
          {#if agent.login}<button class="btn small" class:ghost={agent.logged_in === true} disabled={!canAdmin || job?.running} onclick={() => start('login')}>{agent.logged_in ? '重新登录' : '登录'}</button>{/if}
          {#if agent.upgrade}<button class="btn small ghost" disabled={!canAdmin || job?.running} onclick={() => start('upgrade')}>升级</button>{/if}
          {#if agent.logout && agent.logged_in !== false}<button class="link" disabled={!canAdmin || job?.running} onclick={() => start('logout')}>退出登录</button>{/if}
        {/if}
      </section>

      {#if job}
        {#key job.job_id}<JobLog nodeId={node.id} jobId={job.job_id} />{/key}
      {/if}

      {#if agent.installed}
        <section>
          <h3>模型与思考强度</h3>
          {#if options.length}
            <p class="sub">新开的会话都会先切到这里选的值；选“agent 默认”就不改。</p>
            <div class="opts">
              {#each options as o (o.id)}
                <label>
                  <span title={o.description ?? o.name}>{catName[o.category ?? ''] ?? o.name}</span>
                  <select bind:value={draft[o.id]} disabled={!canAdmin}>
                    <option value={undefined}>agent 默认（{valueName(o, o.current)}）</option>
                    {#each groups(o) as g}
                      {#if g.name}
                        <optgroup label={g.name}>{#each g.values as v}<option value={v.value} title={v.description}>{v.name}</option>{/each}</optgroup>
                      {:else}
                        {#each g.values as v}<option value={v.value} title={v.description}>{v.name}</option>{/each}
                      {/if}
                    {/each}
                  </select>
                </label>
              {/each}
            </div>
            <div class="row">
              <button class="btn small" disabled={!canAdmin || !defaultsDirty || savingDefaults} onclick={saveDefaults}>{savedDefaults ? '已保存' : '保存'}</button>
              <button class="link" disabled={!canAdmin || probing} onclick={probe}>{probing ? '正在读取…' : '重新读取可选项'}</button>
            </div>
          {:else if probed}
            <p class="sub">{agent.title} 没有通过 ACP 提供模型或思考强度的选项，可以在下面的配置文件里设置。</p>
          {:else}
            <p class="sub">启动一次 {agent.title}，读取它支持的模型和思考强度（不会调用模型，可能要等一会儿）。</p>
            <div class="row"><button class="btn small ghost" disabled={!canAdmin || probing || agent.logged_in === false} onclick={probe}>{probing ? '正在读取…' : '读取可选项'}</button></div>
          {/if}
        </section>
      {/if}

      {#if agent.key_env?.length}
        <section>
          <h3>API Key</h3>
          <p class="sub">不想登录账号时，也可以填 API Key。它只保存在这台机器的 node.json 里，页面和 Hub 都不会再显示它。</p>
          {#each agent.key_env as env (env)}
            <div class="key">
              <span class="key-name"><code>{env}</code>
                <span class={agent.env_set?.includes(env) ? 'good' : 'muted'}>{agent.env_set?.includes(env) ? '已设置' : '未设置'}</span></span>
              <input type="password" autocomplete="off" placeholder={agent.env_set?.includes(env) ? '输入新值替换' : '粘贴密钥'}
                bind:value={keyDraft[env]} disabled={!canAdmin} onkeydown={(e) => e.key === 'Enter' && !e.isComposing && setKey(env)} />
              <button class="link accent" disabled={!canAdmin || !keyDraft[env]?.trim()} onclick={() => setKey(env)}>保存</button>
              {#if agent.env_set?.includes(env)}<button class="link danger" disabled={!canAdmin} onclick={() => setKey(env, true)}>清除</button>{/if}
            </div>
          {/each}
        </section>
      {/if}

      {#if agent.installed && agent.configs?.length}
        <section>
          <h3>配置文件</h3>
          <ul class="files">
            {#each agent.configs as c (c.id)}
              <li>
                <code>{c.path}</code>
                <button class="link accent" disabled={!canAdmin} onclick={() => (editing = { agent: name, file: c.id, title: `${agent?.title} · ${c.path}` })}>编辑</button>
              </li>
            {/each}
          </ul>
          {#if agent.launch_override}<p class="sub">node.json 覆盖了它的启动命令（agents 字段）。</p>{/if}
        </section>
      {/if}
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </Sheet>
{/if}

<style>
  section { display: grid; gap: 8px; }
  section.head { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; font-size: 14px; }
  section.actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
  h3 { margin: 6px 0 0; font-size: 15px; }
  .opts { display: grid; gap: 8px; }
  .opts label { display: grid; grid-template-columns: 8em 1fr; gap: 10px; align-items: center; font-size: 14px; }
  select, .key input { border: 1px solid var(--line); background: var(--bg); color: var(--text); border-radius: 8px; padding: 6px 8px; font-size: 14px; min-width: 0; }
  .row { display: flex; gap: 14px; align-items: center; }
  .key { display: grid; grid-template-columns: 17em 1fr auto auto; gap: 10px; align-items: center; font-size: 13px; }
  .key-name { display: flex; gap: 8px; align-items: baseline; justify-content: space-between; }
  .key input { min-width: 0; }
  .files { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
  .files li { display: flex; gap: 12px; align-items: baseline; font-size: 13px; }
  .files code { overflow-wrap: anywhere; }
  .btn.small { padding: 7px 16px; font-size: 14px; }
  .btn:disabled { opacity: .5; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover:not(:disabled) { color: var(--text); }
  .link:disabled { opacity: .5; }
  .link.accent { color: var(--accent); font-weight: 600; }
  .link.danger:hover { color: var(--danger); }
  .sub { color: var(--muted); font-size: 13px; margin: 0; }
  .warn { color: var(--warm); font-size: 13px; margin: 0; }
  .good { color: #3a9d5d; }
  .bad { color: var(--danger); }
  .muted { color: var(--muted); }
  .error { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) {
    .opts label { grid-template-columns: 1fr; gap: 4px; }
    .key { grid-template-columns: 1fr auto auto; row-gap: 4px; }
    .key-name { grid-column: 1 / -1; justify-content: flex-start; }
  }
</style>
