import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { loadOverview, useChat } from '../lib/app'
import { personaColors } from '../lib/status'
import { agentChoices, modeText } from '../lib/tasks'
import type { ConfigOption, Node, Persona } from '../lib/types'
import Avatar from './Avatar'
import Sheet from './Sheet'

const kindNames: Record<string, string> = { code: '代码', general: '一般', ops: '运维' }

export default function Personas({ onClose }: { onClose: () => void }) {
  const chat = useChat()
  const [list, setList] = useState<Persona[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [editing, setEditing] = useState<Partial<Persona> | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function load() {
    try {
      const [p, n] = await Promise.all([api.personas(), api.nodes()])
      setList(p.personas)
      setNodes(n.nodes)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    void load()
  }, [chat.tasksRev])

  const agents = agentChoices(nodes)
  /** The session options an agent reported on any machine (model, effort). */
  const optionsOf = (agent: string): ConfigOption[] => {
    for (const n of nodes) {
      const a = (n.agents ?? []).find((x) => x.name === agent && x.options?.length)
      if (a?.options) return a.options.filter((o) => o.category === 'model' || o.category === 'thought_level')
    }
    return []
  }

  function edit(p?: Persona) {
    setEditing(p ? { ...p, options: { ...p.options }, kinds: [...p.kinds] } : { name: '', glyph: '', color: 'purple', agent: '', options: {}, brief: '', default_mode: '', kinds: ['code'] })
  }

  async function save() {
    if (!editing) return
    setBusy(true)
    setError('')
    try {
      if (editing.id) await api.updatePersona(editing.id, editing)
      else await api.createPersona(editing)
      setEditing(null)
      await Promise.all([load(), loadOverview()])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove(p: Persona) {
    if (!confirm(`删除岗位“${p.name}”？`)) return
    try {
      await api.deletePersona(p.id)
      await Promise.all([load(), loadOverview()])
    } catch (e) {
      setError((e as Error).message)
    }
  }

  function toggleKind(k: 'code' | 'general' | 'ops') {
    if (!editing) return
    const ks = editing.kinds ?? []
    setEditing({ ...editing, kinds: ks.includes(k) ? ks.filter((x) => x !== k) : [...ks, k] })
  }

  return (
    <Sheet title="岗位" onClose={onClose} wide>
      <p className="m-0 text-muted text-[13px]">岗位就是 sous 手下的“人”：每个任务分给一个岗位。岗位不绑定机器，干活时在某台机器上占一个工位。</p>
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}

      {editing ? (
        <form className="border border-line rounded-[14px] p-3 grid gap-2.5" onSubmit={(e) => { e.preventDefault(); void save() }}>
          <div className="flex gap-3 items-center">
            <Avatar glyph={editing.glyph || editing.name?.slice(0, 1) || '?'} color={editing.color ?? 'gray'} size={44} />
            <div className="flex-1 grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-2.5">
              <label className="grid gap-1 text-[13px] text-muted">
                名字
                <input
                  className="border border-line bg-page rounded-[8px] px-2.5 py-1.5 text-ink min-w-0"
                  value={editing.name ?? ''}
                  onChange={(e) => setEditing({ ...editing, name: e.currentTarget.value })}
                  required
                  placeholder="后端"
                />
              </label>
              <label className="grid gap-1 text-[13px] text-muted">
                头像字
                <input
                  className="border border-line bg-page rounded-[8px] px-2.5 py-1.5 text-ink min-w-0"
                  value={editing.glyph ?? ''}
                  onChange={(e) => setEditing({ ...editing, glyph: e.currentTarget.value })}
                  maxLength={2}
                  placeholder="后"
                />
              </label>
            </div>
          </div>
          <div className="flex gap-2" role="radiogroup" aria-label="颜色">
            {personaColors.map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={editing.color === c}
                aria-label={c}
                className={`c-${c} w-6 h-6 rounded-full border-0 bg-[var(--p-bg)] ${
                  editing.color === c ? 'shadow-[0_0_0_2px_var(--panel),0_0_0_4px_var(--p-fg)]' : 'shadow-[inset_0_0_0_1px_var(--line)]'
                }`}
                onClick={() => setEditing({ ...editing, color: c })}
              ></button>
            ))}
          </div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-2.5">
            <label className="grid gap-1 text-[13px] text-muted">
              agent
              <select
                className="border border-line bg-page rounded-[8px] px-2.5 py-1.5 text-ink min-w-0"
                value={editing.agent ?? ''}
                onChange={(e) => setEditing({ ...editing, agent: e.currentTarget.value })}
              >
                <option value="">按项目或全局默认</option>
                {agents.map((a) => <option key={a} value={a}>{a}</option>)}
              </select>
            </label>
            {optionsOf(editing.agent ?? '').map((o) => (
              <label key={o.id} className="grid gap-1 text-[13px] text-muted">
                {o.name}
                <select
                  className="border border-line bg-page rounded-[8px] px-2.5 py-1.5 text-ink min-w-0"
                  value={editing.options?.[o.id] ?? ''}
                  onChange={(e) => setEditing({ ...editing, options: { ...editing.options, [o.id]: e.currentTarget.value } })}
                >
                  <option value="">机器上的默认</option>
                  {o.values.map((v) => <option key={v.value} value={v.value}>{v.name}</option>)}
                </select>
              </label>
            ))}
            <label className="grid gap-1 text-[13px] text-muted">
              权限模式
              <select
                className="border border-line bg-page rounded-[8px] px-2.5 py-1.5 text-ink min-w-0"
                value={editing.default_mode ?? ''}
                onChange={(e) => setEditing({ ...editing, default_mode: e.currentTarget.value as Persona['default_mode'] })}
              >
                <option value="">按项目或全局默认</option>
                {Object.entries(modeText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
          </div>
          <div className="flex gap-3 items-center text-[13px] text-muted">
            适合
            {(['code', 'general', 'ops'] as const).map((k) => (
              <label key={k} className="inline-flex gap-1 items-center text-ink">
                <input type="checkbox" checked={editing.kinds?.includes(k) ?? false} onChange={() => toggleKind(k)} />{kindNames[k]}
              </label>
            ))}
          </div>
          <label className="grid gap-1 text-[13px] text-muted">
            岗位说明（放在每个任务前面交给 agent）
            <textarea
              className="border border-line bg-page rounded-[8px] px-2.5 py-1.5 text-ink min-w-0 resize-y"
              rows={3}
              value={editing.brief ?? ''}
              onChange={(e) => setEditing({ ...editing, brief: e.currentTarget.value })}
            ></textarea>
          </label>
          <div className="flex gap-3 items-center">
            <button className="btn small px-4" type="submit" disabled={busy || !editing.name?.trim()}>保存</button>
            <button className="link" type="button" onClick={() => setEditing(null)}>取消</button>
          </div>
        </form>
      ) : (
        <button className="btn small px-4 justify-self-start" onClick={() => edit()}>新岗位</button>
      )}

      <ul className="list-none m-0 p-0 grid gap-2">
        {list.map((p) => (
          <li key={p.id} className="flex gap-3 items-start border border-line rounded-[12px] px-3 py-2.5">
            <Avatar glyph={p.glyph} color={p.color} size={34} />
            <div className="flex-1 grid gap-0.5 min-w-0">
              <strong>{p.name}</strong>
              <span className="text-muted text-[12px]">
                {p.agent || '默认 agent'}{Object.values(p.options).length ? ' · ' + Object.values(p.options).join(' · ') : ''} · {p.kinds.map((k) => kindNames[k]).join('、') || '各种任务'}
              </span>
              {p.brief && <span className="text-[13px] text-muted line-clamp-2">{p.brief}</span>}
            </div>
            <span className="flex gap-3">
              <button className="link" onClick={() => edit(p)}>编辑</button>
              <button className="link danger" onClick={() => void remove(p)}>删除</button>
            </span>
          </li>
        ))}
      </ul>
    </Sheet>
  )
}
