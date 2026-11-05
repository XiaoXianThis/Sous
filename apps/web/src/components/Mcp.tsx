import { useEffect, useState } from 'react'
import { ApiError, api, type McpInput } from '../lib/api'
import { reauthenticate } from '../lib/passkey'
import type { McpServer, McpView } from '../lib/types'
import Icon from './Icon'

const input = 'border border-line bg-page text-ink rounded-[10px] px-2.5 py-1.5 text-[13px] min-w-0'

const stateText: Record<McpServer['state'], string> = { off: '已关闭', connecting: '连接中…', ready: '已连接', error: '连接失败' }
const stateTone: Record<McpServer['state'], string> = { off: '', connecting: 'tone-accent', ready: 'tone-success', error: 'tone-danger' }

// Well-known servers, to fill the form in one click.
const presets: { name: string; transport: 'stdio' | 'http'; command?: string; args?: string[]; url?: string; headers?: string }[] = [
  { name: 'fetch', transport: 'stdio', command: 'uvx', args: ['mcp-server-fetch'] },
  { name: 'github', transport: 'http', url: 'https://api.githubcopilot.com/mcp/', headers: 'Authorization: Bearer ' },
  { name: 'context7', transport: 'http', url: 'https://mcp.context7.com/mcp' },
]

const commandLine = (s: McpServer) => (s.transport === 'stdio' ? [s.command, ...s.args].join(' ') : s.url)

/** "NAME=value" per line. */
function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const i = line.indexOf('=')
    if (i > 0 && line.slice(0, i).trim()) out[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return out
}

