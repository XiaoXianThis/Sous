import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { chat, go, openThread, ui, useChat, useWb } from '../lib/app'
import { runFromHistory, type RunView } from '../lib/runs'
import { avatarOf, kindText, looks, stages, taskLook } from '../lib/status'
import { deployedAfter, personaFor } from '../lib/workbench'
import type { Run, Task } from '../lib/types'
import Avatar from './Avatar'
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

  return (
    <div className={`grid gap-3 content-start ${embedded ? 'p-0 max-w-none' : 'px-[22px] pt-4 pb-7 max-w-[900px] max-[760px]:px-3.5 max-[760px]:pt-3 max-[760px]:pb-6'}`}>
      {missing ? (
        <p className="text-muted text-[13px] m-0">这个任务不存在，可能链接已经过期。</p>
      ) : task ? (
        <>
          <header className="flex items-center gap-2.5">
            {!embedded && (
              <button className="icon-btn" aria-label="返回项目" onClick={() => go(project ? { view: 'project', id: project.id } : { view: 'overview', id: '' })}><Icon name="back" /></button>
            )}
            <Avatar glyph={a.glyph} color={a.color} title={a.title} size={34} ring={looks[look].tone === 'neutral' ? '' : looks[look].tone} />
            <div className="min-w-0">
              <h1 className="m-0 text-[19px] leading-[1.3]">{task.title}</h1>
              <p className="mt-0.5 mb-0 flex flex-wrap gap-x-3 gap-y-1 text-[13px] text-muted items-center">
                <span className={`inline-flex items-center gap-1 font-semibold tone-${looks[look].tone}`}><Icon name={looks[look].icon} size={13} />{looks[look].text}</span>
                <span>{a.title}</span>
                {project && <button className="link accent" onClick={() => go({ view: 'project', id: project.id })}>{project.name}</button>}
                <span className="border border-line rounded-[6px] px-[5px] text-[11px]">{kindText[task.kind]}</span>
              </p>
            </div>
          </header>

          <StageBar stages={stages(task, { hasDeploy: !!project?.deploy, deployed: task.status === 'done' && !!data && deployedAfter(data.tasks, task.project_id, task.updated_at) })} labels />
          {task.status_reason && <p className="m-0 text-muted text-[13px] [overflow-wrap:anywhere]">{task.status_reason}</p>}
          {task.pr_url && (
            <p className="m-0 flex gap-1.5 items-center flex-wrap text-[13px] [overflow-wrap:anywhere]">
              <Icon name="branch" size={14} /><a className="text-accent" href={task.pr_url} target="_blank" rel="noreferrer">{task.pr_url}</a><span className="text-muted text-[13px] m-0">sous 不会合并，请你评审后合并</span>
            </p>
          )}

          {active && (
            <form className="flex gap-1.5 border border-line bg-panel rounded-full py-[3px] pr-1 pl-3.5" onSubmit={(e) => { e.preventDefault(); void sendNote() }}>
              <input
                className="flex-1 border-0 outline-0 bg-transparent"
                value={note}
                onChange={(e) => setNote(e.currentTarget.value)}
                placeholder={`给正在干活的 ${a.title} 补充一句…`}
                aria-label="追加指令"
              />
              <button className="icon-btn" type="submit" aria-label="发送" disabled={!note.trim()}><Icon name="send" /></button>
            </form>
          )}

          {error && <p className="text-danger text-[13px] m-0">{error}</p>}

          <section className="grid gap-2.5" aria-label="执行记录">
            {views.length === 0 ? (
              <p className="text-muted text-[13px] m-0">还没有执行过。</p>
            ) : (
              views.map((v) => <RunCard key={v.run.id} view={v} nodeName={nodeName} full />)
            )}
          </section>

          <section className="grid gap-2">
            <button className="toggle justify-self-start inline-flex gap-1 items-center border-0 bg-none text-muted text-[13px] p-0" onClick={() => setShowForm(!showForm)} aria-expanded={showForm}>
              <Icon name="chevron" size={14} />任务说明和设置
            </button>
            {showForm && (
              <div className="border border-line rounded-[14px] bg-panel px-3.5 py-3 embed">
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
      ) : null}
    </div>
  )
}
