import { useEffect, useRef, useState } from 'react'
import { ApiError, api } from '../lib/api'
import { useWb } from '../lib/app'
import { reauthenticate } from '../lib/passkey'
import type { Project } from '../lib/types'
import Icon from './Icon'
import ShellTerm from './ShellTerm'

interface Tab {
  node_id: string
  job_id: string
  /** "MacBook 2": the machine's name and the how-manyth shell on it. */
  label: string
}

interface Place {
  node_id: string
  name: string
  path: string
  online: boolean
  /** Why a shell cannot be opened there, if it cannot. */
  blocked: string
}

/**
 * The project's terminals: tabs of shells, each on one of the machines the
 * project is on, started in its directory there. "+" picks the machine.
 * Shells belong to this login session; another device opens its own.
 */
export default function ShellPanel({ project }: { project: Project }) {
  const wb = useWb()
  const nodes = wb.data?.nodes ?? []
  const places: Place[] = project.nodes.filter((pn) => pn.path).map((pn) => {
    const n = nodes.find((x) => x.id === pn.node_id)
    const blocked = !n?.online ? '不在线' : n.admin === 'off' ? '关闭了远程管理' : n.admin === 'unsupported' ? 'sous 版本太旧' : ''
    return { node_id: pn.node_id, name: n?.name ?? '未知机器', path: pn.path, online: !!n?.online, blocked }
  })
  const [tabs, setTabs] = useState<Tab[]>([])
  const [active, setActive] = useState('')
  const [menu, setMenu] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  /** The machine a shell waits to open on, after the passkey is verified again. */
  const [reauth, setReauth] = useState('')
  const body = useRef<HTMLDivElement>(null)

  function labelFor(list: Tab[], nodeId: string) {
    const name = places.find((p) => p.node_id === nodeId)?.name ?? '机器'
    const n = list.filter((t) => t.node_id === nodeId).length + 1
    return n === 1 ? name : `${name} ${n}`
  }

  useEffect(() => {
    let live = true
    api.shells(project.id).then((r) => {
      if (!live) return
      const list: Tab[] = []
      for (const s of r.shells.filter((x) => x.running)) list.push({ node_id: s.node_id, job_id: s.job_id, label: labelFor(list, s.node_id) })
      setTabs(list)
      if (list.length) setActive(list[list.length - 1].job_id)
    }).catch(() => {})
    return () => { live = false }
  }, [project.id])

  function size() {
    // A first guess for the terminal; it refits once it is on screen.
    const r = body.current?.getBoundingClientRect()
    return { cols: Math.max(40, Math.floor(((r?.width ?? 800) - 16) / 7.8)), rows: Math.max(10, Math.floor(((r?.height ?? 400) - 50) / 17)) }
  }

  async function open(nodeId: string) {
    setMenu(false)
    setError('')
    setBusy(true)
    try {
      const { cols, rows } = size()
      const r = await api.openShell(project.id, nodeId, cols, rows)
      setReauth('')
      setTabs((ts) => [...ts, { node_id: nodeId, job_id: r.job_id, label: labelFor(ts, nodeId) }])
      setActive(r.job_id)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'reauth') setReauth(nodeId)
      else setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function verifyAndOpen() {
    const nodeId = reauth
    setError('')
    try {
      await reauthenticate()
    } catch (e) {
      setError(`验证没有完成：${(e as Error).message}`)
      return
    }
    await open(nodeId)
  }

  function drop(jobId: string) {
    setTabs((ts) => {
      const rest = ts.filter((t) => t.job_id !== jobId)
      if (active === jobId) setActive(rest.length ? rest[rest.length - 1].job_id : '')
      return rest
    })
  }

  async function close(t: Tab) {
    drop(t.job_id)
    api.jobCancel(t.node_id, t.job_id).catch(() => {})
  }

  function plus() {
    const ok = places.filter((p) => !p.blocked)
    if (places.length === 1 && ok.length === 1) void open(ok[0].node_id)
    else setMenu(!menu)
  }

  const picker = (
    <div className="grid gap-1 min-w-[260px]" role="menu">
      {places.length === 0 && <p className="m-0 p-2 text-muted text-[13px]">这个项目还不在任何机器上</p>}
      {places.map((p) => (
        <button
          key={p.node_id}
          role="menuitem"
          disabled={!!p.blocked || busy}
          className="flex items-center gap-2.5 text-left rounded-[10px] px-2.5 py-2 border-0 bg-transparent hover:bg-hover disabled:opacity-50 disabled:hover:bg-transparent min-w-0"
          onClick={() => void open(p.node_id)}
        >
          <Icon name="laptop" size={16} />
          <span className="grid min-w-0 flex-1">
            <span className="text-[13px] font-medium">{p.name}{p.blocked && <span className="text-faint font-normal">（{p.blocked}）</span>}</span>
            <span className="text-[12px] text-muted font-mono overflow-hidden text-ellipsis whitespace-nowrap" title={p.path}>{p.path}</span>
          </span>
        </button>
      ))}
    </div>
  )

  return (
    <div className="grid grid-rows-[auto_minmax(0,1fr)] h-full min-h-0 border-t border-line">
      <div className="flex items-center gap-1 px-3 py-2 min-w-0 overflow-x-auto" role="tablist" aria-label="终端">
        {tabs.map((t) => (
          <div
            key={t.job_id}
            className={`group flex items-center gap-1 h-8 pl-2.5 pr-1 rounded-[10px] text-[13px] flex-none cursor-pointer ${
              t.job_id === active ? 'bg-sunken text-ink font-medium' : 'text-muted hover:text-ink'
            }`}
            role="tab"
            aria-selected={t.job_id === active}
            tabIndex={0}
            onClick={() => setActive(t.job_id)}
            onKeyDown={(e) => e.key === 'Enter' && setActive(t.job_id)}
          >
            <Icon name="terminal" size={14} />
            <span className="whitespace-nowrap">{t.label}</span>
            <button className="icon-btn !w-6 !h-6" aria-label={`关闭 ${t.label}`} onClick={(e) => { e.stopPropagation(); void close(t) }}>
              <Icon name="x" size={12} />
            </button>
          </div>
        ))}
        <div className="relative flex-none">
          <button className="icon-btn" aria-label="新终端" data-tip="新终端：选一台机器" data-tip-pos="bottom" disabled={busy} onClick={plus}>
            <Icon name="plus" size={16} />
          </button>
          {menu && (
            <>
              <div className="fixed inset-0 z-[40]" onClick={() => setMenu(false)}></div>
              <div className="absolute left-0 top-9 z-[41] bg-panel shadow-pop rounded-[14px] p-1.5">{picker}</div>
            </>
          )}
        </div>
      </div>

      <div ref={body} className="min-h-0 px-3 pb-3 grid grid-rows-[auto_minmax(0,1fr)]">
        <div>
          {reauth && (
            <div className="flex items-center gap-3 flex-wrap mb-2 rounded-[12px] px-3 py-2 bg-[color-mix(in_srgb,var(--warm)_12%,transparent)] text-[13px]">
              <Icon name="shield" size={16} />
              <span className="flex-1 min-w-0">打开终端需要再验证一次 Passkey（15 分钟内验证过就不用）。</span>
              <button className="btn small" onClick={() => void verifyAndOpen()}>验证并打开</button>
              <button className="btn small ghost" onClick={() => setReauth('')}>取消</button>
            </div>
          )}
          {error && <p className="text-danger text-[13px] m-0 mb-2" role="alert">{error}</p>}
        </div>
        {tabs.length === 0 ? (
          <div className="grid place-items-center min-h-0 text-muted text-[13px]">
            <div className="grid justify-items-center gap-3">
              <Icon name="terminal" size={28} />
              <span>在这个项目的目录里开一个终端，选一台机器：</span>
              <div className="bg-panel shadow-card rounded-[14px] p-1.5">{picker}</div>
              <span className="text-faint text-[12px] max-w-[420px] text-center">终端里的命令由你自己输入，不经过 sous 的权限策略；输出不会保存在 Hub 上。</span>
            </div>
          </div>
        ) : (
          <div className="min-h-0">
            {tabs.map((t) => (
              <ShellTerm key={t.job_id} nodeId={t.node_id} jobId={t.job_id} active={t.job_id === active} onGone={() => drop(t.job_id)} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
