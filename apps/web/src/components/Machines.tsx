import { useEffect, useState } from 'react'
import { ago } from '../lib/time'
import { api } from '../lib/api'
import { useChat } from '../lib/app'
import { jobTitle, latestJob } from '../lib/jobs'
import type { AgentInfo, Node, Updates } from '../lib/types'
import AgentIcon from './AgentIcon'
import AgentPanel from './AgentPanel'
import { SheetPlaceContext } from './Sheet'
import Icon from './Icon'
import ConfigEditor from './ConfigEditor'

const osName: Record<string, string> = { darwin: 'macOS', linux: 'Linux', windows: 'Windows' }

export default function Machines({ onClose }: { onClose: () => void }) {
  const chat = useChat()
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [code, setCode] = useState<{ code: string; expires_at: number } | null>(null)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [draftName, setDraftName] = useState('')
  const [updates, setUpdates] = useState<Updates | null>(null)
  const [updating, setUpdating] = useState<Record<string, string>>({})
  const [wsEdit, setWsEdit] = useState<string | null>(null)
  const [wsDraft, setWsDraft] = useState('')
  const [wsBusy, setWsBusy] = useState(false)
  const [panel, setPanel] = useState<{ node: string; agent: string } | null>(null)
  const [nodeFile, setNodeFile] = useState<string | null>(null)
  const [detecting, setDetecting] = useState<Record<string, boolean>>({})

  async function loadUpdates(refresh = false) {
    try {
      setUpdates(await api.updates(refresh))
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    void loadUpdates()
  }, [])

  async function update(n: Node) {
    const tag = updates?.latest?.tag ?? ''
    if (!confirm(`把“${n.name}”上的 sous 更新到 ${tag}？\n\n它会下载新版本、替换自己并重启；正在执行的任务会在重启后接着做。`)) return
    try {
      const r = await api.updateNode(n.id)
      setUpdating((u) => ({ ...u, [n.id]: `正在更新到 ${r.version}…` }))
    } catch (e) {
      setError((e as Error).message)
    }
  }
  const nodeVersion = (id: string) => updates?.nodes.find((x) => x.id === id)

  async function load() {
    try {
      setNodes((await api.nodes()).nodes)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [chat.nodesRev])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !panel && !nodeFile) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [panel, nodeFile, onClose])

  async function pair() {
    try {
      setCode(await api.pairingCode())
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function rename(n: Node) {
    if (!draftName.trim()) return
    try {
      await api.renameNode(n.id, draftName.trim())
      setRenaming(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function remove(n: Node) {
    if (!confirm(`删除机器“${n.name}”？\n\n它会立刻断开，之后需要重新配对才能使用。`)) return
    try {
      await api.removeNode(n.id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  // M5: role and workspaces.
  async function saveWorkspaces(n: Node) {
    setWsBusy(true)
    try {
      await api.setWorkspaces(n.id, wsDraft.split(/[\n,]/).map((x) => x.trim()).filter(Boolean))
      setWsEdit(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setWsBusy(false)
    }
  }
  async function makeServer(n: Node) {
    if (!confirm(`把“${n.name}”设为服务器？\n\n服务器只接运维任务（部署、重启、查服务），不能用完全开放模式，改文件、删除、启停服务都要你批准。\n改回开发机只能在那台机器的 node.json 里删掉 "role"。`)) return
    try {
      await api.setRole(n.id, 'server')
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function detect(n: Node) {
    setDetecting((d) => ({ ...d, [n.id]: true }))
    try {
      await api.detectAgents(n.id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDetecting((d) => ({ ...d, [n.id]: false }))
    }
  }

  const panelNode = panel ? nodes.find((n) => n.id === panel.node) : undefined
  const fileNode = nodeFile ? nodes.find((n) => n.id === nodeFile) : undefined

  // Installed agents first, then the ones that are not.
  const sortAgents = (as: AgentInfo[]) => [...as].sort((a, b) => Number(b.installed) - Number(a.installed))
  function summary(a: AgentInfo) {
    if (!a.installed || !a.defaults) return ''
    const parts: string[] = []
    for (const o of a.options ?? []) {
      const v = a.defaults[o.id]
      if (v && (o.category === 'model' || o.category === 'thought_level')) parts.push(o.values.find((x) => x.value === v)?.name ?? v)
    }
    return parts.length ? `· ${parts.join(' · ')}` : ''
  }
  // A job this page saw start, or one the machine reports (started before the page loaded).
  const jobOf = (n: Node, a: AgentInfo) => {
    const j = latestJob(chat.jobs, n.id, a.name)
    if (j) return j.running ? j : undefined
    return a.job
  }

  const pairCommand = code ? `sous pair ${location.origin} ${code.code}` : ''
  return (
    <SheetPlaceContext.Provider value="nested">
      <div className="backdrop" role="presentation" onClick={onClose}></div>
      <div className="sheet gap-2.5" role="dialog" aria-modal="true" aria-label="机器">
        <header className="flex items-center justify-between">
          <h2 className="m-0 text-[18px]">机器</h2>
          <button className="icon-btn" aria-label="关闭" onClick={onClose}>
            <Icon name="x" size={18} />
          </button>
        </header>
        <p className="text-muted text-[13px] m-0">装了 sous 客户端的电脑。sous 会把编程任务派到这些电脑上的 agent 去做。</p>

        {code ? (
          <div className="border border-dashed border-accent rounded-[14px] px-3 py-2.5 grid gap-2">
            <p className="m-0 text-[14px]">在要添加的电脑上运行（10 分钟内有效）：</p>
            <code className="font-mono text-[13px] bg-page px-2.5 py-2 rounded-[10px] [overflow-wrap:anywhere] select-all">{pairCommand}</code>
            <p className="text-muted text-[13px] m-0">服务器加上 <code>--role server</code>；加上 <code>--workspace ~/Code</code> 指定放项目的目录。</p>
            <div className="flex gap-3 items-center">
              <button className="btn small ghost py-[7px] px-4" onClick={() => void navigator.clipboard?.writeText(pairCommand)}>复制命令</button>
              <button className="link" onClick={() => setCode(null)}>完成</button>
            </div>
          </div>
        ) : (
          <button className="btn small justify-self-start self-start" onClick={() => void pair()}><Icon name="plus" size={15} stroke={2.2} />配对新机器</button>
        )}

        {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}

        {updates && (
          <p className="m-0 text-[13px] text-muted flex gap-1.5 flex-wrap items-baseline">
            Hub {updates.hub.version}
            {updates.latest ? <> · 最新发布 <a className="text-accent" href={updates.latest.url} target="_blank" rel="noreferrer">{updates.latest.tag}</a></> : updates.error ? <> · 检查更新失败：{updates.error}</> : null}
            <button className="link" onClick={() => void loadUpdates(true)}>检查更新</button>
          </p>
        )}
        {updates?.hub.update_available && <p className="text-muted text-[13px] m-0">Hub 有新版本：在服务器上按部署指南替换 sous-hub 并重启服务。</p>}

        <ul className="list-none m-0 p-0 overflow-y-auto grid gap-2">
          {nodes.map((n) => (
            <li key={n.id} className="border border-line rounded-[18px] px-4 py-3.5 grid gap-2.5">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="relative grid place-items-center w-9 h-9 rounded-[12px] bg-sunken text-muted flex-none">
                  <Icon name={n.role === 'server' ? 'server' : 'laptop'} size={19} />
                  <span className={`absolute -bottom-0.5 -right-0.5 w-2.5 h-2.5 rounded-full shadow-[0_0_0_2px_var(--panel)] ${n.online ? 'bg-[var(--online)]' : 'bg-faint'}`}></span>
                </span>
                {renaming === n.id ? (
                  <>
                    <input
                      className="flex-1 min-w-0 border border-line bg-page rounded-[10px] px-2 py-1 text-ink"
                      value={draftName}
                      aria-label="机器名"
                      onChange={(e) => setDraftName(e.currentTarget.value)}
                      onKeyDown={(e) => e.key === 'Enter' && void rename(n)}
                    />
                    <button className="link" onClick={() => void rename(n)}>保存</button>
                    <button className="link" onClick={() => setRenaming(null)}>取消</button>
                  </>
                ) : (
                  <>
                    <strong>{n.name}</strong>
                    <span className="text-muted text-[13px] m-0">
                      {osName[n.os] ?? n.os} · {n.online ? '在线' : `离线 ${ago(n.last_seen)}`}{n.version ? ` · sous ${n.version}` : ''}
                    </span>
                  </>
                )}
              </div>
              {n.agents?.length ? (
                <table className="border-collapse text-[13px] w-full">
                  <tbody>
                    {sortAgents(n.agents).map((a) => (
                      <tr key={a.name}>
                        <td className={`py-1.5 pr-3 ${a.installed ? 'font-medium' : 'text-muted'}`}>
                          <span className={`inline-flex items-center gap-2 whitespace-nowrap ${a.installed ? '' : 'grayscale opacity-60'}`}><AgentIcon name={a.name} size={22} />{a.title}</span>
                          {a.default && <span className="pill tone-accent ml-1.5" title="这台电脑的默认 agent（node.json 的 default_agent）">默认</span>}
                          {a.disabled && <span className="pill ml-1.5" title="node.json 的 disabled_agents 停用了它，这台电脑不会用它">已停用</span>}
                          {a.custom && <span className="pill ml-1.5" title="node.json 的 custom_agents 里添加的">自定义</span>}
                        </td>
                        <td className="py-1.5 pr-3 text-muted max-[760px]:hidden">{a.installed ? (a.version ?? '') : ''}{summary(a) ? ` ${summary(a)}` : ''}</td>
                        <td className="py-1.5 pr-3">
                          <span
                            className={`pill ${!a.installed ? '' : a.logged_in === false ? 'tone-danger' : a.logged_in ? 'tone-success' : ''}`}
                            title={!a.installed ? '未安装' : a.logged_in === false ? '未登录' : a.logged_in ? '已登录' : '登录状态未知'}
                          >
                            <Icon name={!a.installed ? 'download' : a.logged_in === false ? 'key' : a.logged_in ? 'check' : 'circle-dashed'} size={11} stroke={2.4} />
                            {!a.installed ? '未安装' : a.logged_in === false ? '未登录' : a.logged_in ? '已登录' : '未知'}
                          </span>
                        </td>
                        <td className="py-1.5 text-right pr-0 whitespace-nowrap">
                          {jobOf(n, a) && <span className="text-muted text-[13px] mr-2">正在{jobTitle[jobOf(n, a)?.kind ?? '']}…</span>}
                          <button className="link accent" onClick={() => setPanel({ node: n.id, agent: a.name })}>
                            {n.admin === 'on' && n.online && !a.installed && a.install ? '安装' : n.admin === 'on' && n.online && a.installed && a.logged_in === false && a.login ? '登录' : '管理'}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : n.online ? (
                <p className="text-muted text-[13px] m-0">还没有收到这台机器的 agent 清单</p>
              ) : null}
              <div className="flex flex-wrap gap-x-3 gap-y-1.5 items-center text-[13px]">
                <span className={`pill ${n.role === 'server' ? 'tone-warning' : ''}`}>
                  <Icon name={n.role === 'server' ? 'server' : 'laptop'} size={11} />{n.role === 'server' ? '服务器' : '开发机'}
                </span>
                {wsEdit === n.id ? (
                  <>
                    <textarea
                      className="flex-1 min-w-[200px] border border-line bg-page rounded-[10px] px-2 py-1 text-[13px]"
                      rows={2}
                      value={wsDraft}
                      onChange={(e) => setWsDraft(e.currentTarget.value)}
                      placeholder="~/Code（每行一个）"
                      aria-label="工作空间"
                    ></textarea>
                    <button className="link accent" disabled={wsBusy} onClick={() => void saveWorkspaces(n)}>保存</button>
                    <button className="link" onClick={() => setWsEdit(null)}>取消</button>
                  </>
                ) : (
                  <>
                    <span className="text-muted text-[13px] m-0" title="sous 在这些目录里找 git 仓库，新项目也放在第一个目录里">工作空间：{n.workspaces?.length ? n.workspaces.join('、') : '未设置'}</span>
                    {n.online && n.admin === 'on' && (
                      <button className="icon-btn w-7 h-7" aria-label="修改工作空间" onClick={() => { setWsEdit(n.id); setWsDraft((n.workspaces ?? []).join('\n')) }}><Icon name="edit" size={14} /></button>
                    )}
                    {n.role !== 'server' && <span className="text-muted text-[13px] m-0">{n.wok ? `wok ${n.wok}` : '没装 wok'}</span>}
                  </>
                )}
                {n.role !== 'server' && n.online && n.admin === 'on' && wsEdit !== n.id && (
                  <button className="link" onClick={() => void makeServer(n)}>设为服务器</button>
                )}
              </div>
              {n.online && n.admin === 'off' && <p className="text-muted text-[13px] m-0">这台机器关闭了远程管理（node.json 里的 remote_admin）。</p>}
              {n.online && n.admin === 'unsupported' && <p className="text-muted text-[13px] m-0">更新这台机器上的 sous 后，就能在这里安装、登录和配置 agent。</p>}
              <div className="flex justify-end items-center gap-0.5 -mb-1.5 -mr-1.5">
                {n.online && n.admin === 'on' && (
                  <>
                    <button className="icon-btn" aria-label="重新检测 agent" data-tip="重新检测" data-tip-pos="bottom" disabled={detecting[n.id]} onClick={() => void detect(n)}><Icon name="refresh" size={16} className={detecting[n.id] ? 'spin' : ''} /></button>
                    <button className="icon-btn" aria-label="编辑 node.json" data-tip="node.json" data-tip-pos="bottom" onClick={() => setNodeFile(n.id)}><Icon name="file" size={16} /></button>
                  </>
                )}
                {updating[n.id] ? (
                  <span className="text-muted text-[13px] m-0">{updating[n.id]}</span>
                ) : nodeVersion(n.id)?.update_available && n.online ? (
                  <button className="btn small" onClick={() => void update(n)}><Icon name="download" size={14} />{updates?.latest?.tag}</button>
                ) : null}
                <button className="icon-btn" aria-label="改名" data-tip="改名" data-tip-pos="bottom" onClick={() => { setRenaming(n.id); setDraftName(n.name) }}><Icon name="edit" size={16} /></button>
                <button className="icon-btn hover:!text-danger" aria-label="删除机器" data-tip="删除" data-tip-pos="bottom" onClick={() => void remove(n)}><Icon name="trash" size={16} /></button>
              </div>
            </li>
          ))}
          {nodes.length === 0 && !loading && (
            <li className="text-muted text-center border border-dashed border-line rounded-[14px] px-3 py-6">还没有机器。点“配对新机器”，然后在电脑上安装 sous 客户端并运行配对命令。</li>
          )}
        </ul>
      </div>

      {/* Opened from a machine, on top of this sheet. */}
      {panel && panelNode && <AgentPanel node={panelNode} name={panel.agent} onClose={() => setPanel(null)} />}
      {nodeFile && fileNode && <ConfigEditor nodeId={fileNode.id} agent="sous" file="node" title={`${fileNode.name} · node.json`} onClose={() => setNodeFile(null)} />}
    </SheetPlaceContext.Provider>
  )
}
