import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { chat, go, openThread, ui, useChat, useWb } from '../lib/app'
import { runFromHistory, type RunView } from '../lib/runs'
import { avatarOf, gitStateText, kindText, looks, placeText, reviewText, stages, taskLook, userReviewing } from '../lib/status'
import { deployedAfter, personaFor } from '../lib/workbench'
import type { Run, Task } from '../lib/types'
import Icon from './Icon'
import RunCard from './RunCard'
import StageBar from './StageBar'
import TaskDetail from './TaskDetail'

export default function TaskView({ id, embedded = false }: { id: string; embedded?: boolean }) {
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

  const info = looks[look]
  const kindIcon: Record<string, string> = { code: 'terminal', general: 'sparkles', ops: 'server' }

  return (
    <div className={`grid gap-4 content-start ${embedded ? 'p-0 max-w-none' : 'px-[22px] pt-4 pb-7 max-w-[900px] max-[760px]:px-3.5 max-[760px]:pt-3 max-[760px]:pb-6'}`}>
      {missing ? (
        <div className="grid justify-items-center gap-2 py-10 text-muted">
          <Icon name="circle-dashed" size={32} />
          <p className="m-0 text-[13px]">这个任务不存在，可能链接已经过期。</p>
        </div>
      ) : task ? (
        <>
          <header className="flex items-start gap-3.5">
            {!embedded && (
              <button className="icon-btn" aria-label="返回项目" onClick={() => go(project ? { view: 'project', id: project.id } : { view: 'overview', id: '' })}><Icon name="back" /></button>
            )}
            <div className="min-w-0 grid gap-1.5">
              <h1 className="m-0 text-[20px] font-bold tracking-tight leading-[1.3] [overflow-wrap:anywhere]">{task.title}</h1>
              <p className="m-0 flex flex-wrap gap-1.5 items-center">
                <span className={`pill tone-${info.tone}`}><Icon name={info.icon} size={12} stroke={2.2} />{info.text}</span>
                <span className="pill" title={a.title}><Icon name="user" size={12} />{a.title}</span>
                <span className="pill" title={`${kindText[task.kind]}任务`}><Icon name={kindIcon[task.kind] ?? 'dot'} size={12} />{kindText[task.kind]}</span>
                {task.status === 'done' && task.git_state && (
                  <span className="pill" title="改动的去向"><Icon name="branch" size={12} />{gitStateText[task.git_state]}</span>
                )}
              </p>
            </div>
          </header>

          <div className="card px-3 pt-4 pb-3">
            <StageBar stages={stages(task, { hasDeploy: !!project?.deploy, deployed: task.status === 'done' && !!data && deployedAfter(data.tasks, task.project_id, task.updated_at) })} labels />
          </div>

          <dl className="card m-0 px-4 py-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13px]" aria-label="怎么做">
            <dt className="text-muted">在哪儿做</dt>
            <dd className="m-0">{task.place ? placeText[task.place] : <span className="text-faint">还没定（派发前要定下）</span>}</dd>
            {task.place === 'worktree' && (
              <>
                <dt className="text-muted">从哪儿开始</dt>
                <dd className="m-0 [overflow-wrap:anywhere]">
                  {task.base_branch ? `${task.base_from === 'remote' ? 'origin/' : '本地 '}${task.base_branch}` : <span className="text-faint">还没定</span>}
                  {task.base_commit && <span className="text-faint">（{task.base_commit.slice(0, 8)}）</span>}
                </dd>
              </>
            )}
            {task.branch && (
              <>
                <dt className="text-muted">分支</dt>
                <dd className="m-0 [overflow-wrap:anywhere]"><code>{task.branch}</code>{task.git_state && <span className="text-muted"> · {gitStateText[task.git_state]}</span>}</dd>
              </>
            )}
            <dt className="text-muted">验收</dt>
            <dd className="m-0">{task.review ? reviewText[task.review] : <span className="text-faint">还没定</span>}{task.review && task.review !== 'none' && task.on_fail ? <span className="text-muted">（不通过时{task.on_fail === 'rework' ? '返工' : '停下来商量'}）</span> : null}</dd>
          </dl>

          {userReviewing(task) && (
            <div className="card px-4 py-3 grid gap-2" aria-label="验收">
              <p className="m-0 font-semibold text-[14px] flex items-center gap-1.5"><Icon name="eye" size={16} />等你验收</p>
              <textarea
                className="min-h-[64px]"
                value={verdictNote}
                onChange={(e) => setVerdictNote(e.currentTarget.value)}
                placeholder="不通过时写下要改什么，agent 会照着返工"
                aria-label="验收意见"
              />
              <div className="flex gap-2 justify-end">
                <button className="btn small ghost" disabled={deciding || !verdictNote.trim()} onClick={() => void decide(false)}>要改</button>
                <button className="btn small" disabled={deciding} onClick={() => void decide(true)}>通过</button>
              </div>
            </div>
          )}

          {task.status_reason && (
            <p className="m-0 flex gap-2 items-start text-muted text-[13px] [overflow-wrap:anywhere] bg-sunken rounded-[14px] px-3 py-2">
              <Icon name="bulb" size={15} className="mt-[2px]" />{task.status_reason}
            </p>
          )}
          {task.pr_url && (
            <a
              className="flex gap-3 items-center rounded-[16px] px-3 py-2.5 no-underline text-ink bg-[color-mix(in_srgb,var(--ok)_9%,var(--panel))] shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--ok)_30%,transparent)] hover:bg-[color-mix(in_srgb,var(--ok)_14%,var(--panel))]"
              href={task.pr_url}
              target="_blank"
              rel="noreferrer"
              title="打开 Pull Request"
            >
              <span className="grid place-items-center w-9 h-9 rounded-[12px] bg-ok text-white flex-none"><Icon name="pr" size={18} stroke={2} /></span>
              <span className="grid min-w-0 flex-1 leading-tight">
                <span className="font-semibold text-[14px]">Pull Request</span>
                <span className="text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap">{task.pr_url.replace(/^https?:\/\//, '')}</span>
              </span>
              <Icon name="external" size={16} className="text-muted" />
            </a>
          )}

          {active && (
            <form className="flex items-center gap-1.5 bg-panel shadow-card rounded-[18px] py-1 pr-1 pl-3" onSubmit={(e) => { e.preventDefault(); void sendNote() }}>
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

          <section className="grid gap-3" aria-label="执行记录">
            {views.length === 0 ? (
              <div className="flex items-center gap-2 text-muted text-[13px] rounded-[16px] border border-dashed border-line-strong px-3 py-3">
                <Icon name="clock" size={16} />还没有执行过
              </div>
            ) : (
              views.map((v) => <RunCard key={v.run.id} view={v} nodeName={nodeName} full />)
            )}
          </section>

          <section className="grid gap-2">
            <button className="toggle justify-self-start inline-flex gap-1.5 items-center border-0 bg-sunken rounded-full text-muted text-[12.5px] font-semibold px-3 py-1.5 hover:text-ink" onClick={() => setShowForm(!showForm)} aria-expanded={showForm}>
              <Icon name="settings" size={14} />说明与设置<Icon name="chevron" size={13} />
            </button>
            {showForm && (
              <div className="card px-4 py-3.5 embed">
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
              </div>
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
  )
}
