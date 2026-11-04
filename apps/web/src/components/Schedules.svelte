<script lang="ts">
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { cronPresets, describeCron } from '../lib/schedules'
  import type { Schedule } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let list = $state<Schedule[]>([])
  let loading = $state(true)
  let error = $state('')
  let info = $state('')
  let editing = $state<Partial<Schedule> | null>(null)
  let busy = $state(false)
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  async function load() {
    try {
      list = (await api.schedules()).schedules
      error = ''
    } catch (e) {
      error = (e as Error).message
    } finally {
      loading = false
    }
  }

  $effect(() => {
    void chat.schedulesRev
    load()
  })

  function startNew() {
    info = ''
    editing = { title: '', cron: cronPresets[0].cron, prompt: '', enabled: true, tz }
  }

  async function save() {
    if (!editing) return
    busy = true
    error = ''
    try {
      const body = { title: editing.title, cron: editing.cron, prompt: editing.prompt, enabled: editing.enabled, tz: editing.tz || tz }
      if (editing.id) await api.updateSchedule(editing.id, body)
      else await api.createSchedule(body)
      editing = null
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function toggle(s: Schedule) {
    try {
      await api.updateSchedule(s.id, { enabled: !s.enabled })
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function remove(s: Schedule) {
    if (!confirm(`删除定时任务“${s.title}”？`)) return
    try {
      await api.deleteSchedule(s.id)
      await load()
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function runNow(s: Schedule) {
    try {
      await api.runSchedule(s.id)
      info = `已触发“${s.title}”，结果会出现在${s.thread_id ? '它所在的对话' : '主线'}里。`
    } catch (e) {
      error = (e as Error).message
    }
  }

  function when(ts: number, zone: string) {
    if (!ts) return ''
    return new Date(ts).toLocaleString('zh-CN', { timeZone: zone || undefined, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  }
</script>

<Sheet title="定时任务" {onClose}>
  <p class="hint">到时间时，sous 会把说明交给主管去做，比如每天早上汇总进展。也可以直接在对话里说“每天 9 点帮我……”。</p>

  {#if editing}
    <form class="edit" onsubmit={(e) => { e.preventDefault(); save() }}>
      <label>名字<input bind:value={editing.title} placeholder="比如：每日进展" /></label>
      <label>时间
        <div class="row">
          <select aria-label="常用时间" onchange={(e) => editing && e.currentTarget.value && (editing.cron = e.currentTarget.value)}>
            <option value="">常用时间…</option>
            {#each cronPresets as p}<option value={p.cron}>{p.label}</option>{/each}
          </select>
          <input class="cron" bind:value={editing.cron} aria-label="cron 表达式" placeholder="分 时 日 月 周" />
        </div>
        <span class="muted">{describeCron(editing.cron ?? '')}（{editing.tz || tz}）</span>
      </label>
      <label>要做的事<textarea bind:value={editing.prompt} rows="4" placeholder="比如：汇总昨天完成和受阻的任务，三句话以内发给我"></textarea></label>
      <label class="check"><input type="checkbox" bind:checked={editing.enabled} /> 启用</label>
      <div class="row">
        <button class="btn small" type="submit" disabled={busy || !editing.prompt?.trim() || !editing.cron?.trim()}>保存</button>
        <button class="link" type="button" onclick={() => (editing = null)}>取消</button>
      </div>
    </form>
  {:else}
    <button class="btn small add" onclick={startNew}>新建定时任务</button>
  {/if}

  {#if error}<p class="err" role="alert">{error}</p>{/if}
  {#if info}<p class="muted">{info}</p>{/if}

  <ul>
    {#each list as s (s.id)}
      <li class:off={!s.enabled}>
        <div class="head">
          <strong>{s.title}</strong>
          <span class="muted">{describeCron(s.cron)}{s.tz ? `（${s.tz}）` : ''}</span>
        </div>
        <p class="prompt">{s.prompt}</p>
        <p class="muted">
          {#if s.enabled && s.next_run}下次：{when(s.next_run, s.tz)}{:else if !s.enabled}已停用{/if}
          {#if s.last_run} · 上次：{when(s.last_run, s.tz)}{/if}
        </p>
        <div class="actions">
          <button class="link" onclick={() => runNow(s)}>现在执行一次</button>
          <button class="link" onclick={() => toggle(s)}>{s.enabled ? '停用' : '启用'}</button>
          <button class="link" onclick={() => { info = ''; editing = { ...s } }}>编辑</button>
          <button class="link danger" onclick={() => remove(s)}>删除</button>
        </div>
      </li>
    {:else}
      {#if !loading && !editing}<li class="empty">还没有定时任务。</li>{/if}
    {/each}
  </ul>
</Sheet>

<style>
  .hint, .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .btn.small { padding: 7px 16px; font-size: 14px; }
  .add { justify-self: start; }
  .edit { display: grid; gap: 10px; border: 1px dashed var(--accent); border-radius: 12px; padding: 12px; }
  label { display: grid; gap: 4px; font-size: 14px; }
  label.check { display: flex; align-items: center; gap: 6px; }
  input:not([type='checkbox']), textarea, select {
    border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 8px; color: var(--text); font: inherit;
  }
  textarea { resize: vertical; }
  .cron { font: 13px ui-monospace, Menlo, Consolas, monospace; flex: 1; min-width: 0; }
  .row { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
  li { border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; display: grid; gap: 4px; }
  li.off { opacity: .65; }
  li.empty { color: var(--muted); text-align: center; border-style: dashed; padding: 24px 12px; }
  .head { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
  .prompt { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 14px; }
  .actions { display: flex; gap: 14px; justify-content: flex-end; flex-wrap: wrap; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
</style>