/** "Name: value" per line. */
function parseHeaders(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const i = line.indexOf(':')
    if (i > 0 && line.slice(0, i).trim()) out[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return out
}

/**
 * MCP servers the supervisor can use. The agent can propose a server, but a
 * proposal only runs after it is approved here. Changing what runs, or where
 * a token goes, needs a recent passkey login.
 */
export default function Mcp() {
  const [view, setView] = useState<McpView | null>(null)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState('') // server id, or "new"
  const [reauth, setReauth] = useState<(() => Promise<void>) | null>(null)

  useEffect(() => {
    api.mcp().then(setView).catch((e) => setError((e as Error).message))
  }, [])

  // A server that is still connecting settles within seconds: look again.
  const connecting = view?.servers.some((s) => s.state === 'connecting') ?? false
  useEffect(() => {
    if (!connecting) return
    const t = setTimeout(() => api.mcp().then(setView).catch(() => {}), 1500)
    return () => clearTimeout(t)
  }, [connecting, view])

  async function run(fn: () => Promise<McpView>, done?: () => void) {
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

  const pending = view.servers.filter((s) => s.pending)
  const servers = view.servers.filter((s) => !s.pending)

  return (
    <div className="grid gap-6">
      {reauth && (
        <div className="flex items-center gap-3 flex-wrap rounded-[12px] px-3 py-2 bg-[color-mix(in_srgb,var(--warm)_12%,transparent)] text-[13px]">
          <Icon name="shield" size={16} />
          <span className="flex-1 min-w-0">这会改变 Hub 上运行的程序或令牌的去向，需要再验证一次 Passkey。</span>
          <button className="btn small" onClick={() => void reauth().catch((e) => setError((e as Error).message))}>验证并继续</button>
          <button className="btn small ghost" onClick={() => setReauth(null)}>取消</button>
        </div>
      )}
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}

      <p className="m-0 text-muted text-[13px]">
        MCP 服务给主管加上新的工具（查资料、读写文档、调用你的系统……）。连上后，它们的工具会自动出现在主管的工具列表里。
        主管也可以自己提议添加服务，但必须在这里批准后才会启动。
      </p>

      {pending.length > 0 && (
        <section className="grid gap-2">
          <h3 className="m-0 text-[14px]">等你批准（{pending.length}）</h3>
          {pending.map((s) =>
            editing === s.id ? (
              <ServerForm key={s.id} server={s} stdioAllowed={view.stdio_allowed} onCancel={() => setEditing('')}
                onSave={(b) => run(() => api.updateMcp(s.id, b), () => setEditing(''))} />
            ) : (
              <PendingCard key={s.id} s={s} onEdit={() => setEditing(s.id)}
                onApprove={(env, headers) => run(async () => {
                  if (Object.keys(env).length || Object.keys(headers).length) await api.updateMcp(s.id, { env, headers })
                  return api.approveMcp(s.id)
                })}
                onReject={() => run(() => api.deleteMcp(s.id))} />
            ),
          )}
        </section>
      )}

      <section className="grid gap-2">
        <div className="flex items-center gap-2">
          <h3 className="m-0 text-[14px] flex-1">服务</h3>
          <button className="btn small" onClick={() => setEditing('new')}><Icon name="plus" size={14} />添加服务</button>
        </div>
        {editing === 'new' && (
          <ServerForm stdioAllowed={view.stdio_allowed} onCancel={() => setEditing('')}
            onSave={(b) => run(() => api.createMcp(b), () => setEditing(''))} />
        )}
        {servers.length === 0 && editing !== 'new' && <p className="m-0 text-muted text-[13px]">还没有 MCP 服务。</p>}
        {servers.map((s) =>
          editing === s.id ? (
            <ServerForm key={s.id} server={s} stdioAllowed={view.stdio_allowed} onCancel={() => setEditing('')}
              onSave={(b) => run(() => api.updateMcp(s.id, b), () => setEditing(''))} />
          ) : (
            <ServerCard key={s.id} s={s} onEdit={() => setEditing(s.id)}
              onToggle={(on) => run(() => api.updateMcp(s.id, { enabled: on }))}
              onTools={(disabled) => run(() => api.updateMcp(s.id, { disabled_tools: disabled }))}
              onReconnect={() => run(() => api.reconnectMcp(s.id))}
              onDelete={() => { if (confirm(`删除 MCP 服务「${s.name}」？`)) void run(() => api.deleteMcp(s.id)) }} />
          ),
        )}
        <p className="m-0 text-faint text-[12px]">
          令牌和环境变量的值只保存在 Hub 上，页面上不会再显示。本地程序形式的服务（stdio）是在 Hub 所在的机器上运行命令，只添加你信任的。
          {!view.stdio_allowed && '这个 Hub 已关闭本地程序形式的服务，只能添加远程（HTTP）服务。'}
        </p>
      </section>
    </div>
  )
}

function PendingCard({ s, onEdit, onApprove, onReject }: {
  s: McpServer
  onEdit: () => void
  onApprove: (env: Record<string, string>, headers: Record<string, string>) => void
  onReject: () => void
}) {
  const [fill, setFill] = useState<Record<string, string>>({})
  const envNames = new Set(s.env_keys.map((k) => k.name))
  const ready = s.missing.every((m) => (fill[m] ?? '').trim() !== '')

  function approve() {
    const env: Record<string, string> = {}
    const headers: Record<string, string> = {}
    for (const m of s.missing) (envNames.has(m) ? env : headers)[m] = fill[m].trim()
    onApprove(env, headers)
  }

  return (
    <div className="card p-3 grid gap-2.5 border border-[color-mix(in_srgb,var(--warm)_45%,transparent)]">
      <div className="flex items-center gap-2 min-w-0 flex-wrap">
        <strong className="text-[14px]">{s.name}</strong>
        <span className="pill">{s.transport === 'stdio' ? '本地程序' : '远程服务'}</span>
        {s.source === 'agent' && <span className="pill tone-warning"><Icon name="sparkles" size={12} />主管提议</span>}
      </div>
      {s.note && <p className="m-0 text-[13px]">{s.note}</p>}
      <code className="text-[12px] text-muted font-mono break-all bg-sunken rounded-[9px] px-2.5 py-1.5">{commandLine(s)}</code>
      <p className="m-0 text-[12px] text-faint">
        {s.transport === 'stdio'
          ? '批准后，Hub 会在它所在的机器上运行上面这条命令。确认命令来自可信的来源再批准。'
          : '批准后，Hub 会连接这个地址，并把下面填写的请求头一起发过去。'}
      </p>
      {s.missing.length > 0 && (
        <div className="grid gap-2">
          {s.missing.map((m) => (
            <label key={m} className="grid gap-1 text-[12px] text-muted">
              {m}（{envNames.has(m) ? '环境变量' : '请求头'}，批准前要填）
              <input className={`${input} font-mono`} type="password" autoComplete="off" value={fill[m] ?? ''}
                onChange={(e) => setFill({ ...fill, [m]: e.currentTarget.value })} />
            </label>
          ))}
        </div>
      )}
      <div className="flex gap-2 flex-wrap">
        <button className="btn small" disabled={!ready} onClick={approve}><Icon name="check" size={14} />批准并启动</button>
        <button className="btn small ghost" onClick={onEdit}>修改</button>
        <button className="btn small ghost hover:!text-danger" onClick={onReject}>拒绝</button>
      </div>
    </div>
  )
}

function ServerCard({ s, onEdit, onToggle, onTools, onReconnect, onDelete }: {
  s: McpServer
  onEdit: () => void
  onToggle: (on: boolean) => void
  onTools: (disabled: string[]) => void
  onReconnect: () => void
  onDelete: () => void
}) {
  const visible = s.tools.filter((t) => !t.disabled).length
  const toggleTool = (name: string, on: boolean) =>
    onTools(on ? s.disabled_tools.filter((x) => x !== name) : [...s.disabled_tools, name])

  return (
    <div className="card p-3 grid gap-2.5">
      <div className="flex items-center gap-2 min-w-0 flex-wrap">
        <strong className="text-[14px]">{s.name}</strong>
        <span className="pill">{s.transport === 'stdio' ? '本地程序' : '远程服务'}</span>
        <span className={`pill ${stateTone[s.enabled ? s.state : 'off']}`}>{stateText[s.enabled ? s.state : 'off']}</span>
        {s.source === 'agent' && <span className="pill">主管提议</span>}
        <span className="flex-1" />
        <label className="inline-flex items-center gap-1.5 text-[12.5px] text-muted">
          <input type="checkbox" checked={s.enabled} onChange={(e) => onToggle(e.currentTarget.checked)} aria-label={`启用 ${s.name}`} />启用
        </label>
        {s.enabled && <button className="icon-btn" aria-label={`重新连接 ${s.name}`} data-tip="重新连接" onClick={onReconnect}><Icon name="refresh" size={16} /></button>}
        <button className="icon-btn" aria-label={`编辑 ${s.name}`} data-tip="编辑" onClick={onEdit}><Icon name="edit" size={16} /></button>
        <button className="icon-btn" aria-label={`删除 ${s.name}`} data-tip="删除" onClick={onDelete}><Icon name="trash" size={16} /></button>
      </div>
      <code className="text-[12px] text-muted font-mono overflow-hidden text-ellipsis whitespace-nowrap">{commandLine(s)}</code>
      {s.enabled && s.state === 'error' && s.error && (
        <pre className="m-0 text-[12px] text-danger whitespace-pre-wrap break-words font-mono max-h-[120px] overflow-y-auto">{s.error}</pre>
      )}
      {s.tools.length > 0 && (
        <details className="text-[13px]">
          <summary className="cursor-pointer text-muted">{s.tools.length} 个工具，{visible} 个对主管可见</summary>
          <ul className="list-none m-0 mt-2 p-0 grid gap-1.5">
            {s.tools.map((t) => (
              <li key={t.name}>
                <label className="grid grid-cols-[auto_1fr] gap-x-2 items-baseline">
                  <input type="checkbox" checked={!t.disabled} onChange={(e) => toggleTool(t.name, e.currentTarget.checked)} aria-label={`让主管使用 ${t.name}`} />
                  <span className="min-w-0">
                    <code className="font-mono text-[12.5px]">{t.name}</code>
                    {t.read_only && <span className="pill ml-1.5">只读</span>}
                    {t.description && <span className="block text-[12px] text-faint">{t.description}</span>}
                  </span>
                </label>
              </li>
            ))}
          </ul>
          <p className="m-0 mt-2 text-[12px] text-faint">取消勾选的工具不会出现在主管的工具列表里。工具越多，每次对话占用的上下文越多。</p>
        </details>
      )}
    </div>
  )
}

function ServerForm({ server, stdioAllowed, onSave, onCancel }: {
  server?: McpServer
  stdioAllowed: boolean
  onSave: (b: McpInput) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(server?.name ?? '')
  const [transport, setTransport] = useState<'stdio' | 'http'>(server?.transport ?? (stdioAllowed ? 'stdio' : 'http'))
  const [command, setCommand] = useState(server?.command ?? '')
  const [args, setArgs] = useState((server?.args ?? []).join('\n'))
  const [url, setUrl] = useState(server?.url ?? '')
  const [env, setEnv] = useState('')
  const [headers, setHeaders] = useState('')
  const [dropEnv, setDropEnv] = useState<string[]>([])
  const [dropHeaders, setDropHeaders] = useState<string[]>([])

  function save(e: React.FormEvent) {
    e.preventDefault()
    const b: McpInput = { name, transport }
    if (transport === 'stdio') {
      b.command = command
      b.args = args.split('\n').map((a) => a.trim()).filter(Boolean)
      b.env = parseEnv(env)
      if (dropEnv.length) b.remove_env = dropEnv
    } else {
      b.url = url
      b.headers = parseHeaders(headers)
      if (dropHeaders.length) b.remove_headers = dropHeaders
    }
    onSave(b)
  }

  const keys = (transport === 'stdio' ? server?.env_keys : server?.header_names) ?? []
  const dropped = transport === 'stdio' ? dropEnv : dropHeaders
  const setDropped = transport === 'stdio' ? setDropEnv : setDropHeaders

  return (
    <form className="card p-3 grid gap-2.5" onSubmit={save}>
      {!server && (
        <div className="flex flex-wrap gap-1.5 items-center">
          <span className="text-[12px] text-muted">常用：</span>
          {presets.filter((p) => p.transport === 'http' || stdioAllowed).map((p) => (
            <button key={p.name} type="button" className="h-7 px-2.5 rounded-[9px] border border-line bg-panel text-[12.5px]"
              onClick={() => {
                setName(p.name)
                setTransport(p.transport)
                setCommand(p.command ?? '')
                setArgs((p.args ?? []).join('\n'))
                setUrl(p.url ?? '')
                setHeaders(p.headers ?? '')
              }}>{p.name}</button>
          ))}
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 max-[760px]:grid-cols-1">
        <label className="grid gap-1 text-[12px] text-muted">名字
          <input className={input} value={name} onChange={(e) => setName(e.currentTarget.value)} required placeholder="如 github" />
        </label>
        <label className="grid gap-1 text-[12px] text-muted">类型
          <select className={input} value={transport} onChange={(e) => setTransport(e.currentTarget.value as 'stdio' | 'http')}>
            <option value="http">远程服务（HTTP）</option>
            {(stdioAllowed || transport === 'stdio') && <option value="stdio" disabled={!stdioAllowed}>本地程序（在 Hub 上运行）</option>}
          </select>
        </label>
        {transport === 'stdio' ? (
          <>
            <label className="grid gap-1 text-[12px] text-muted col-span-2 max-[760px]:col-span-1">命令（只填可执行文件）
              <input className={`${input} font-mono`} value={command} onChange={(e) => setCommand(e.currentTarget.value)} placeholder="npx、uvx 或程序的完整路径" required />
            </label>
            <label className="grid gap-1 text-[12px] text-muted col-span-2 max-[760px]:col-span-1">参数（每行一个）
              <textarea className={`${input} font-mono`} rows={3} value={args} onChange={(e) => setArgs(e.currentTarget.value)} placeholder={'-y\n@scope/some-mcp-server'} />
            </label>
          </>
        ) : (
          <label className="grid gap-1 text-[12px] text-muted col-span-2 max-[760px]:col-span-1">地址
            <input className={`${input} font-mono`} value={url} onChange={(e) => setUrl(e.currentTarget.value)} placeholder="https://…/mcp" required />
          </label>
        )}
        <label className="grid gap-1 text-[12px] text-muted col-span-2 max-[760px]:col-span-1">
          {transport === 'stdio' ? '环境变量（每行 NAME=值，如 API Key）' : '请求头（每行 名字: 值，如 Authorization: Bearer …）'}
          <textarea className={`${input} font-mono`} rows={2} autoComplete="off" spellCheck={false}
            value={transport === 'stdio' ? env : headers}
            onChange={(e) => (transport === 'stdio' ? setEnv : setHeaders)(e.currentTarget.value)}
            placeholder={server && keys.length ? '留空则不改已保存的值；填写同名的行会覆盖它' : ''} />
        </label>
      </div>
      {keys.length > 0 && (
        <div className="flex flex-wrap gap-1.5 items-center text-[12.5px]">
          <span className="text-muted">已保存：</span>
          {keys.map((k) => (
            <span key={k.name} className={`inline-flex items-center gap-1 h-7 pl-2.5 pr-1 rounded-[9px] bg-sunken font-mono ${dropped.includes(k.name) ? 'line-through opacity-50' : ''}`}>
              {k.name}{!k.set && <span className="text-warm">（缺值）</span>}
              <button type="button" className="icon-btn !w-6 !h-6" aria-label={`${dropped.includes(k.name) ? '保留' : '删除'} ${k.name}`}
                onClick={() => setDropped(dropped.includes(k.name) ? dropped.filter((x) => x !== k.name) : [...dropped, k.name])}>
                <Icon name={dropped.includes(k.name) ? 'refresh' : 'x'} size={12} />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="flex gap-2">
        <button className="btn small" type="submit">保存</button>
        <button className="btn small ghost" type="button" onClick={onCancel}>取消</button>
      </div>
    </form>
  )
}
