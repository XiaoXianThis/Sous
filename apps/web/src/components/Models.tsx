import { useEffect, useState } from 'react'
import { ApiError, api, type ProviderInput } from '../lib/api'
import { reauthenticate } from '../lib/passkey'
import type { ModelProvider, ModelRole, ModelsView } from '../lib/types'
import Icon from './Icon'

export const roleText: Record<string, string> = { supervisor: '主管', reviewer: '验收', approver: '审批', inspector: '巡检', compactor: '整理' }
const roleHelp: Record<string, string> = {
  supervisor: '和你对话、管理任务、派活',
  reviewer: '检查 agent 的改动是否完成了任务',
  approver: '自动模式下替你判断要不要放行',
  inspector: '定时巡检，发现卡住或循环的 Run',
  compactor: '对话很长时，把较早的消息整理成摘要',
}
export const kindText: Record<string, string> = {
  'openai-chat': 'OpenAI 兼容（Chat Completions）',
  'openai-responses': 'OpenAI Responses',
  'anthropic-messages': 'Anthropic Messages',
  mock: '离线测试（mock）',
}
const effortText: Record<string, string> = { low: '低', medium: '中', high: '高', xhigh: '很高', max: '最高' }

// Common providers, to fill the form in one click.
const presets: { name: string; kind: string; base_url: string; models: string[] }[] = [
  { name: 'Anthropic', kind: 'anthropic-messages', base_url: 'https://api.anthropic.com', models: ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5'] },
  { name: 'OpenAI', kind: 'openai-responses', base_url: 'https://api.openai.com/v1', models: [] },
  { name: 'DeepSeek', kind: 'openai-chat', base_url: 'https://api.deepseek.com', models: ['deepseek-v4-pro', 'deepseek-flash'] },
  { name: 'OpenRouter', kind: 'openai-chat', base_url: 'https://openrouter.ai/api/v1', models: [] },
  { name: 'OpenCode Go', kind: 'openai-chat', base_url: 'https://opencode.ai/inference/go/openai/v1', models: [] },
  { name: 'Ollama（本机）', kind: 'openai-chat', base_url: 'http://localhost:11434/v1', models: [] },
]

const SEP = '\u0000'
const input = 'border border-line bg-page text-ink rounded-[10px] px-2.5 py-1.5 text-[13px] min-w-0'

/** "provider/model" choices grouped by provider, for a select. */
export function ModelOptions({ providers }: { providers: ModelProvider[] }) {
  return (
    <>
      {providers.map((p) => (
        <optgroup key={p.id} label={p.name}>
          {p.models.map((m) => <option key={m} value={p.id + SEP + m}>{m}</option>)}
          {p.models.length === 0 && <option disabled value="">（还没有模型）</option>}
        </optgroup>
      ))}
    </>
  )
}

export const choiceValue = (r?: ModelRole) => (r && r.model ? r.provider_id + SEP + r.model : '')
export const parseChoice = (v: string) => {
  const [provider_id, model] = v.split(SEP)
  return { provider_id, model }
}

/**
 * Model providers and which model each role uses, kept on the Hub. Saving
 * a provider needs a recent passkey login: its address decides where the
 * API key goes.
 */
export default function Models() {
  const [view, setView] = useState<ModelsView | null>(null)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<string>('') // provider id, or "new"
  const [reauth, setReauth] = useState<(() => Promise<void>) | null>(null)

  useEffect(() => {
    api.models().then(setView).catch((e) => setError((e as Error).message))
  }, [])

  async function run(fn: () => Promise<ModelsView>, done?: () => void) {
    setError('')
    try {
      setView(await fn())
      done?.()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'reauth') {
        setReauth(() => async () => {
          await reauthenticate()
          setReauth(null)
          await run(fn, done)
        })
      } else setError((e as Error).message)
    }
  }

  if (!view) return error ? <p className="text-danger text-[13px]">{error}</p> : <p className="text-muted text-[13px]">加载中…</p>

  const setRole = (role: string, next: Partial<ModelRole> | null) => {
    // An inherited role shows the supervisor's choice; it is not its own to build on.
    const cur = view.roles[role]?.inherited ? undefined : view.roles[role]
    if (next === null) return run(() => api.putRoles({ [role]: null }))
    const merged = { provider_id: cur?.provider_id ?? '', model: cur?.model ?? '', effort: cur?.effort ?? '', ...next }
    if (next.context_window === undefined) delete merged.context_window // the Hub keeps it while the model stays
    if (!merged.model) return
    return run(() => api.putRoles({ [role]: merged }))
  }

  return (
    <div className="grid gap-6">
      {reauth && (
        <div className="flex items-center gap-3 flex-wrap rounded-[12px] px-3 py-2 bg-[color-mix(in_srgb,var(--warm)_12%,transparent)] text-[13px]">
          <Icon name="shield" size={16} />
          <span className="flex-1 min-w-0">修改供应商需要再验证一次 Passkey（它决定 API Key 发到哪里）。</span>
          <button className="btn small" onClick={() => void reauth().catch((e) => setError((e as Error).message))}>验证并保存</button>
          <button className="btn small ghost" onClick={() => setReauth(null)}>取消</button>
        </div>
      )}
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}

      <section className="grid gap-2">
        <h3 className="m-0 text-[14px]">各角色用的模型</h3>
        <p className="m-0 text-muted text-[13px]">随时可以切换，下一次调用模型就生效，不用重启。没单独选的角色跟主管用同一个。</p>
        <div className="card p-1.5 grid gap-px">
          {view.role_order.map((role) => {
            const r = view.roles[role]
            const own = !r?.inherited && !!r?.model
            return (
              <div key={role} className="grid grid-cols-[120px_minmax(0,1fr)_120px] gap-2 items-center px-2.5 py-2 max-[760px]:grid-cols-1">
                <div className="grid">
                  <strong className="text-[13px]">{roleText[role] ?? role}</strong>
                  <span className="text-[12px] text-faint">{roleHelp[role]}</span>
                </div>
                <select
                  className={input}
                  aria-label={`${roleText[role]}的模型`}
                  value={own || role === 'supervisor' ? choiceValue(r) : ''}
                  onChange={(e) => void (e.currentTarget.value ? setRole(role, parseChoice(e.currentTarget.value)) : setRole(role, null))}
                >
                  {role === 'supervisor' ? <option value="" disabled>选一个模型</option> : <option value="">跟主管一样{r?.model ? `（${r.model}）` : ''}</option>}
                  <ModelOptions providers={view.providers} />
                </select>
                <select
                  className={input}
                  aria-label={`${roleText[role]}的思考强度`}
                  disabled={!own && role !== 'supervisor'}
                  value={own || role === 'supervisor' ? r?.effort ?? '' : ''}
                  onChange={(e) => void setRole(role, { effort: e.currentTarget.value })}
                >
                  <option value="">思考：默认</option>
                  {view.efforts.map((x) => <option key={x} value={x}>思考：{effortText[x] ?? x}</option>)}
                </select>
              </div>
            )
          })}
        </div>
        <ContextWindow
          value={view.roles.supervisor?.context_window ?? 0}
          disabled={!view.roles.supervisor?.model}
          onSave={(n) => setRole('supervisor', { context_window: n })}
        />
      </section>

      <section className="grid gap-2">
        <div className="flex items-center gap-2">
          <h3 className="m-0 text-[14px] flex-1">供应商</h3>
          <button className="btn small" onClick={() => setEditing('new')}><Icon name="plus" size={14} />添加供应商</button>
        </div>
        {editing === 'new' && (
          <ProviderForm kinds={view.kinds} onCancel={() => setEditing('')} onSave={(p) => run(() => api.createProvider(p), () => setEditing(''))} />
        )}
        {view.providers.length === 0 && editing !== 'new' && (
          <p className="m-0 text-muted text-[13px]">还没有供应商。添加一个，填上 API Key 和要用的模型，再给主管选上。</p>
        )}
        {view.providers.map((p) =>
          editing === p.id ? (
            <ProviderForm key={p.id} provider={p} kinds={view.kinds} onCancel={() => setEditing('')}
              onSave={(x) => run(() => api.updateProvider(p.id, x), () => setEditing(''))} />
          ) : (
            <ProviderCard key={p.id} p={p} view={view} onEdit={() => setEditing(p.id)}
              onModels={(models) => run(() => api.updateProvider(p.id, { models }))}
              onDelete={() => { if (confirm(`删除供应商「${p.name}」？`)) void run(() => api.deleteProvider(p.id)) }} />
          ),
        )}
        <p className="m-0 text-faint text-[12px]">API Key 只保存在 Hub 上，页面上不会再显示。也可以填 Hub 上的环境变量名，从环境变量读。</p>
      </section>
    </div>
  )
}

