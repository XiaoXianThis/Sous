// The user's own controls (decisions 0030, 0031): which machines belong,
// which may think, what the AI may do on its own, and the panic buttons.
// Everything here is signed on this page with the user's identity; what
// widens trust also takes Face ID, and the machines check it themselves.
import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import type { Project } from '../lib/types'
import { nodeAddr, type RosterNode, type Scope, type Scopes } from '../trust/docs'
import { sha256, utf8, b64 } from '../trust/bytes'
import {
  addPasskey, ask, forgetThisDevice, identityFingerprint, localDevice, verifyName, isUnlocked, kick, lock, lockAfter, lockChoices, net, paused, permit, roster,
  saveScopes, scopes, setBrain, setConsole, setFrozen, setLockAfter, setPaused, startPairing, useVault, type PairRequest,
} from '../trust/vault'
import { Cancelled } from '../trust/webauthn'
import Icon from './Icon'

const osName: Record<string, string> = { darwin: 'macOS', linux: 'Linux', windows: 'Windows' }

function useErr() {
  const [error, setError] = useState('')
  const run = async (fn: () => Promise<unknown>) => {
    setError('')
    try {
      await fn()
    } catch (e) {
      if (!(e instanceof Cancelled)) setError((e as Error).message)
    }
  }
  return { error, run, setError }
}

/** The machine thinking for everyone, from the lease. */
export function useBrainHolder(): string | null {
  useVault()
  return isUnlocked() ? net().brain() : null
}

// --- machines -------------------------------------------------------------------

export function RosterPanel() {
  useVault()
  const { error, run } = useErr()
  const [adding, setAdding] = useState(false)
  const r = roster()
  if (!r) return <p className="text-muted text-[13px] m-0">正在读取名册…</p>
  const online = new Set(net().online)
  const holder = net().brain()
  const alerts = net().alerts
  const brains = r.nodes.filter((n) => n.brain)

  return (
    <section className="grid gap-2" aria-label="名册">
      <div className="flex items-center gap-2 flex-wrap">
        <button className="btn small" onClick={() => setAdding(true)}><Icon name="plus" size={15} stroke={2.2} />添加机器</button>
        <PauseButton />
        <span className="text-muted text-[12.5px] ml-auto">名册 v{r.v} · 身份 {identityFingerprint()}</span>
      </div>
      {adding && <AddMachine onDone={() => setAdding(false)} />}
      {!holder && brains.length > 0 && <p className="m-0 text-[13px] text-warm" role="status">没有在线的大脑机器：AI 暂停工作，任务会排队。</p>}
      {brains.length === 0 && r.nodes.length > 0 && <p className="m-0 text-[13px] text-warm" role="status">还没有指定大脑机器：在一台你信任的常开电脑上点“设为大脑”。</p>}
      <ul className="list-none m-0 p-0 grid gap-2">
        {r.nodes.map((n) => (
          <RosterRow key={n.id} n={n} online={online.has(nodeAddr(n.id))} thinking={holder === n.id} alert={alerts[n.id]} run={run} />
        ))}
      </ul>
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
    </section>
  )
}

