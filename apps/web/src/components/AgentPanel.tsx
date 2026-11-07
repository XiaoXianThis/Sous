import { lazy, Suspense, useEffect, useReducer, useRef, useState } from 'react'
import { api } from '../lib/api'
import { chat, useChat } from '../lib/app'
import { jobTitle, latestJob } from '../lib/jobs'
import type { ConfigOption, Node } from '../lib/types'
import ConfigEditor from './ConfigEditor'
const JobLog = lazy(() => import('./JobLog'))
import Icon from './Icon'
import Sheet from './Sheet'

const catName: Record<string, string> = { model: '模型', thought_level: '思考强度' }

export default function AgentPanel({ node, name, onClose }: { node: Node; name: string; onClose: () => void }) {
  const chatState = useChat()
  const [, force] = useReducer((x: number) => x + 1, 0)
  const agent = node.agents?.find((a) => a.name === name)
  const listedJob = agent?.job

  // A job started before this page loaded is known from the inventory;
  // JobLog then fetches its output so far.
  useEffect(() => {
    if (listedJob && !chat.state.jobs[listedJob.job_id]) {
      chat.state.jobs[listedJob.job_id] = {
        node_id: node.id, job_id: listedJob.job_id, kind: listedJob.kind, agent: listedJob.agent,
        command: listedJob.command, data: '', end: 0, epoch: 0, running: true,
      }
      chat.emit()
    }
  }, [listedJob?.job_id, node.id])

  const job = latestJob(chatState.jobs, node.id, name)
  const canAdmin = node.online && node.admin === 'on'

  const [error, setError] = useState('')
  const [probing, setProbing] = useState(false)
  const [probed, setProbed] = useState(false)
  const [editing, setEditing] = useState<{ agent: string; file: string; title: string } | null>(null)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const draftFor = useRef('')
  const [savingDefaults, setSavingDefaults] = useState(false)
  const [savedDefaults, setSavedDefaults] = useState(false)
  const [keyDraft, setKeyDraft] = useState<Record<string, string>>({})

  // Start the draft from the machine's defaults once per agent.
  useEffect(() => {
    if (agent && draftFor.current !== agent.name) {
      draftFor.current = agent.name
      setDraft({ ...(agent.defaults ?? {}) })
    }
  }, [agent?.name, agent?.defaults])

  const order: Record<string, number> = { model: 0, thought_level: 1, model_config: 2 }
  const options: ConfigOption[] = [...(agent?.options ?? [])].sort((a, b) => (order[a.category ?? ''] ?? 9) - (order[b.category ?? ''] ?? 9))
  const defaultsDirty =
    JSON.stringify(Object.entries(draft).filter(([, v]) => v).sort()) !==
    JSON.stringify(Object.entries(agent?.defaults ?? {}).filter(([, v]) => v).sort())

  function groups(o: ConfigOption) {
    const out: { name: string; values: ConfigOption['values'] }[] = []
    for (const v of o.values) {
      const g = v.group ?? ''
      let last = out[out.length - 1]
      if (!last || last.name !== g) out.push((last = { name: g, values: [] }))
      last.values.push(v)
    }
    return out
  }
  const valueName = (o: ConfigOption, v: string) => o.values.find((x) => x.value === v)?.name ?? v

  async function probe() {
    setProbing(true)
    setError('')
    try {
      const r = await api.probeAgent(node.id, name)
      // Show them now; the inventory with the same options follows.
      if (agent) {
        agent.options = r.options ?? []
        agent.options_at = r.at
        force()
      }
      setProbed(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setProbing(false)
    }
  }

  async function saveDefaults() {
    setSavingDefaults(true)
    try {
      const clean = Object.fromEntries(Object.entries(draft).filter(([, v]) => v))
      await api.setAgentDefaults(node.id, name, clean)
      if (agent) {
        agent.defaults = clean
        force()
      }
      setSavedDefaults(true)
      setTimeout(() => setSavedDefaults(false), 2000)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSavingDefaults(false)
    }
  }

  async function start(what: 'install' | 'upgrade' | 'login' | 'logout') {
    if (!agent) return
    const cmd = { install: agent.install, upgrade: agent.upgrade, login: agent.login, logout: agent.logout }[what]
    const why = {
      install: '会从官方渠道下载并安装。',
      upgrade: '会更新到最新版本。',
      login: '按终端里的提示完成登录：打开链接，需要时把页面上给出的授权码粘贴回来。',
      logout: '',
    }[what]
    if (!confirm(`在“${node.name}”上${jobTitle[what]} ${agent.title}？\n\n将运行：\n${cmd}\n\n${why}`)) return
    try {
      await api.agentJob(node.id, name, what === 'upgrade' ? 'install' : what, { cols: 80, rows: 24 }, what === 'upgrade')
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function setKey(env: string, clear = false) {
    const value = clear ? '' : (keyDraft[env] ?? '').trim()
    if (!clear && !value) return
    if (clear && !confirm(`清除 ${env}？`)) return
    try {
      await api.setAgentEnv(node.id, name, env, value)
      setKeyDraft({ ...keyDraft, [env]: '' })
      if (agent) {
        const set = new Set(agent.env_set ?? [])
        if (clear) set.delete(env)
        else set.add(env)
        agent.env_set = [...set].sort()
        force()
      }
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const status = !agent ? '' : !agent.installed ? '未安装' : agent.logged_in === false ? '未登录' : agent.logged_in ? '已登录' : '登录状态未知'
  const statusClass = !agent?.installed ? 'text-muted' : agent.logged_in === false ? 'text-danger' : agent.logged_in ? 'text-done' : 'text-muted'

  return editing ? (
    <ConfigEditor nodeId={node.id} agent={editing.agent} file={editing.file} title={editing.title} onClose={() => setEditing(null)} />
  ) : (
    <Sheet title={`${agent?.title ?? name} · ${node.name}`} onClose={onClose} wide>
    <div className="relative grid gap-3.5 content-start min-h-[220px]">
      <div className={`grid gap-3.5 content-start ${node.online ? '' : 'blur-[5px] pointer-events-none select-none'}`} aria-hidden={node.online ? undefined : true}>
      {!agent ? (
        <p className="text-muted text-[13px] m-0">这台机器没有报告这个 agent。</p>
      ) : (
        <>
          <section className="flex gap-2.5 items-baseline flex-wrap text-[14px]">
            <span className={statusClass}>{status}</span>
            {agent.version && <span className="text-muted text-[13px] m-0">{agent.version}</span>}
            {agent.note && <span className="text-muted text-[13px] m-0">{agent.note}</span>}
          </section>
          {agent.missing?.length ? (
            <p className="text-warm text-[13px] m-0">
              这台机器缺少 {agent.missing.join('、')}{agent.missing.includes('npx') ? '：需要先安装 Node.js（nodejs.org），sous 通过 npx 启动它的 ACP 适配器' : ''}。
            </p>
          ) : null}
          {!node.online ? null : node.admin === 'off' ? (
            <p className="text-warm text-[13px] m-0">这台机器在 node.json 里关闭了远程管理（remote_admin: false），只能在那台电脑上修改。</p>
          ) : node.admin === 'unsupported' ? (
            <p className="text-warm text-[13px] m-0">这台机器上的 sous 版本太旧，不支持远程管理，请先更新 sous。</p>
          ) : null}

          <section className="flex gap-3 items-center flex-wrap">
            {!agent.installed ? (
              agent.install ? (
                <button className="btn small py-[7px] px-4" disabled={!canAdmin || job?.running} onClick={() => void start('install')}>安装</button>
              ) : (
                <span className="text-muted text-[13px] m-0">sous 还不能在这个系统上自动安装它，请按官网说明手动安装。</span>
              )
            ) : (
              <>
                {agent.login && <button className={`btn small py-[7px] px-4 ${agent.logged_in === true ? 'ghost' : ''}`} disabled={!canAdmin || job?.running} onClick={() => void start('login')}>{agent.logged_in ? '重新登录' : '登录'}</button>}
                {agent.upgrade && <button className="btn small ghost py-[7px] px-4" disabled={!canAdmin || job?.running} onClick={() => void start('upgrade')}>升级</button>}
                {agent.logout && agent.logged_in !== false && <button className="link" disabled={!canAdmin || job?.running} onClick={() => void start('logout')}>退出登录</button>}
              </>
            )}
          </section>

          {job && <Suspense fallback={null}><JobLog key={job.job_id} nodeId={node.id} jobId={job.job_id} /></Suspense>}

          {agent.installed && (
            <section className="grid gap-2">
              <h3 className="mt-1.5 mb-0 text-[15px]">模型与思考强度</h3>
              {options.length ? (
                <>
                  <p className="text-muted text-[13px] m-0">新开的会话都会先切到这里选的值；选“agent 默认”就不改。</p>
                  <div className="grid gap-2">
                    {options.map((o) => (
                      <label key={o.id} className="grid grid-cols-[8em_1fr] gap-2.5 items-center text-[14px] max-[760px]:grid-cols-1 max-[760px]:gap-1">
                        <span title={o.description ?? o.name}>{catName[o.category ?? ''] ?? o.name}</span>
                        <select
                          className="border border-line bg-page text-ink rounded-[10px] px-2 py-1.5 text-[14px] min-w-0"
                          value={draft[o.id] ?? ''}
                          onChange={(e) => setDraft({ ...draft, [o.id]: e.currentTarget.value })}
                          disabled={!canAdmin}
                        >
                          <option value="">agent 默认（{valueName(o, o.current)}）</option>
                          {groups(o).map((g) =>
                            g.name ? (
                              <optgroup key={g.name} label={g.name}>
                                {g.values.map((v) => <option key={v.value} value={v.value} title={v.description}>{v.name}</option>)}
                              </optgroup>
                            ) : (
                              g.values.map((v) => <option key={v.value} value={v.value} title={v.description}>{v.name}</option>)
                            ),
                          )}
                        </select>
                      </label>
                    ))}
                  </div>
                  <div className="flex gap-3.5 items-center">
                    <button className="btn small py-[7px] px-4" disabled={!canAdmin || !defaultsDirty || savingDefaults} onClick={() => void saveDefaults()}>{savedDefaults ? '已保存' : '保存'}</button>
                    <button className="link" disabled={!canAdmin || probing} onClick={() => void probe()}>{probing ? '正在读取…' : '重新读取可选项'}</button>
                  </div>
                </>
              ) : probed ? (
                <p className="text-muted text-[13px] m-0">{agent.title} 没有通过 ACP 提供模型或思考强度的选项，可以在下面的配置文件里设置。</p>
              ) : (
                <>
                  <p className="text-muted text-[13px] m-0">启动一次 {agent.title}，读取它支持的模型和思考强度（不会调用模型，可能要等一会儿）。</p>
                  <div className="flex gap-3.5 items-center">
                    <button className="btn small ghost py-[7px] px-4" disabled={!canAdmin || probing || agent.logged_in === false} onClick={() => void probe()}>{probing ? '正在读取…' : '读取可选项'}</button>
                  </div>
                </>
              )}
            </section>
          )}

          {agent.key_env?.length ? (
            <section className="grid gap-2">
              <h3 className="mt-1.5 mb-0 text-[15px]">API Key</h3>
              <p className="text-muted text-[13px] m-0">不想登录账号时，也可以填 API Key。它只保存在这台机器的 node.json 里，页面和 Hub 都不会再显示它。</p>
              {agent.key_env.map((env) => (
                <div key={env} className="grid grid-cols-[17em_1fr_auto_auto] gap-2.5 items-center text-[13px] max-[760px]:grid-cols-[1fr_auto_auto] max-[760px]:gap-y-1">
                  <span className="flex gap-2 items-baseline justify-between max-[760px]:col-span-full max-[760px]:justify-start">
                    <code>{env}</code>
                    <span className={agent.env_set?.includes(env) ? 'text-done' : 'text-muted'}>{agent.env_set?.includes(env) ? '已设置' : '未设置'}</span>
                  </span>
                  <input
                    type="password"
                    autoComplete="off"
                    placeholder={agent.env_set?.includes(env) ? '输入新值替换' : '粘贴密钥'}
                    value={keyDraft[env] ?? ''}
                    onChange={(e) => setKeyDraft({ ...keyDraft, [env]: e.currentTarget.value })}
                    disabled={!canAdmin}
                    onKeyDown={(e) => e.key === 'Enter' && !e.nativeEvent.isComposing && void setKey(env)}
                    className="border border-line bg-page text-ink rounded-[10px] px-2 py-1.5 text-[14px] min-w-0"
                  />
                  <button className="link accent" disabled={!canAdmin || !keyDraft[env]?.trim()} onClick={() => void setKey(env)}>保存</button>
                  {agent.env_set?.includes(env) && <button className="link danger" disabled={!canAdmin} onClick={() => void setKey(env, true)}>清除</button>}
                </div>
              ))}
            </section>
          ) : null}

          {agent.installed && agent.configs?.length ? (
            <section className="grid gap-2">
              <h3 className="mt-1.5 mb-0 text-[15px]">配置文件</h3>
              <ul className="list-none m-0 p-0 grid gap-1.5">
                {agent.configs.map((c) => (
                  <li key={c.id} className="flex gap-3 items-baseline text-[13px]">
                    <code className="[overflow-wrap:anywhere]">{c.path}</code>
                    <button className="link accent" disabled={!canAdmin} onClick={() => setEditing({ agent: name, file: c.id, title: `${agent?.title} · ${c.path}` })}>编辑</button>
                  </li>
                ))}
              </ul>
              {agent.launch_override && <p className="text-muted text-[13px] m-0">node.json 覆盖了它的启动命令（agents 字段）。</p>}
              {agent.default && <p className="text-muted text-[13px] m-0">这是这台电脑的默认 agent（node.json 的 default_agent）：任务、预设和项目都没指定 agent 时用它。</p>}
              {agent.disabled && <p className="text-muted text-[13px] m-0">node.json 的 disabled_agents 停用了它：这台电脑不会用它执行任务。</p>}
              {agent.custom && <p className="text-muted text-[13px] m-0">它是 node.json 的 custom_agents 里添加的 agent；启动命令只能在这台电脑上改。</p>}
            </section>
          ) : null}
        </>
      )}
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
      </div>
      {!node.online && (
        <div className="absolute inset-0 z-[2] grid place-items-center p-6 text-center rounded-[14px] bg-[color-mix(in_srgb,var(--panel)_55%,transparent)]" role="status">
          <div className="grid gap-1.5 justify-items-center">
            <Icon name="server" size={28} className="text-muted" />
            <strong className="text-[16px]">机器离线</strong>
            <span className="text-muted text-[13px]">连上之后才能管理。</span>
          </div>
        </div>
      )}
    </div>
    </Sheet>
  )
}
