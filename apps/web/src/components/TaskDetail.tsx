import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import { useChat } from '../lib/app'
import { agentChoices, isOpen, modeText, statusText } from '../lib/tasks'
import type { Node, Persona, Project, Run, Task } from '../lib/types'
import AgentIcon, { agentTitle } from './AgentIcon'
import Avatar from './Avatar'
import Icon from './Icon'
import Sheet from './Sheet'

const inputCls = 'border border-line bg-panel rounded-[12px] px-3 py-2 text-ink'

export default function TaskDetail({ id, onClose, onOpenTask, onOpenThread, projectId: presetProject = '', compact = false }: {
  id: string // 'new' for a new task
  onClose: () => void
  onOpenTask: (id: string) => void
  onOpenThread: (id: string) => void
  /** A new task's project. */
  projectId?: string
  /** Embedded in the task page: no run list (the page shows the runs). */
  compact?: boolean
}) {
  const chat = useChat()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [subtasks, setSubtasks] = useState<Task[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [nodes, setNodes] = useState<Node[]>([])
  const [personas, setPersonas] = useState<Persona[]>([])
  const [error, setError] = useState('')
  const [missing, setMissing] = useState(false)
  const [busy, setBusy] = useState(false)

  // The form.
  const [title, setTitle] = useState('')
  const [detail, setDetail] = useState('')
  const [projectId, setProjectId] = useState('')
  const [priority, setPriority] = useState(0)
  const [agent, setAgent] = useState('')
  const [node, setNode] = useState('')
  const [mode, setMode] = useState('')
  const [persona, setPersona] = useState('')
  const [kind, setKind] = useState<Task['kind'] | ''>('')
  const [dirty, setDirtyState] = useState(false)
  const dirtyRef = useRef(false)
  const projectIdRef = useRef('')
  projectIdRef.current = projectId
  function setDirty(v: boolean) {
    dirtyRef.current = v
    setDirtyState(v)
  }

  const isNew = id === 'new'
  const agents = agentChoices(nodes)

  async function load() {
    try {
      const [p, n, ps] = await Promise.all([api.projects(), api.nodes(), api.personas()])
      setProjects(p.projects)
      setNodes(n.nodes)
      setPersonas(ps.personas)
      if (isNew && !dirtyRef.current && presetProject && !projectIdRef.current && presetProject !== 'misc') setProjectId(presetProject)
      if (!isNew) {
        const d = await api.task(id)
        setTask(d.task)
        setRuns(d.runs)
        setSubtasks(d.subtasks)
        if (!dirtyRef.current) fill(d.task)
      }
      setError('')
    } catch (e) {
      if ((e as { status?: number }).status === 404) setMissing(true)
      else setError((e as Error).message)
    }
  }

  function fill(t: Task) {
    setTitle(t.title)
    setDetail(t.detail)
    setProjectId(t.project_id === 'misc' ? '' : t.project_id)
    setPriority(t.priority)
    setAgent(t.agent_pref)
    setNode(t.node_pref)
    setMode(t.permission_mode)
    setPersona(t.persona_id)
    setKind(t.kind)
  }

  useEffect(() => {
    void load()
  }, [chat.tasksRev, id])

  async function act(fn: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await fn()
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  // The "misc" project stands for "no repository".
  const pid = () => projectId || (projects.some((p) => p.id === 'misc') ? 'misc' : '')

  async function save(dispatch = false) {
    if (!title.trim()) {
      setError('标题不能为空')
      return
    }
    await act(async () => {
      if (isNew) {
        const t = await api.createTask({
          title, detail, project_id: pid(), priority, agent_pref: agent, node_pref: node,
          permission_mode: mode as Task['permission_mode'], persona_id: persona, dispatch,
          kind: kind || (projectId && projectId !== 'misc' ? 'code' : 'general'),
        })
        setDirty(false)
        onOpenTask(t.id)
        return
      }
      await api.patchTask(id, {
        title, detail, project_id: pid(), priority, agent_pref: agent, node_pref: node,
        permission_mode: mode as Task['permission_mode'], persona_id: persona,
      })
      setDirty(false)
      if (dispatch) await api.dispatchTask(id)
    })
  }

  async function cancel() {
    if (!confirm('取消这个任务？正在执行的 Run 会被停止，未完成的子任务也会一起取消。')) return
    await act(() => api.cancelTask(id))
  }

  const nodeName = (nid: string) => nodes.find((n) => n.id === nid)?.name ?? '未知机器'
  const purposeText: Record<string, string> = { work: '开发', rebase: 'rebase', deliver: '交付' }
  const runStatus: Record<string, string> = {
    queued: '排队中', running: '进行中', succeeded: '成功', failed: '失败', cancelled: '已取消', interrupted: '已中断',
  }
  const canDispatch = task && !['running', 'waiting_approval', 'review'].includes(task.status) && !(task.status === 'done' && task.pr_url)
  const when = (ts: number) => new Date(ts).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  const badgeCls = task
    ? task.status === 'blocked' || task.status === 'failed'
      ? 'text-danger'
      : task.status === 'done'
        ? 'text-done'
        : 'text-warm'
    : 'text-warm'

  return (
    <Sheet title={isNew ? '新任务' : '任务'} onClose={onClose} wide>
      {missing ? (
        <p className="text-muted text-[13px] m-0">这个任务不存在，可能链接已经过期。</p>
      ) : (
        <>
          {task && (
            <>
              <div className="flex gap-2.5 items-baseline flex-wrap">
                <span className={`text-[13px] font-semibold ${badgeCls}`}>{statusText[task.status]}</span>
                {task.status_reason && <span className="text-muted text-[13px] [overflow-wrap:anywhere]">{task.status_reason}</span>}
              </div>
              {task.pr_url && (
                <p className="m-0 text-[14px] [overflow-wrap:anywhere]">PR：<a className="text-accent" href={task.pr_url} target="_blank" rel="noreferrer">{task.pr_url}</a>（sous 不会合并，请你评审后合并）</p>
              )}
              {task.review_note && !task.status_reason.includes(task.review_note) && (
                <p className="m-0 text-[14px] [overflow-wrap:anywhere]"><strong>验收：</strong>{task.review_note}</p>
              )}
            </>
          )}

          <form className="grid gap-2.5" onInput={() => setDirty(true)} onSubmit={(e) => { e.preventDefault(); void save() }}>
            <label className="grid gap-1 text-[13px] text-muted">
              标题
              <input className={inputCls} value={title} onChange={(e) => setTitle(e.currentTarget.value)} required />
            </label>
            <label className="grid gap-1 text-[13px] text-muted">
              说明（交给 agent 的完整任务说明）
              <textarea className={`${inputCls} resize-y leading-[1.5]`} value={detail} onChange={(e) => setDetail(e.currentTarget.value)} rows={5}></textarea>
            </label>
            <div className="grid gap-1.5 text-[13px] text-muted" role="radiogroup" aria-label="岗位">
              岗位
              <div className="flex gap-1.5 flex-wrap">
                {[{ id: '', name: '默认', glyph: '', color: 'gray' as const, agent: '' }, ...personas].map((p) => (
                  <button
                    key={p.id || 'default'}
                    type="button"
                    role="radio"
                    aria-checked={persona === p.id}
                    title={p.id ? p.name : '按项目默认'}
                    className={`flex items-center gap-1.5 h-9 pl-1 pr-2.5 rounded-full border-0 text-[12.5px] font-medium transition-shadow ${
                      persona === p.id ? 'bg-panel text-ink shadow-[0_0_0_2px_var(--accent)]' : 'bg-sunken text-muted hover:text-ink'
                    }`}
                    onClick={() => { setPersona(p.id); setDirty(true) }}
                  >
                    {p.id ? <Avatar glyph={p.glyph || p.name.slice(0, 1)} color={p.color} size={26} /> : <span className="grid place-items-center w-[26px] h-[26px] rounded-full bg-hover"><Icon name="sparkles" size={14} /></span>}
                    {p.name}
                  </button>
                ))}
              </div>
            </div>
            <div className="grid gap-1.5 text-[13px] text-muted" role="radiogroup" aria-label="agent">
              agent
              <div className="flex gap-1.5 flex-wrap">
                {['', ...agents].map((x) => (
                  <button
                    key={x || 'default'}
                    type="button"
                    role="radio"
                    aria-checked={agent === x}
                    title={x ? agentTitle(x) : '按项目或全局默认'}
                    className={`flex items-center gap-1.5 h-9 pl-1.5 pr-2.5 rounded-[12px] border-0 text-[12.5px] font-medium transition-shadow ${
                      agent === x ? 'bg-panel text-ink shadow-[0_0_0_2px_var(--accent)]' : 'bg-sunken text-muted hover:text-ink'
                    }`}
                    onClick={() => { setAgent(x); setDirty(true) }}
                  >
                    {x ? <AgentIcon name={x} size={24} /> : <span className="grid place-items-center w-6 h-6 rounded-[9px] bg-hover"><Icon name="sparkles" size={14} /></span>}
                    {x ? agentTitle(x) : '默认'}
                  </button>
                ))}
              </div>
            </div>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-2.5">
              <label className="grid gap-1 text-[13px] text-muted">
                项目
                <select
                  className={inputCls}
                  value={projectId}
                  onChange={(e) => setProjectId(e.currentTarget.value)}
                  disabled={!isNew && !!task && task.status !== 'inbox' && task.status !== 'planned'}
                >
                  <option value="">杂项（一般任务）</option>
                  {projects.filter((p) => p.id !== 'misc').map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                </select>
              </label>

              {isNew && (
                <label className="grid gap-1 text-[13px] text-muted">
                  类型
                  <select className={inputCls} value={kind} onChange={(e) => setKind(e.currentTarget.value as Task['kind'] | '')}>
                    <option value="">自动</option>
                    <option value="code">代码任务</option>
                    <option value="general">一般任务</option>
                    <option value="ops">运维任务（服务器）</option>
                  </select>
                </label>
              )}
              <label className="grid gap-1 text-[13px] text-muted">
                优先级
                <input
                  className={inputCls}
                  type="number"
                  value={priority}
                  onChange={(e) => setPriority(e.currentTarget.value === '' ? 0 : Number(e.currentTarget.value))}
                  min={-10}
                  max={10}
                />
              </label>

              <label className="grid gap-1 text-[13px] text-muted">
                机器
                <select className={inputCls} value={node} onChange={(e) => setNode(e.currentTarget.value)}>
                  <option value="">按项目的优先级</option>
                  {nodes.map((n) => <option key={n.id} value={n.id}>{n.name}{n.online ? '' : '（离线）'}</option>)}
                </select>
              </label>
              <label className="grid gap-1 text-[13px] text-muted">
                权限模式
                <select className={inputCls} value={mode} onChange={(e) => setMode(e.currentTarget.value)}>
                  <option value="">按项目或全局默认</option>
                  {Object.entries(modeText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </select>
              </label>
            </div>
            <div className="flex gap-3 items-center flex-wrap">
              {isNew ? (
                <>
                  <button className="btn small" type="button" disabled={busy} onClick={() => void save(true)}><Icon name="send" size={14} stroke={2.2} />创建并派发</button>
                  <button className="btn small ghost" type="submit" disabled={busy}><Icon name="plus" size={14} />只创建</button>
                </>
              ) : (
                <>
                  {dirty && <button className="btn small ghost" type="submit" disabled={busy}><Icon name="check" size={14} stroke={2.2} />保存</button>}
                  {canDispatch && (
                    <button className="btn small" type="button" disabled={busy} onClick={() => void save(true)}>
                      <Icon name={task?.status === 'failed' || task?.status === 'blocked' ? 'refresh' : 'send'} size={14} stroke={2.2} />
                      {task?.status === 'failed' || task?.status === 'blocked' ? '重试' : task?.status === 'queued' ? '重新派发' : '派发'}
                    </button>
                  )}
                  {task && isOpen(task) && <button className="link danger" type="button" disabled={busy} onClick={() => void cancel()}>取消任务</button>}
                  {task && !isOpen(task) && !task.dismissed && (
                    <button className="link" type="button" disabled={busy} onClick={() => void act(() => api.patchTask(id, { dismissed: true }))}>标记已处理</button>
                  )}
                </>
              )}
            </div>
          </form>
          {error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere]" role="alert">{error}</p>}

          {task && (
            <>
              {subtasks.length > 0 && (
                <section>
                  <h3 className="mt-1 mb-1.5 text-[13px] text-muted">子任务</h3>
                  <ul className="list-none m-0 p-0 grid gap-1">
                    {subtasks.map((s) => (
                      <li key={s.id}>
                        <button className="flex flex-wrap gap-x-2.5 gap-y-1 w-full text-left border border-line bg-panel rounded-[12px] px-2.5 py-[7px] text-[13px] hover:border-accent" onClick={() => onOpenTask(s.id)}>
                          <span className="font-semibold text-warm">{statusText[s.status]}</span>{s.title}
                        </button>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              {task.depends_on.length > 0 && (
                <section>
                  <h3 className="mt-1 mb-1.5 text-[13px] text-muted">依赖</h3>
                  <ul className="list-none m-0 p-0 grid gap-1">
                    {task.depends_on.map((d) => (
                      <li key={d}>
                        <button className="flex flex-wrap gap-x-2.5 gap-y-1 w-full text-left border border-line bg-panel rounded-[12px] px-2.5 py-[7px] text-[13px] hover:border-accent" onClick={() => onOpenTask(d)}>{d}</button>
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              {!compact && (
                <section>
                  <h3 className="mt-1 mb-1.5 text-[13px] text-muted">执行记录</h3>
                  {runs.length === 0 && <p className="text-muted text-[13px] m-0">还没有执行过。</p>}
                  <ul className="list-none m-0 p-0 grid gap-1">
                    {runs.map((r) => (
                      <li key={r.id}>
                        <button
                          className="flex flex-wrap gap-x-2.5 gap-y-1 w-full text-left border border-line bg-panel rounded-[12px] px-2.5 py-[7px] text-[13px] hover:border-accent"
                          onClick={() => { onClose(); onOpenThread(r.thread_id) }}
                          title="在对话里查看"
                        >
                          <span className="font-semibold text-warm">{purposeText[r.purpose] ?? r.purpose}</span>
                          <span>{r.purpose === 'deliver' ? 'sous' : r.agent} @ {nodeName(r.node_id)} · {runStatus[r.status] ?? r.status}</span>
                          <span className="text-muted text-[13px] m-0">{when(r.created_at)} · {modeText[r.permission_mode] ?? r.permission_mode}</span>
                          {r.error && <span className="text-danger text-[13px] m-0 [overflow-wrap:anywhere]">{r.error}</span>}
                          {r.diffstat && <span className="text-muted text-[13px] m-0">{r.diffstat}</span>}
                        </button>
                      </li>
                    ))}
                  </ul>
                  {task.branch && <p className="text-muted text-[13px] m-0">分支：{task.branch}{task.base_branch ? ` → ${task.base_branch}` : ''}</p>}
                </section>
              )}
            </>
          )}
        </>
      )}
    </Sheet>
  )
}