function RosterRow({ n, online, thinking, alert, run }: {
  n: RosterNode; online: boolean; thinking: boolean; alert?: { frozen: boolean; reason: string }; run: (fn: () => Promise<unknown>) => Promise<void>
}) {
  const [audit, setAudit] = useState(false)
  const brainCount = roster()?.nodes.filter((x) => x.brain).length ?? 0
  return (
    <li className="bg-sunken rounded-[14px] px-3.5 py-3 grid gap-2">
      <div className="flex items-center gap-2 flex-wrap">
        <span className={`w-2.5 h-2.5 rounded-full ${online ? 'bg-[var(--online)]' : 'bg-faint'}`} aria-label={online ? '在线' : '离线'}></span>
        <span className="font-medium text-[14px]">{n.name}</span>
        <span className="text-muted text-[12px]">{osName[n.os ?? ''] ?? n.os}{n.role === 'server' ? ' · 服务器' : ''}</span>
        {n.brain && <span className="pill">{thinking ? '正在思考' : '大脑'} · 优先级 {n.brain.priority}</span>}
        {n.frozen && <span className="pill tone-danger">已冻结</span>}
        {n.console && !n.brain && <span className="pill" data-tip="这台机器上的 sous chat 能看全部对话、处理所有审批">终端对话</span>}
        {alert?.frozen && <span className="pill tone-danger" title={alert.reason}>自我冻结</span>}
        {n.key_storage === 'software' && <span className="text-faint text-[12px]" data-tip="密钥存在文件里（权限 0600），不在安全芯片里">软件密钥</span>}
      </div>
      {alert?.frozen && <p className="m-0 text-[12.5px] text-danger">这台机器收到了越权指令，已自己停下：{alert.reason}</p>}
      <div className="flex gap-3 flex-wrap text-[13px]">
        {n.frozen ? (
          <button className="link" onClick={() => void run(() => setFrozen(n.id, false))}>解冻（刷 Face ID）</button>
        ) : (
          <button className="link" onClick={() => void run(() => setFrozen(n.id, true))}>冻结</button>
        )}
        {alert?.frozen && (
          <button className="link" onClick={() => void run(() => permit(n.id, 'unfreeze', {}, true))}>解除自我冻结（刷 Face ID）</button>
        )}
        {n.brain ? (
          <button className="link" onClick={() => void run(() => setBrain(n.id, null))}>不再当大脑</button>
        ) : (
          <button className="link" onClick={() => {
            if (n.role === 'server' && !confirm(`“${n.name}”是服务器。大脑能读到它参与的项目的数据，也能指挥其他机器；服务器暴露在外，不建议当大脑。仍要设为大脑吗？`)) return
            void run(() => setBrain(n.id, brainCount))
          }}>设为大脑</button>
        )}
        {n.brain && brainCount > 1 && (
          <button className="link" onClick={() => void run(() => setBrain(n.id, Math.max(0, n.brain!.priority - 1)))}>提高优先级</button>
        )}
        {!n.brain && (n.console ? (
          <button className="link" onClick={() => void run(() => setConsole(n.id, false))}>关闭终端对话</button>
        ) : (
          <button className="link" data-tip="让这台机器上的 sous chat 能看全部对话、处理所有审批" onClick={() => {
            if (n.role === 'server' && !confirm(`“${n.name}”是服务器。打开后，拿到这台服务器的人就能读你所有对话、替你审批。仍要打开吗？`)) return
            void run(() => setConsole(n.id, true))
          }}>开放终端对话（刷 Face ID）</button>
        ))}
        <button className="link" onClick={() => setAudit((a) => !a)}>{audit ? '收起审计' : '审计记录'}</button>
        <button className="link text-danger" onClick={() => {
          if (!confirm(`踢出“${n.name}”？\n\n它会立刻被所有机器拒绝，删除自己的密钥，之后读不到任何新数据。要再用需要重新配对。`)) return
          void run(() => kick(n.id))
        }}>踢出（刷 Face ID）</button>
      </div>
      {audit && <AuditLog nodeId={n.id} />}
    </li>
  )
}

function PauseButton() {
  useVault()
  const { error, run } = useErr()
  const p = paused()
  return (
    <>
      <button className={`btn small ${p ? '' : 'ghost'}`} onClick={() => void run(() => setPaused(!p))}
        data-tip={p ? '恢复 AI 的工作' : '立刻停止 AI 的所有工作（不需要 Face ID）'}>
        <Icon name={p ? 'play' : 'pause'} size={15} />{p ? '恢复工作' : '全部暂停'}
      </button>
      {error && <span className="text-danger text-[12.5px]">{error}</span>}
    </>
  )
}

