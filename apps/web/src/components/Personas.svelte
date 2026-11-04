<script lang="ts">
  // Personas ("stations"): who does a task — an agent, its model and effort,
  // a brief put before every task, and the kinds of task it suits.
  import { api } from '../lib/api'
  import { chat, loadOverview } from '../lib/app.svelte'
  import { personaColors } from '../lib/status'
  import { agentChoices, modeText } from '../lib/tasks'
  import type { ConfigOption, Node, Persona } from '../lib/types'
  import Avatar from './Avatar.svelte'
  import Sheet from './Sheet.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let list = $state<Persona[]>([])
  let nodes = $state<Node[]>([])
  let editing = $state<Partial<Persona> | null>(null)
  let error = $state('')
  let busy = $state(false)

  async function load() {
    try {
      const [p, n] = await Promise.all([api.personas(), api.nodes()])
      list = p.personas
      nodes = n.nodes
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }
  $effect(() => {
    void chat.tasksRev
    load()
  })

  const agents = $derived(agentChoices(nodes))
  /** The session options an agent reported on any machine (model, effort). */
  const optionsOf = (agent: string): ConfigOption[] => {
    for (const n of nodes) {
      const a = (n.agents ?? []).find((x) => x.name === agent && x.options?.length)
      if (a?.options) return a.options.filter((o) => o.category === 'model' || o.category === 'thought_level')
    }
    return []
  }
  const kindNames: Record<string, string> = { code: '代码', general: '一般', ops: '运维' }

  function edit(p?: Persona) {
    editing = p ? { ...p, options: { ...p.options }, kinds: [...p.kinds] } : { name: '', glyph: '', color: 'purple', agent: '', options: {}, brief: '', default_mode: '', kinds: ['code'] }
  }

  async function save() {
    if (!editing) return
    busy = true
    error = ''
    try {
      if (editing.id) await api.updatePersona(editing.id, editing)
      else await api.createPersona(editing)
      editing = null
      await Promise.all([load(), loadOverview()])
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function remove(p: Persona) {
    if (!confirm(`删除岗位“${p.name}”？`)) return
    try {
      await api.deletePersona(p.id)
      await Promise.all([load(), loadOverview()])
    } catch (e) {
      error = (e as Error).message
    }
  }

  function toggleKind(k: 'code' | 'general' | 'ops') {
    if (!editing) return
    const ks = editing.kinds ?? []
    editing.kinds = ks.includes(k) ? ks.filter((x) => x !== k) : [...ks, k]
  }
</script>

<Sheet title="岗位" {onClose} wide>
  <p class="hint">岗位就是 sous 手下的“人”：每个任务分给一个岗位。岗位不绑定机器，干活时在某台机器上占一个工位。</p>
  {#if error}<p class="err" role="alert">{error}</p>{/if}

  {#if editing}
    <form class="form" onsubmit={(e) => { e.preventDefault(); save() }}>
      <div class="top">
        <Avatar glyph={editing.glyph || editing.name?.slice(0, 1) || '?'} color={editing.color ?? 'gray'} size={44} />
        <div class="grid">
          <label>名字<input bind:value={editing.name} required placeholder="后端" /></label>
          <label>头像字<input bind:value={editing.glyph} maxlength="2" placeholder="后" /></label>
        </div>
      </div>
      <div class="colors" role="radiogroup" aria-label="颜色">
        {#each personaColors as c}
          <button type="button" role="radio" aria-checked={editing.color === c} aria-label={c} class="sw c-{c}" class:on={editing.color === c} onclick={() => editing && (editing.color = c)}></button>
        {/each}
      </div>
      <div class="grid">
        <label>agent
          <select bind:value={editing.agent}>
            <option value="">按项目或全局默认</option>
            {#each agents as a}<option value={a}>{a}</option>{/each}
          </select>
        </label>
        {#each optionsOf(editing.agent ?? '') as o (o.id)}
          <label>{o.name}
            <select value={editing.options?.[o.id] ?? ''} onchange={(e) => editing && (editing.options = { ...editing.options, [o.id]: (e.currentTarget as HTMLSelectElement).value })}>
              <option value="">机器上的默认</option>
              {#each o.values as v}<option value={v.value}>{v.name}</option>{/each}
            </select>
          </label>
        {/each}
        <label>权限模式
          <select bind:value={editing.default_mode}>
            <option value="">按项目或全局默认</option>
            {#each Object.entries(modeText) as [k, v]}<option value={k}>{v}</option>{/each}
          </select>
        </label>
      </div>
      <div class="kinds">
        适合
        {#each ['code', 'general', 'ops'] as const as k}
          <label class="check"><input type="checkbox" checked={editing.kinds?.includes(k)} onchange={() => toggleKind(k)} />{kindNames[k]}</label>
        {/each}
      </div>
      <label>岗位说明（放在每个任务前面交给 agent）<textarea bind:value={editing.brief} rows="3"></textarea></label>
      <div class="actions">
        <button class="btn small" type="submit" disabled={busy || !editing.name?.trim()}>保存</button>
        <button class="link" type="button" onclick={() => (editing = null)}>取消</button>
      </div>
    </form>
  {:else}
    <button class="btn small add" onclick={() => edit()}>新岗位</button>
  {/if}

  <ul>
    {#each list as p (p.id)}
      <li>
        <Avatar glyph={p.glyph} color={p.color} size={34} />
        <div class="info">
          <strong>{p.name}</strong>
          <span class="sub">{p.agent || '默认 agent'}{Object.values(p.options).length ? ' · ' + Object.values(p.options).join(' · ') : ''} · {p.kinds.map((k) => kindNames[k]).join('、') || '各种任务'}</span>
          {#if p.brief}<span class="brief">{p.brief}</span>{/if}
        </div>
        <span class="acts">
          <button class="link" onclick={() => edit(p)}>编辑</button>
          <button class="link danger" onclick={() => remove(p)}>删除</button>
        </span>
      </li>
    {/each}
  </ul>
</Sheet>

<style>
  .hint { margin: 0; color: var(--muted); font-size: 13px; }
  .form { border: 1px solid var(--line); border-radius: 14px; padding: 12px; display: grid; gap: 10px; }
  .top { display: flex; gap: 12px; align-items: center; }
  .top .grid { flex: 1; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; }
  label { display: grid; gap: 4px; font-size: 13px; color: var(--muted); }
  label.check { display: inline-flex; gap: 4px; align-items: center; color: var(--text); }
  input:not([type='checkbox']), textarea, select { border: 1px solid var(--line); background: var(--bg); border-radius: 8px; padding: 6px 10px; color: var(--text); min-width: 0; }
  .colors { display: flex; gap: 8px; }
  .sw { width: 24px; height: 24px; border-radius: 50%; border: 0; background: var(--p-bg); box-shadow: inset 0 0 0 1px var(--line); }
  .sw.on { box-shadow: 0 0 0 2px var(--panel), 0 0 0 4px var(--p-fg); }
  .kinds { display: flex; gap: 12px; align-items: center; font-size: 13px; color: var(--muted); }
  .actions { display: flex; gap: 12px; align-items: center; }
  .btn.small { padding: 6px 16px; font-size: 14px; }
  .add { justify-self: start; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
  li { display: flex; gap: 12px; align-items: flex-start; border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; }
  .info { flex: 1; display: grid; gap: 2px; min-width: 0; }
  .sub { color: var(--muted); font-size: 12px; }
  .brief { font-size: 13px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .acts { display: flex; gap: 12px; }
  .link { border: 0; background: none; padding: 0; font-size: 13px; color: var(--muted); }
  .link.danger:hover { color: var(--danger); }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
</style>