function ProviderCard({ p, view, onEdit, onModels, onDelete }: {
  p: ModelProvider
  view: ModelsView
  onEdit: () => void
  onModels: (models: string[]) => void
  onDelete: () => void
}) {
  const [add, setAdd] = useState('')
  const [found, setFound] = useState<string[] | null>(null)
  const [filter, setFilter] = useState('')
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')
  const used = Object.values(view.roles).some((r) => !r.inherited && r.provider_id === p.id)

  async function discover() {
    setBusy('discover')
    setNote('')
    try {
      setFound((await api.discoverModels(p.id)).models)
    } catch (e) {
      setNote((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  async function test(model: string) {
    setBusy('test:' + model)
    setNote('')
    try {
      const r = await api.testModel(p.id, model)
      setNote(r.ok ? `${model} 可以用：回答“${r.reply?.trim()}”，用时 ${(r.ms / 1000).toFixed(1)} 秒` : `${model} 不能用：${r.error}`)
    } catch (e) {
      setNote((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const key = p.key_set ? 'Key 已保存' : p.api_key_env ? (p.env_set ? `Key 来自环境变量 ${p.api_key_env}` : `环境变量 ${p.api_key_env} 没有设置`) : p.kind === 'mock' ? '不需要 Key' : '没有 Key'
  const keyOk = p.key_set || p.env_set || p.kind === 'mock'

  return (
    <div className="card p-3 grid gap-2.5">
      <div className="flex items-center gap-2 min-w-0 flex-wrap">
        <strong className="text-[14px]">{p.name}</strong>
        <span className="pill">{kindText[p.kind] ?? p.kind}</span>
        <span className={`pill ${keyOk ? '' : 'tone-warm'}`}><Icon name="key" size={12} />{key}</span>
        <span className="flex-1" />
        <button className="icon-btn" aria-label={`编辑 ${p.name}`} data-tip="编辑" onClick={onEdit}><Icon name="edit" size={16} /></button>
        <button className="icon-btn" aria-label={`删除 ${p.name}`} data-tip={used ? '有角色在用，不能删除' : '删除'} disabled={used} onClick={onDelete}><Icon name="trash" size={16} /></button>
      </div>
      <code className="text-[12px] text-muted font-mono overflow-hidden text-ellipsis whitespace-nowrap">{p.base_url || p.default_url}</code>
      <div className="flex flex-wrap gap-1.5 items-center">
        {p.models.map((m) => (
          <span key={m} className="inline-flex items-center gap-1 h-7 pl-2.5 pr-1 rounded-[9px] bg-sunken text-[12.5px] font-mono">
            {m}
            <button className="link text-[12px] px-1" disabled={!!busy} onClick={() => void test(m)}>{busy === 'test:' + m ? '测试中…' : '测试'}</button>
            <button className="icon-btn !w-6 !h-6" aria-label={`移除 ${m}`} onClick={() => onModels(p.models.filter((x) => x !== m))}><Icon name="x" size={12} /></button>
          </span>
        ))}
        <form className="inline-flex gap-1" onSubmit={(e) => { e.preventDefault(); if (add.trim()) { onModels([...p.models, add.trim()]); setAdd('') } }}>
          <input className={`${input} w-[180px] font-mono`} placeholder="加一个模型名" value={add} onChange={(e) => setAdd(e.currentTarget.value)} aria-label="模型名" />
          <button className="btn small ghost" type="submit">添加</button>
        </form>
        {p.kind !== 'mock' && <button className="link" disabled={!!busy} onClick={() => void discover()}>{busy === 'discover' ? '读取中…' : '从供应商读取模型列表'}</button>}
      </div>
      {found && (
        <div className="grid gap-1.5 border border-line rounded-[12px] p-2">
          <div className="flex gap-2 items-center">
            <input className={`${input} flex-1`} placeholder={`筛选 ${found.length} 个模型`} value={filter} onChange={(e) => setFilter(e.currentTarget.value)} />
            <button className="btn small ghost" onClick={() => setFound(null)}>收起</button>
          </div>
          <div className="flex flex-wrap gap-1 max-h-[180px] overflow-y-auto">
            {found.filter((m) => m.toLowerCase().includes(filter.toLowerCase())).map((m) => {
              const on = p.models.includes(m)
              return (
                <button key={m} className={`h-7 px-2.5 rounded-[9px] text-[12.5px] font-mono border ${on ? 'border-accent text-accent bg-transparent' : 'border-line bg-panel text-ink'}`}
                  onClick={() => onModels(on ? p.models.filter((x) => x !== m) : [...p.models, m])}>
                  {on && '✓ '}{m}
                </button>
              )
            })}
          </div>
        </div>
      )}
      {note && <p className="m-0 text-[13px] text-muted">{note}</p>}
    </div>
  )
}

function ProviderForm({ provider, kinds, onSave, onCancel }: {
  provider?: ModelProvider
  kinds: string[]
  onSave: (p: ProviderInput) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(provider?.name ?? '')
  const [kind, setKind] = useState(provider?.kind ?? 'openai-chat')
  const [baseURL, setBaseURL] = useState(provider?.base_url ?? '')
  const [key, setKey] = useState('')
  const [env, setEnv] = useState(provider?.api_key_env ?? '')
  const [models, setModels] = useState((provider?.models ?? []).join(', '))
  const [clear, setClear] = useState(false)

  function save(e: React.FormEvent) {
    e.preventDefault()
    const p: ProviderInput = { name, kind, base_url: baseURL, api_key_env: env, models: models.split(/[,\s]+/).filter(Boolean) }
    if (key.trim()) p.api_key = key.trim()
    else if (clear) p.clear_key = true
    onSave(p)
  }

  return (
    <form className="card p-3 grid gap-2.5" onSubmit={save}>
      {!provider && (
        <div className="flex flex-wrap gap-1.5 items-center">
          <span className="text-[12px] text-muted">常用：</span>
          {presets.map((x) => (
            <button key={x.name} type="button" className="h-7 px-2.5 rounded-[9px] border border-line bg-panel text-[12.5px]"
              onClick={() => { setName(x.name); setKind(x.kind); setBaseURL(x.base_url); setModels(x.models.join(', ')) }}>{x.name}</button>
          ))}
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 max-[760px]:grid-cols-1">
        <label className="grid gap-1 text-[12px] text-muted">名字<input className={input} value={name} onChange={(e) => setName(e.currentTarget.value)} required /></label>
        <label className="grid gap-1 text-[12px] text-muted">协议
          <select className={input} value={kind} onChange={(e) => setKind(e.currentTarget.value)}>
            {kinds.map((k) => <option key={k} value={k}>{kindText[k] ?? k}</option>)}
          </select>
        </label>
        <label className="grid gap-1 text-[12px] text-muted col-span-2 max-[760px]:col-span-1">地址
          <input className={`${input} font-mono`} value={baseURL} onChange={(e) => setBaseURL(e.currentTarget.value)} placeholder="https://…（不填用这个协议的官方地址）" />
        </label>
        <label className="grid gap-1 text-[12px] text-muted">API Key
          <input className={`${input} font-mono`} type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.currentTarget.value)}
            placeholder={provider?.key_set ? '已保存，留空不改' : '粘贴 Key'} />
        </label>
        <label className="grid gap-1 text-[12px] text-muted">或者从 Hub 的环境变量读
          <input className={`${input} font-mono`} value={env} onChange={(e) => setEnv(e.currentTarget.value)} placeholder="如 DEEPSEEK_API_KEY" />
        </label>
        <label className="grid gap-1 text-[12px] text-muted col-span-2 max-[760px]:col-span-1">模型（逗号分隔，保存后也可以从供应商读取）
          <input className={`${input} font-mono`} value={models} onChange={(e) => setModels(e.currentTarget.value)} />
        </label>
      </div>
      {provider?.key_set && (
        <label className="flex gap-1.5 items-center text-[13px]"><input type="checkbox" checked={clear} onChange={(e) => setClear(e.currentTarget.checked)} />删除已保存的 Key</label>
      )}
      <div className="flex gap-2">
        <button className="btn small" type="submit">保存</button>
        <button className="btn small ghost" type="button" onClick={onCancel}>取消</button>
      </div>
    </form>
  )
}

/** The supervisor model's context window: when long conversations get folded into a summary. */
function ContextWindow({ value, disabled, onSave }: { value: number; disabled: boolean; onSave: (n: number) => unknown }) {
  const [draft, setDraft] = useState(value ? String(value) : '')
  useEffect(() => setDraft(value ? String(value) : ''), [value])
  const n = Number(draft.replace(/[,_\s]/g, '')) || 0
  return (
    <div className="flex items-center gap-2 flex-wrap text-[13px]">
      <label htmlFor="ctx-window" className="text-muted">主管模型的上下文窗口</label>
      <input
        id="ctx-window"
        className={`${input} w-[150px]`}
        inputMode="numeric"
        placeholder="128000（默认）"
        disabled={disabled}
        value={draft}
        onChange={(e) => setDraft(e.currentTarget.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') e.currentTarget.blur() }}
        onBlur={() => { if (n !== value) void onSave(n) }}
      />
      <span className="text-faint text-[12px]">token。用到 60% 时把较早的对话整理成摘要。</span>
    </div>
  )
}