function AddMachine({ onDone }: { onDone: () => void }) {
  const [s, setS] = useState<Awaited<ReturnType<typeof startPairing>> | null>(null)
  const [req, setReq] = useState<PairRequest | null>(null)
  const [brain, setBrainChoice] = useState(false)
  const [consoleOn, setConsoleOn] = useState<boolean | null>(null)
  const { error, run, setError } = useErr()
  const [done, setDone] = useState(false)

  useEffect(() => {
    let cancel = () => {}
    startPairing().then((x) => {
      setS(x)
      cancel = x.cancel
      x.request.then(setReq).catch((e) => setError((e as Error).message))
    }).catch((e) => setError((e as Error).message))
    return () => cancel()
  }, [setError])

  const relay = net() ? roster()?.relay ?? '' : ''
  const command = s ? `sous pair ${relay} ${s.code}` : ''
  return (
    <div className="border border-dashed border-accent rounded-[14px] px-3 py-2.5 grid gap-2">
      {done ? (
        <>
          <p className="m-0 text-[14px]">已添加“{req?.name}”。</p>
          <button className="link justify-self-start" onClick={onDone}>完成</button>
        </>
      ) : !req ? (
        <>
          <p className="m-0 text-[14px]">在要添加的电脑上运行（10 分钟内有效，保持这个窗口打开）：</p>
          <code className="font-mono text-[12.5px] bg-page px-2.5 py-2 rounded-[10px] [overflow-wrap:anywhere] select-all">{command || '正在生成…'}</code>
          <p className="text-muted text-[12.5px] m-0">配对串由你亲手复制过去，不经过中继，所以中继没法冒充任何一方。服务器加上 <code>--role server</code>。</p>
          <div className="flex gap-3 items-center">
            <button className="btn small ghost py-[7px] px-4" disabled={!s} onClick={() => void navigator.clipboard?.writeText(command)}>复制命令</button>
            <button className="link" onClick={onDone}>取消</button>
          </div>
        </>
      ) : (
        <>
          <p className="m-0 text-[14px]">“{req.name}”（{osName[req.os] ?? req.os}{req.role === 'server' ? '，服务器' : ''}）请求加入。</p>
          <label className="flex gap-2 items-center text-[13px]">
            <input type="checkbox" checked={brain} onChange={(e) => setBrainChoice(e.currentTarget.checked)} />
            设为大脑机器（AI 在这里思考；只选你信任的常开电脑）
          </label>
          {!brain && (
            <label className="flex gap-2 items-center text-[13px]">
              <input type="checkbox" checked={consoleOn ?? req.role !== 'server'} onChange={(e) => setConsoleOn(e.currentTarget.checked)} />
              开放终端对话（这台机器上的 sous chat 能看全部对话、处理所有审批；服务器不建议）
            </label>
          )}
          <div className="flex gap-3 items-center">
            <button className="btn small" onClick={() => void run(async () => {
              await s!.confirm(brain ? (roster()?.nodes.filter((n) => n.brain).length ?? 0) : null, !brain && (consoleOn ?? req.role !== 'server'))
              setDone(true)
            })}><Icon name="fingerprint" size={15} />刷 Face ID 确认添加</button>
            <button className="link" onClick={onDone}>拒绝</button>
          </div>
        </>
      )}
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
    </div>
  )
}

interface AuditEntry { seq: number; at: number; from: string; type: string; ok: boolean; reason?: string; digest?: string; prev: string; hash: string }

// Go's encoding/json escapes these in strings; JSON.stringify does not.
const goEscapes = new RegExp('[<>&\\u2028\\u2029]', 'g')

/** Recomputes Go's node.AuditHash: the entry's JSON with an empty hash. */
async function auditHash(e: AuditEntry): Promise<string> {
  const o: Record<string, unknown> = { seq: e.seq, at: e.at, from: e.from, type: e.type, ok: e.ok }
  if (e.reason) o.reason = e.reason
  if (e.digest) o.digest = e.digest
  o.prev = e.prev
  o.hash = ''
  const json = JSON.stringify(o).replace(goEscapes, (c) => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'))
  return b64(await sha256(utf8(json)))
}

function AuditLog({ nodeId }: { nodeId: string }) {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null)
  const [broken, setBroken] = useState('')
  const { error, run } = useErr()
  useEffect(() => {
    void run(async () => {
      const res = await ask(nodeAddr(nodeId), { k: 'audit.get', since: 0 }, 'audit')
      const es = (res.entries as AuditEntry[] | null) ?? []
      for (let i = 0; i < es.length; i++) {
        if (i > 0 && es[i].prev !== es[i - 1].hash) {
          setBroken(`第 ${es[i].seq} 条接不上前一条：记录被改动过`)
          break
        }
        if ((await auditHash(es[i])) !== es[i].hash) {
          setBroken(`第 ${es[i].seq} 条的哈希不对：记录被改动过`)
          break
        }
      }
      setEntries(es.slice(-80).reverse())
    })
  }, [nodeId, run])
  return (
    <div className="grid gap-1 text-[12.5px] max-h-[260px] overflow-y-auto">
      {broken && <p className="m-0 text-danger">{broken}</p>}
      {!entries && !error && <p className="m-0 text-muted">正在向机器要审计记录…</p>}
      {entries?.length === 0 && <p className="m-0 text-muted">还没有记录</p>}
      {entries?.map((e) => (
        <div key={e.seq} className="flex gap-2">
          <span className="text-faint tabular-nums">{new Date(e.at * 1000).toLocaleString('zh-CN')}</span>
          <span className={e.ok ? '' : 'text-danger'}>{e.ok ? '接受' : '拒绝'}</span>
          <span className="font-mono">{e.type}</span>
          <span className="text-muted">{e.from === 'brain' ? '大脑' : e.from === 'self' ? '自己' : '你的会话'}</span>
          {e.reason && <span className="text-muted truncate" title={e.reason}>{e.reason}</span>}
        </div>
      ))}
      {error && <p className="m-0 text-danger">{error}</p>}
    </div>
  )
}

