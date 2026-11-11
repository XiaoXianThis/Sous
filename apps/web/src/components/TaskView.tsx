import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { chat, go, openThread, ui, useChat, useWb } from '../lib/app'
import { runFromHistory, type RunView } from '../lib/runs'
import { avatarOf, gitStateText, kindText, looks, placeText, reviewText, stages, taskLook, userReviewing } from '../lib/status'
import { personaFor } from '../lib/workbench'
import type { Run, Task } from '../lib/types'
import { agentTitle } from './AgentIcon'
import Icon from './Icon'
import RunCard from './RunCard'
import StageBar from './StageBar'
import TalkLog from './TalkLog'
import TaskDetail from './TaskDetail'
import { SheetPageContext } from './Sheet'

/**
 * A task's detail inside the project dialog: a fixed header (title, actions),
 * its runs newest first, and a fixed bottom: one line of task info, plus the
 * verdict and the note to the running agent when either is needed.
 */
export default function TaskView({ id }: { id: string }) {
  const chatState = useChat()
  const wb = useWb()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [note, setNote] = useState('')
  const [verdictNote, setVerdictNote] = useState('')
  const [deciding, setDeciding] = useState(false)

  async function load() {
    try {
      const d = await api.task(id)
      setTask(d.task)
      setRuns(d.runs)
      setMissing(false)
      const need = d.runs.filter((r) => !chat.state.runs[r.id])
      const details = await Promise.all(need.map((r) => api.run(r.id)))
      for (const x of details) {
        if (!chat.state.runs[x.run.id]) chat.state.runs[x.run.id] = runFromHistory(x.run, x.events, x.approvals)
      }
      chat.emit()
      setError('')
    } catch (e) {
      if ((e as { status?: number }).status === 404) setMissing(true)
      else setError((e as Error).message)
    }
  }
  useEffect(() => {
    void load()
  }, [chatState.tasksRev, id])

  const data = wb.data
  const project = data?.projects.find((p) => p.id === task?.project_id)
  const persona = data && task ? personaFor(data, task) : undefined
  const look = task ? taskLook(task) : 'planned'
  const views: RunView[] = runs.map((r) => chatState.runs[r.id]).filter((v): v is RunView => !!v).reverse()
  const active = views.find((v) => v.run.status === 'running')
  const nodeName = (nid: string) => data?.nodes.find((n) => n.id === nid)?.name ?? '未知机器'
  const a = avatarOf(persona, task?.agent_pref)

  async function sendNote() {
    if (!note.trim() || !active) return
    try {
      await api.messageRun(active.run.id, note.trim())
      setNote('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function decide(pass: boolean) {
    if (!task) return
    setDeciding(true)
    try {
      await api.verdict(task.id, pass, verdictNote.trim())
      setVerdictNote('')
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDeciding(false)
    }
  }

  async function act(fn: () => Promise<unknown>) {
    setError('')
    try {
      await fn()
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const info = looks[look]
  const isOpenTask = !!task && task.status !== 'done' && task.status !== 'cancelled'
  const stuck = !!task && (task.status === 'failed' || task.status === 'blocked')
  const canDispatch = !!task && !['running', 'waiting_approval', 'review'].includes(task.status) && !['merged', 'discarded', 'pr'].includes(task.git_state)
  const dispatchText = !task ? '' : stuck ? '重试' : task.status === 'queued' ? '重新派发' : task.status === 'done' || task.status === 'cancelled' ? '再做一轮' : '派发'
  const onlineNodes = (data?.nodes ?? []).filter((n) => n.online)
  const whoAgent = task?.last_agent || task?.agent_pref || ''
  const whoNode = task?.last_node || task?.node_pref || ''
  const latestDiff = [...runs].reverse().find((r) => r.diffstat)
  const kindIcon: Record<string, string> = { code: 'terminal', general: 'sparkles', ops: 'server' }

  const placeLine = task ? [
    task.place ? placeText[task.place] : '',
    task.place === 'worktree' && task.base_branch ? `从 ${task.base_from === 'remote' ? 'origin/' : '本地 '}${task.base_branch}${task.base_commit ? `（${task.base_commit.slice(0, 8)}）` : ''}` : '',
  ].filter(Boolean).join(' · ') : ''

  return (
    <div className="grid grid-rows-[auto_minmax(0,1fr)_auto] h-full min-h-0">
      {/* The title, actions and the one-line info stay put while the runs scroll. */}
      {task && !missing ? (
          <header className="min-w-0 px-5 pt-5 pb-1 max-[760px]:px-4">
            <div className="flex items-start gap-3 min-w-0">
              <h1 className="m-0 flex-1 min-w-0 text-[20px] font-bold tracking-tight leading-[1.3] [overflow-wrap:anywhere]">{task.title}</h1>
              <div className="flex items-center gap-0.5 flex-none -mr-1.5" role="toolbar" aria-label="任务操作">
                <button className="icon-btn" aria-label="在现场里追踪" data-tip="在现场里追踪" onClick={() => go({view:'system',id:'task:'+task.id})}><Icon name="system" size={16} /></button>
                {canDispatch && (
                  <button className="btn small mr-1" onClick={() => void act(() => api.dispatchTask(task.id))}>
                    <Icon name={stuck ? 'refresh' : 'send'} size={14} stroke={2.2} />{dispatchText}
                  </button>
                )}
                {canDispatch && onlineNodes.length > 1 && (
                  <label className="icon-btn relative cursor-pointer" data-tip="换一台机器重新派发" data-tip-pos="bottom">
                    <Icon name="server" size={16} />
                    <select
                      className="absolute inset-0 opacity-0 cursor-pointer"
                      aria-label="换一台机器重新派发"
                      value=""
                      onChange={(e) => { const n = e.currentTarget.value; if (n) void act(() => api.dispatchTask(task.id, n)) }}
                    >
                      <option value="">选机器…</option>
                      {onlineNodes.map((n) => <option key={n.id} value={n.id}>{n.name}{n.role === 'server' ? '（服务器）' : ''}</option>)}
                    </select>
                  </label>
                )}
                {latestDiff && (
                  <a className="icon-btn no-underline" href={`/api/v1/runs/${latestDiff.id}/diff`} target="_blank" rel="noreferrer" aria-label="看改动" data-tip={`看改动 · ${latestDiff.diffstat}`} data-tip-pos="bottom">
                    <Icon name="branch" size={16} />
                  </a>
                )}
                {isOpenTask && (
                  <button className="icon-btn hover:!text-danger" aria-label="取消任务" data-tip="取消任务" data-tip-pos="bottom" onClick={() => { if (confirm('取消这个任务？正在执行的 Run 会被停止，未完成的子任务也会一起取消。')) void act(() => api.cancelTask(task.id)) }}>
                    <Icon name="x" size={16} />
                  </button>
                )}
                <button className={`icon-btn ${showForm ? '!text-ink bg-hover' : ''}`} onClick={() => setShowForm(!showForm)} aria-expanded={showForm} aria-label="说明与设置" data-tip="说明与设置" data-tip-pos="bottom">
                  <Icon name="settings" size={16} />
                </button>
              </div>
            </div>
          </header>
      ) : <div></div>}
      <div
        className="overflow-y-auto min-h-0 p-5 max-[760px]:px-4"
      >
        <div className="grid grid-cols-[minmax(0,1fr)] gap-4 content-start">
          {missing ? (
            <div className="grid justify-items-center gap-2 py-10 text-muted">
              <Icon name="circle-dashed" size={32} />
              <p className="m-0 text-[13px]">这个任务不存在，可能链接已经过期。</p>
            </div>
          ) : task ? (
            <>

              {showForm && (
                // The form is a dialog elsewhere; here it is laid flat into the card (.embed).
                <div className="card px-4 py-3.5 embed">
                  <SheetPageContext.Provider value>
                  <TaskDetail
                    id={id}
                    compact
                    onClose={() => setShowForm(false)}
                    onOpenTask={(tid) => go({ view: 'task', id: tid })}
                    onOpenThread={(tid) => {
                      void openThread(tid)
                      ui.state.chatOpen = true
                      ui.emit()
                    }}
                  />
                  </SheetPageContext.Provider>
                </div>
              )}

              {/* What the supervisor and the agents said to each other, folded. */}
              {views.length > 0 && <TalkLog taskId={id} />}

              {/* Every run of the task, newest first. */}
              <section className="grid grid-cols-[minmax(0,1fr)] gap-3" aria-label="执行记录">
                {views.length === 0 ? (
                  <div className="flex items-center gap-2 text-muted text-[13px] py-2">
                    <Icon name="clock" size={16} />还没有执行过
                  </div>
                ) : (
                  views.map((v, i) => <RunCard key={v.run.id} view={v} nodeName={nodeName} latest={i === 0} />)
                )}
              </section>
            </>
          ) : (
            <div className="grid gap-3">
              <div className="h-12 rounded-[16px] bg-sunken animate-pulse"></div>
              <div className="h-20 rounded-[16px] bg-sunken animate-pulse"></div>
            </div>
          )}
        </div>
      </div>

      {task && !missing && (
        <footer className="grid grid-cols-[minmax(0,1fr)] gap-2.5 bg-panel px-5 pt-2.5 pb-3.5 max-[760px]:px-4 max-[760px]:pb-[calc(12px+var(--safe-b))]" aria-label="任务状态">
          {/* Everything about the task on one line: state, stages, who, where, how it is checked, where it went. */}
          <div className="flex items-center gap-x-3.5 text-[12.5px] text-muted whitespace-nowrap overflow-x-auto [scrollbar-width:none] min-w-0" aria-label="任务信息">
            <span className={`inline-flex items-center gap-1 font-semibold tone-${info.tone}`}><span className="sr-only">状态</span><span className="inline-flex items-center gap-1"><Icon name={info.icon} size={13} stroke={2.2} />{info.text}</span></span>
            <StageBar stages={stages(task)} inline />
            <span className="inline-flex items-center gap-1" title={`预设 · ${kindText[task.kind]}任务`}><span className="sr-only">预设</span><span className="inline-flex items-center gap-1"><Icon name={kindIcon[task.kind] ?? 'user'} size={13} />{a.title}</span></span>
            {whoAgent && (
              <span className="inline-flex items-center gap-1" title={task.last_agent ? '最近一次执行的 agent 和机器' : '指定的 agent 和机器'}>
                <span className="sr-only">agent</span><span className="inline-flex items-center gap-1"><Icon name="terminal" size={13} />{agentTitle(whoAgent)}{whoNode ? ` · ${nodeName(whoNode)}` : ''}</span>
              </span>
            )}
            <span className="inline-flex items-center gap-1" title="在哪做">
              <span className="inline-flex"><Icon name="folder" size={13} /><span className="sr-only">在哪做</span></span>
              <span >{placeLine || <span className="text-faint">在哪做还没定</span>}</span>
            </span>
            <span className="inline-flex items-center gap-1" title={task.review && task.review !== 'none' && task.on_fail ? `不通过时${task.on_fail === 'rework' ? '返工' : '停下来商量'}` : '验收'}>
              <span className="inline-flex"><Icon name="eye" size={13} /><span className="sr-only">验收</span></span>
              <span >{task.review ? reviewText[task.review] : <span className="text-faint">验收还没定</span>}</span>
            </span>
            {task.branch && (
              <span className="inline-flex items-center gap-1" title={`分支 ${task.branch}`}>
                <span className="inline-flex"><Icon name="branch" size={13} /><span className="sr-only">分支</span></span>
                <span className="font-mono text-[12px]">{task.branch}{task.git_state ? <span className="font-sans"> · {gitStateText[task.git_state]}</span> : null}</span>
              </span>
            )}
            {!task.branch && task.git_state && (
              <span className="inline-flex items-center gap-1"><span className="inline-flex"><Icon name="check-circle" size={13} /><span className="sr-only">完成</span></span><span >{gitStateText[task.git_state]}</span></span>
            )}
            {task.pr_url && (
              <span className="inline-flex"><span className="sr-only">PR</span><span >
                <a className="inline-flex items-center gap-1 font-semibold text-accent" href={task.pr_url} target="_blank" rel="noreferrer" title={task.pr_url}><Icon name="pr" size={13} />PR<Icon name="external" size={11} /></a>
              </span></span>
            )}
            {task.status_reason && (
              <span className="inline-flex items-center gap-1 min-w-0" title={task.status_reason}>
                <span className="inline-flex"><Icon name="bulb" size={13} /><span className="sr-only">说明</span></span>
                <span className="max-w-[360px] overflow-hidden text-ellipsis">{task.status_reason}</span>
              </span>
            )}
          </div>
          {userReviewing(task) && (
            <div className="grid gap-2" aria-label="验收">
              <textarea
                className="min-h-[56px]"
                value={verdictNote}
                onChange={(e) => setVerdictNote(e.currentTarget.value)}
                placeholder="等你验收：不通过时写下要改什么，agent 会照着返工"
                aria-label="验收意见"
              />
              <div className="flex gap-2 justify-end">
                <button className="btn small ghost" disabled={deciding || !verdictNote.trim()} onClick={() => void decide(false)}>要改</button>
                <button className="btn small" disabled={deciding} onClick={() => void decide(true)}>通过</button>
              </div>
            </div>
          )}
          {active && (
            <form className="flex items-center gap-1.5 rounded-[16px] border border-line py-1 pr-1 pl-3" onSubmit={(e) => { e.preventDefault(); void sendNote() }}>
              <Icon name="message" size={16} className="text-muted" />
              <input
                className="flex-1 border-0 outline-0 bg-transparent py-1.5 !shadow-none"
                value={note}
                onChange={(e) => setNote(e.currentTarget.value)}
                placeholder={`给 ${a.title} 补充一句…`}
                aria-label="追加指令"
              />
              <button className="icon-btn primary w-8 h-8 disabled:bg-sunken disabled:text-faint" type="submit" aria-label="发送" disabled={!note.trim()}><Icon name="send" size={16} stroke={2.2} /></button>
            </form>
          )}
          {error && <p className="text-danger text-[13px] m-0 flex items-center gap-1.5"><Icon name="alert" size={14} />{error}</p>}
        </footer>
      )}
    </div>
  )
}
