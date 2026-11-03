<script lang="ts">
  import { onMount } from 'svelte'
  import { api } from '../lib/api'
  import { disablePush, enablePush, pushState, type PushState } from '../lib/push'
  import { agentChoices, modeText, pushKindText } from '../lib/tasks'
  import type { Node, PermissionMode, Settings } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let s = $state<Settings | null>(null)
  let nodes = $state<Node[]>([])
  let push = $state<PushState>('off')
  let error = $state('')
  let info = $state('')
  let busy = $state(false)

  onMount(async () => {
    try {
      const [st, n] = await Promise.all([api.settings(), api.nodes()])
      s = st
      nodes = n.nodes
      push = await pushState()
    } catch (e) {
      error = (e as Error).message
    }
  })

  async function patch(p: Partial<Settings>) {
    error = ''
    try {
      s = await api.patchSettings(p)
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function togglePush() {
    busy = true
    error = ''
    info = ''
    try {
      push = push === 'on' ? await disablePush() : await enablePush()
      s = await api.settings()
      if (push === 'denied') error = '浏览器拒绝了通知权限，请在系统或浏览器设置里打开。'
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function test() {
    busy = true
    info = ''
    try {
      const r = await api.testPush()
      info = r.devices === 0 ? '还没有设备开启推送。' : `已发送到 ${r.sent}/${r.devices} 台设备。${r.errors.length ? r.errors.join('；') : ''}`
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  const agents = $derived(agentChoices(nodes))
  const modeHelp: Record<string, string> = {
    default: '读文件、在 worktree 里改代码、跑测试直接放行；其他操作（执行命令、安装、push 等）都问你。',
    auto: '原本要问你的操作先交给审批 agent 判断；它拿不准的、不可逆的，再问你。',
    open: '全部放行，只记录日志。底线规则仍然生效。',
  }
  const roleText: Record<string, string> = { supervisor: '主管', reviewer: '验收', approver: '审批', inspector: '巡检' }
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
</script>

<Sheet title="设置" {onClose}>
  {#if s}
    <section>
      <h3>权限模式（全局默认）</h3>
      <div class="modes" role="radiogroup" aria-label="权限模式">
        {#each Object.entries(modeText) as [k, v]}
          <label class="mode" class:on={s.default_mode === k}>
            <input type="radio" name="mode" value={k} checked={s.default_mode === k} onchange={() => patch({ default_mode: k as PermissionMode })} />
            <strong>{v}</strong><span>{modeHelp[k]}</span>
          </label>
        {/each}
      </div>
      <p class="muted">项目和单个任务可以覆盖这里的设置。派发时会固定下当时的模式。</p>
    </section>

    <section>
      <h3>默认 agent</h3>
      <select value={s.default_agent} onchange={(e) => patch({ default_agent: e.currentTarget.value })} aria-label="默认 agent">
        <option value="">自动（第一个可用的）</option>
        {#each agents as a}<option value={a}>{a}</option>{/each}
      </select>
    </section>

    <section>
      <h3>推送通知</h3>
      {#if push === 'unsupported'}
        <p class="muted">这个浏览器不支持推送。iPhone 和 iPad 上要先把 sous“添加到主屏幕”，再从主屏幕打开。</p>
      {:else}
        <div class="row">
          <button class="btn small" class:ghost={push === 'on'} disabled={busy} onclick={togglePush}>
            {push === 'on' ? '在这台设备上关闭' : '在这台设备上开启'}
          </button>
          {#if s.push_devices > 0}<button class="link" disabled={busy} onclick={test}>发一条测试通知</button>{/if}
          <span class="muted">已开启的设备：{s.push_devices}</span>
        </div>
      {/if}
      <div class="kinds">
        {#each Object.entries(pushKindText) as [k, v]}
          <label class="check"><input type="checkbox" checked={s.notify[k] ?? true} onchange={(e) => patch({ notify: { [k]: e.currentTarget.checked } })} /> {v}</label>
        {/each}
      </div>
      <div class="quiet">
        <label class="check">
          <input type="checkbox" checked={s.quiet.enabled} onchange={(e) => patch({ quiet: { ...s!.quiet, enabled: e.currentTarget.checked, tz: s!.quiet.tz || tz } })} />
          免打扰
        </label>
        <input type="time" value={s.quiet.start} aria-label="开始" onchange={(e) => patch({ quiet: { ...s!.quiet, start: e.currentTarget.value, tz: s!.quiet.tz || tz } })} />
        <span>到</span>
        <input type="time" value={s.quiet.end} aria-label="结束" onchange={(e) => patch({ quiet: { ...s!.quiet, end: e.currentTarget.value, tz: s!.quiet.tz || tz } })} />
        <span class="muted">期间只推送审批</span>
      </div>
      {#if info}<p class="muted">{info}</p>{/if}
    </section>

    <section>
      <h3>模型</h3>
      <table>
        <tbody>
          {#each Object.keys(roleText).filter((r) => s!.roles[r]) as role}<tr><td>{roleText[role]}</td><td><code>{s.roles[role]}</code></td></tr>{/each}
        </tbody>
      </table>
      <p class="muted">模型和 API key 在 Hub 的配置文件和环境变量里设置，界面上不显示密钥。</p>
    </section>
  {/if}
  {#if error}<p class="err" role="alert">{error}</p>{/if}
</Sheet>

<style>
  section { display: grid; gap: 8px; }
  h3 { margin: 0; font-size: 14px; }
  .modes { display: grid; gap: 6px; }
  .mode { display: grid; grid-template-columns: auto auto 1fr; gap: 4px 8px; align-items: baseline; border: 1px solid var(--line); border-radius: 10px; padding: 8px 10px; cursor: pointer; }
  .mode.on { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 6%, var(--panel)); }
  .mode span { color: var(--muted); font-size: 13px; }
  select, input[type='time'] { border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 5px 8px; color: var(--text); justify-self: start; }
  .row, .quiet { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
  .kinds { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 4px 12px; }
  .check { display: flex; gap: 6px; align-items: center; font-size: 14px; }
  .btn.small { padding: 6px 16px; font-size: 14px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  table { border-collapse: collapse; font-size: 14px; }
  td { padding: 2px 16px 2px 0; }
  code { font: 13px ui-monospace, Menlo, Consolas, monospace; }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
</style>