// --- security settings ---------------------------------------------------------

export function Security() {
  useVault()
  const { error, run } = useErr()
  const r = roster()
  const [after, setAfter] = useState(lockAfter())
  return (
    <div className="grid gap-5 max-w-[640px]">
      <section className="grid gap-2">
        <h3 className="m-0 text-[15px]">解锁</h3>
        <p className="m-0 text-muted text-[13px]">打开 sous 时验证一次（iPhone 用 Face ID，Android 用指纹，Mac 用触控 ID 或开机密码，Windows 用 PIN），之后直到退出或自动锁定都不用再验证。只有添加或踢出机器、修改授权、打开终端、更新这类关键操作才需要再刷一次。</p>
        <label className="flex items-center gap-2 text-[13px]">
          无操作后自动锁定
          <select className="border border-line bg-page rounded-[10px] px-2 py-1.5" value={after}
            onChange={(e) => { const v = Number(e.currentTarget.value); setAfter(v); setLockAfter(v) }}>
            {lockChoices.map((c) => <option key={c.s} value={c.s}>{c.text}</option>)}
          </select>
        </label>
        <button className="btn small ghost justify-self-start" onClick={lock}><Icon name="lock" size={15} />现在锁定</button>
      </section>

      <section className="grid gap-2">
        <h3 className="m-0 text-[15px]">Passkey</h3>
        <p className="m-0 text-muted text-[13px]">这些 Passkey 是你唯一的钥匙。同步的 Passkey（iCloud 钥匙串、Google 密码管理器）丢一台设备也不会丢钥匙；全部丢失时聊天数据无法恢复，机器要在本机执行 <code>sous trust reset</code> 后重新配对。</p>
        <ul className="list-none m-0 p-0 grid gap-1 text-[13px]">
          {r?.user.passkeys.map((p) => (
            <li key={p.cred_id} className="flex gap-2"><Icon name="key" size={14} />{p.label || 'Passkey'}<span className="text-faint">{p.added ? new Date(p.added * 1000).toLocaleDateString('zh-CN') : ''}</span></li>
          ))}
        </ul>
        <ThisDevice run={run} />
      </section>

      <ScopeEditor />
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
    </div>
  )
}

