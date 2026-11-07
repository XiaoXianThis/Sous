import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { loadOverview, useChat } from '../lib/app'
import { agentChoices, machinesFor, modeText } from '../lib/tasks'
import type { ConfigOption, Node, Persona } from '../lib/types'
import AgentIcon, { agentTitle } from './AgentIcon'
import Icon from './Icon'
import { Who } from './Avatar'
import Sheet from './Sheet'

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
    setEditing(p ? { ...p, options: { ...p.options }, kinds: [...p.kinds] } : { name: '', glyph: '', color: 'purple', agent: '', options: {}, brief: '', default_mode: '', kinds: [] })
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
    if (!confirm(`删除预设“${p.name}”？`)) return
    try {
      await api.deletePersona(p.id)
      await Promise.all([load(), loadOverview()])
    } catch (e) {
      setError((e as Error).message)
    }
  }

  /** The free-form model and effort when no machine reported the agent's
   *  options yet: the node maps them to the agent's own option (0024). */
  const freeKeys = (agent: string) => {
    const cats = optionsOf(agent).map((o) => o.category)
    return [
      ...(cats.includes('model') ? [] : [['model', '模型', '比如 opus-5.5、gpt-6.1-terra']]),
      ...(cats.includes('thought_level') ? [] : [['effort', '思考强度', '比如 high；不填用 agent 的默认']]),
    ] as [string, string, string][]
  }

  return (
    <Sheet title="预设" onClose={onClose} wide>
      <p className="m-0 text-muted text-[13px]">
        预设是你常用的“agent + 模型”组合，写上它的定位（快速、主力、便宜但慢……），主管派活时按任务挑一个。每台电脑装的 agent 不同，下面会标出预设能在哪些电脑上用；用不了时主管会换一个。
      </p>
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}

      {editing ? (
        <form className="border border-line rounded-[16px] p-3 grid gap-2.5" onSubmit={(e) => { e.preventDefault(); void save() }}>
          <label className="grid gap-1 text-[13px] text-muted">
            名字
            <input
              className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
              value={editing.name ?? ''}
              onChange={(e) => setEditing({ ...editing, name: e.currentTarget.value })}
              required
              placeholder="主力"
            />
          </label>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-2.5">
            <div className="grid gap-1 text-[13px] text-muted col-span-full" role="radiogroup" aria-label="agent">
              agent
              <div className="flex gap-1.5 flex-wrap">
                {['', ...agents].map((a) => (
                  <button
                    key={a || 'default'}
                    type="button"
                    role="radio"
                    aria-checked={(editing.agent ?? '') === a}
                    title={a ? agentTitle(a) : '用电脑的默认 agent'}
                    className={`flex items-center gap-1.5 h-9 pl-1.5 pr-2.5 rounded-[12px] border-0 text-[12.5px] font-medium transition-shadow ${
                      (editing.agent ?? '') === a ? 'bg-panel text-ink shadow-[0_0_0_2px_var(--accent)]' : 'bg-sunken text-muted hover:text-ink'
                    }`}
                    onClick={() => setEditing({ ...editing, agent: a })}
                  >
                    {a ? <AgentIcon name={a} size={24} /> : <span className="grid place-items-center w-6 h-6 rounded-[9px] bg-hover"><Icon name="sparkles" size={14} /></span>}
                    {a ? agentTitle(a) : '电脑的默认'}
                  </button>
                ))}
              </div>
            </div>
            {optionsOf(editing.agent ?? '').map((o) => (
              <label key={o.id} className="grid gap-1 text-[13px] text-muted">
                {o.name}
                <select
                  className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                  value={editing.options?.[o.id] ?? ''}
                  onChange={(e) => setEditing({ ...editing, options: { ...editing.options, [o.id]: e.currentTarget.value } })}
                >
                  <option value="">电脑上的默认</option>
                  {o.values.map((v) => <option key={v.value} value={v.value}>{v.name}</option>)}
                </select>
              </label>
            ))}
            {editing.agent && freeKeys(editing.agent).map(([k, label, hint]) => (
              <label key={k} className="grid gap-1 text-[13px] text-muted">
                {label}
                <input
                  className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                  value={editing.options?.[k] ?? ''}
                  placeholder={hint}
                  onChange={(e) => setEditing({ ...editing, options: { ...editing.options, [k]: e.currentTarget.value } })}
                />
              </label>
            ))}
            <label className="grid gap-1 text-[13px] text-muted">
              权限模式
              <select
                className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                value={editing.default_mode ?? ''}
                onChange={(e) => setEditing({ ...editing, default_mode: e.currentTarget.value as Persona['default_mode'] })}
              >
                <option value="">按项目或全局默认</option>
                {Object.entries(modeText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
          </div>
          <label className="grid gap-1 text-[13px] text-muted">
            定位（给主管看：什么时候用它）
            <textarea
              className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0 resize-y"
              rows={2}
              placeholder="快速：小改动、问答；主力：复杂功能；廉价：不急、可以慢慢跑的活"
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
        <button className="btn small justify-self-start" onClick={() => edit()}><Icon name="plus" size={15} stroke={2.2} />新预设</button>
      )}

      <ul className="list-none m-0 p-0 grid gap-2">
        {list.map((p) => {
          const where = p.agent ? machinesFor(p.agent, nodes) : []
          return (
            <li key={p.id} className="flex gap-3 items-start border border-line rounded-[18px] px-4 py-3.5">
              <Who persona={p} agent={p.agent} size={38} />
              <div className="flex-1 grid gap-0.5 min-w-0">
                <strong>{p.name}{p.id === 'reviewer' && <span className="text-muted font-normal text-[12.5px]">（验收任务时用）</span>}</strong>
                <span className="flex gap-1 flex-wrap mt-0.5">
                  <span className="pill">{p.agent ? agentTitle(p.agent) : '电脑的默认 agent'}</span>
                  {Object.values(p.options).filter(Boolean).map((v) => <span key={v} className="pill"><Icon name="cpu" size={11} />{v}</span>)}
                </span>
                {p.brief && <span className="text-[13px] text-muted line-clamp-2">{p.brief}</span>}
                {p.agent && (
                  <span className={`text-[12.5px] ${where.length ? 'text-muted' : 'tone-warn'}`}>
                    {where.length ? `能在 ${where.map((n) => n.name + (n.online ? '' : '（离线）')).join('、')} 上用` : '现在没有电脑能用它（没装、停用或没登录）'}
                  </span>
                )}
              </div>
              <span className="flex gap-0.5 -mr-1.5 -mt-1">
                <button className="icon-btn" aria-label={`编辑 ${p.name}`} data-tip="编辑" data-tip-pos="bottom" onClick={() => edit(p)}><Icon name="edit" size={16} /></button>
                <button className="icon-btn hover:!text-danger" aria-label={`删除 ${p.name}`} data-tip="删除" data-tip-pos="bottom" onClick={() => void remove(p)}><Icon name="trash" size={16} /></button>
              </span>
            </li>
          )
        })}
      </ul>
    </Sheet>
  )
}