function ScopeEditor() {
  useVault()
  const cur = scopes()
  const [draft, setDraft] = useState<Scopes>(cur)
  const [projects, setProjects] = useState<Project[]>([])
  const { error, run } = useErr()
  const [saved, setSaved] = useState(false)
  useEffect(() => setDraft(cur), [cur.v]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    api.projects().then((p) => setProjects(p.projects)).catch(() => {})
  }, [])
  const nodes = roster()?.nodes ?? []
  const changed = useMemo(() => JSON.stringify(draft) !== JSON.stringify(cur), [draft, cur])

  const edit = (key: string | null, patch: Partial<Scope>) => {
    setSaved(false)
    setDraft((d) => key === null
      ? { ...d, default: { ...d.default, ...patch } }
      : { ...d, projects: { ...(d.projects ?? {}), [key]: { ...(d.projects?.[key] ?? d.default), ...patch } } })
  }
  const row = (key: string | null, sc: Scope) => (
    <div className="grid gap-1.5 text-[13px]">
      <label className="flex items-center gap-2">
        最高权限模式
        <select className="border border-line bg-page rounded-[8px] px-2 py-1" value={sc.max_mode || 'auto'} onChange={(e) => edit(key, { max_mode: e.currentTarget.value as 'auto' })}>
          <option value="default">default（每步都问）</option>
          <option value="auto">auto（常规操作自动放行）</option>
        </select>
      </label>
      <label className="flex items-center gap-2"><input type="checkbox" checked={!!sc.allow_merge} onChange={(e) => edit(key, { allow_merge: e.currentTarget.checked })} />可以自己合并分支</label>
      <label className="flex items-center gap-2"><input type="checkbox" checked={!!sc.allow_ops} onChange={(e) => edit(key, { allow_ops: e.currentTarget.checked })} />可以自己做运维和部署</label>
      <label className="flex items-center gap-2"><input type="checkbox" checked={!!sc.allow_open} onChange={(e) => edit(key, { allow_open: e.currentTarget.checked })} />可以用 open 模式（完全放开）</label>
      <div className="flex items-center gap-2 flex-wrap">
        机器：
        {nodes.map((n) => (
          <label key={n.id} className="flex items-center gap-1">
            <input type="checkbox" checked={!sc.nodes?.length || sc.nodes.includes(n.id)} onChange={(e) => {
              const all = sc.nodes?.length ? sc.nodes : nodes.map((x) => x.id)
              const next = e.currentTarget.checked ? [...all, n.id] : all.filter((x) => x !== n.id)
              edit(key, { nodes: next.length === nodes.length ? [] : next })
            }} />{n.name}
          </label>
        ))}
      </div>
    </div>
  )
  return (
    <section className="grid gap-2">
      <h3 className="m-0 text-[15px]">AI 自主范围</h3>
      <p className="m-0 text-muted text-[13px]">AI 在范围内自己干活，不打扰你；超出范围的事在页面上点一下“许可”。每台机器自己检查这个范围，大脑改不了它。改范围要刷 Face ID。</p>
      <div className="bg-sunken rounded-[14px] p-3 grid gap-2">
        <span className="font-medium text-[13.5px]">默认</span>
        {row(null, draft.default)}
      </div>
      {projects.filter((p) => p.remote_key).map((p) => {
        const key = p.remote_key!
        const own = draft.projects?.[key]
        return (
          <div key={p.id} className="bg-sunken rounded-[14px] p-3 grid gap-2">
            <label className="flex items-center gap-2 text-[13.5px] font-medium">
              <input type="checkbox" checked={!!own} onChange={(e) => {
                setSaved(false)
                setDraft((d) => {
                  const projects = { ...(d.projects ?? {}) }
                  if (e.currentTarget.checked) projects[key] = { ...d.default }
                  else delete projects[key]
                  return { ...d, projects }
                })
              }} />{p.name} 单独设置
            </label>
            {own && row(key, own)}
          </div>
        )
      })}
      <div className="flex gap-3 items-center">
        <button className="btn small" disabled={!changed} onClick={() => void run(async () => {
          await saveScopes({ default: draft.default, projects: draft.projects })
          setSaved(true)
        })}><Icon name="fingerprint" size={15} />刷 Face ID 保存</button>
        {saved && <span className="text-muted text-[13px]">已保存，所有机器立即生效</span>}
      </div>
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
    </section>
  )
}

/** Shown while no brain machine is online: the controls still work. */
export function BrainOffline({ message }: { message: string }) {
  return (
    <main className="min-h-full grid place-items-center p-4">
      <div className="w-[min(640px,100%)] bg-panel shadow-pop rounded-[22px] p-6 grid gap-3">
        <h1 className="m-0 text-[20px]">大脑离线</h1>
        <p className="m-0 text-muted text-[13.5px]">{message}</p>
        <p className="m-0 text-muted text-[13.5px]">聊天和任务要等一台大脑机器上线。机器的冻结、踢出和全部暂停不需要大脑，现在就能用。</p>
        <RosterPanel />
        <button className="btn small ghost justify-self-start" onClick={lock}><Icon name="lock" size={15} />锁定</button>
      </div>
    </main>
  )
}

/** This device's own passkey: unlock with Face ID / Touch ID / fingerprint / PIN, no phone. */
function ThisDevice({ run }: { run: (fn: () => Promise<unknown>) => Promise<void> }) {
  const [, redraw] = useState(0)
  const d = localDevice()
  const r = roster()
  const known = d && r?.user.passkeys.some((p) => p.cred_id === d.cred_id)
  return (
    <div className="grid gap-1.5 text-[13px]">
      {known ? (
        <>
          <p className="m-0">这台设备直接用{verifyName()}解锁{d!.mode === 'device' ? '（设备密钥：这台设备的 Passkey 不支持 PRF，身份由只存在这个浏览器里的密钥加密，中继核对你的验证后才交出密文）' : ''}。</p>
          <button className="btn small ghost justify-self-start" onClick={() => void run(async () => { await forgetThisDevice(); redraw((n) => n + 1) })}>不再让这台设备直接解锁</button>
        </>
      ) : (
        <>
          <p className="m-0 text-muted">这台设备还没有自己的 Passkey，解锁时可能要用手机扫码。添加后就用{verifyName()}解锁（需要再用已有的 Passkey 验证一次）。</p>
          <button className="btn small ghost justify-self-start" onClick={() => void run(async () => { await addPasskey(); redraw((n) => n + 1) })}>让这台设备直接解锁</button>
        </>
      )}
    </div>
  )
}
