import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { go, ui, useChat, useWb } from '../lib/app'
import { isActive, runFromHistory, type RunView } from '../lib/runs'
import { avatarOf } from '../lib/status'
import { personaFor, runMeta } from '../lib/workbench'
import type { Approval, HubEvent, Run } from '../lib/types'
import ApprovalCard from './ApprovalCard'
import Avatar from './Avatar'
import Icon from './Icon'
import Sheet from './Sheet'
import TextViewer from './TextViewer'

const statusText: Record<string, string> = {
  queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断',
}
const toolIcon: Record<string, string> = { completed: '✓', failed: '✕', in_progress: '…', pending: '·' }

export default function RunSheet({ id, onClose }: { id: string; onClose: () => void }) {
  const chatState = useChat()
  const wb = useWb()
  const [detail, setDetail] = useState<{ run: Run; events: HubEvent[]; approvals: Approval[] } | null>(null)
  const [view, setView] = useState<RunView | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [viewer, setViewer] = useState<{ title: string; text: string; diff: boolean } | null>(null)

  async function load() {
    try {
      const d = await api.run(id)
      setDetail(d)
      setView(runFromHistory(d.run, d.events, d.approvals))
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    void load()
  }, [id, chatState.runsRev, chatState.inboxRev])

  const data = wb.data
  const run = detail?.run ?? data?.runs.find((r) => r.id === id)
  const task = run?.task_id ? data?.tasks.find((t) => t.id === run.task_id) : undefined
  const persona = data && task ? personaFor(data, task, run) : undefined
  const node = data?.nodes.find((n) => n.id === run?.node_id)
  const meta = run ? runMeta(run, node) : null
  const metaText = [persona?.name ?? '未分配', meta?.agentTitle, meta?.model, meta?.effort ? `思考 ${meta.effort}` : ''].filter(Boolean).join(' · ')
  const a = avatarOf(persona, run?.agent)
  const acts = wb.live[id] ?? []
  const approvals = view?.approvals ?? []
  const pending = approvals.filter((x) => x.status === 'pending')
  const liveIDs = new Set(acts.filter((x) => x.kind === 'tool').map((x) => x.id))
  const steps = (view?.tools ?? []).filter((t) => !liveIDs.has(t.id))
  const plan = view?.plan ?? []
  const runState = run ? (run.paused ? '已暂停' : statusText[run.status] ?? run.status) : ''
  const tone = run
    ? run.paused
      ? 'warning'
      : run.status === 'running'
        ? 'accent'
        : run.status === 'succeeded'
          ? 'success'
          : run.status === 'failed' || run.status === 'interrupted'
            ? 'danger'
            : 'neutral'
    : 'neutral'

  async function act(key: string, fn: () => Promise<unknown>) {
    setBusy(key)
    setError('')
    try {
      await fn()
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  async function open(what: 'log' | 'diff') {
    if (!run) return
    await act(what, async () => {
      const text = await api.runText(run.id, what)
      setViewer({ title: what === 'diff' ? `改动 · ${run.branch}` : '运行日志', text, diff: what === 'diff' })
    })
  }

  return (
    <>
      <Sheet title={task?.title || (run?.persona ? `${run.persona} 的 Run` : '运行详情')} wide onClose={onClose}>
        {!run ? (
          <p className="text-muted text-[13px] m-0">{error || '这个 Run 不在了。'}</p>
        ) : (
          <>
            <header className="flex items-center gap-2.5 min-w-0">
              <Avatar glyph={a.glyph} color={a.color} title={a.title} size={30} ring={tone === 'neutral' ? '' : tone} />
              <div className="min-w-0 flex-1">
                <p className="m-0 font-semibold text-[14px] overflow-hidden text-ellipsis whitespace-nowrap">{metaText}</p>
                <p className="m-0 text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap">
                  {node && <>{node.name}</>}{run.branch && <> · {run.branch}</>}{run.pr_url && <> · <a className="text-accent" href={run.pr_url} target="_blank" rel="noreferrer">PR</a></>}
                </p>
              </div>
              <span className={`text-[13px] font-semibold whitespace-nowrap tone-${tone}`}>{runState}</span>
            </header>

            {error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere]" role="alert">{error}</p>}
            {run.summary && <p className="m-0 text-[14px] whitespace-pre-wrap [overflow-wrap:anywhere]">{run.summary}</p>}
            {run.error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere]">{run.error}</p>}
            {run.diffstat && <p className="m-0 text-muted text-[13px]">{run.diffstat}</p>}

            <div className="flex gap-3 items-center flex-wrap">
              {isActive(run) && run.paused ? (
                <button className="btn small" disabled={!!busy} onClick={() => void act('resume', () => api.resumeRun(run.id))}>继续</button>
              ) : run.status === 'running' && run.purpose !== 'deliver' ? (
                <button className="btn small ghost" disabled={!!busy} onClick={() => void act('pause', () => api.pauseRun(run.id))}>暂停</button>
              ) : null}
              {isActive(run) && <button className="link" disabled={!!busy} onClick={() => void act('cancel', () => api.cancelRun(run.id))}>取消</button>}
              {run.diffstat && run.purpose !== 'deliver' && <button className="link" disabled={!!busy} onClick={() => void open('diff')}>改动</button>}
              {run.status !== 'queued' && <button className="link" disabled={!!busy} onClick={() => void open('log')}>日志</button>}
              {task && (
                <button className="link accent" onClick={() => { ui.state.runSheet = ''; ui.emit(); go({ view: 'task', id: task.id }) }}>打开任务</button>
              )}
            </div>

            {pending.map((x) => <ApprovalCard key={x.id} approval={x} context={task?.title} />)}

            <section className="grid gap-1.5">
              <h3 className="mt-1 mb-0 text-[13px] text-muted">活动</h3>
              {acts.length > 0 ? (
                <ol className="list-none m-0 p-0 grid gap-[3px] text-[13px]">
                  {acts.map((x) => (
                    <li key={x.id} className={`flex gap-1.5 items-baseline [overflow-wrap:anywhere] ${x.kind === 'thought' ? 'text-muted' : x.kind === 'permission' || x.kind === 'status' ? 'text-warm' : ''}`}>
                      {x.kind === 'tool' ? <Icon name="link" size={11} /> : x.kind === 'thought' ? <Icon name="bulb" size={11} /> : null}
                      <span className="flex-1 min-w-0">{x.text}</span>
                      {x.status && <span className="text-muted text-[12px]">{toolIcon[x.status] ?? x.status}</span>}
                    </li>
                  ))}
                </ol>
              ) : steps.length || plan.length ? (
                <p className="m-0 text-muted text-[13px]">没有实时输出，下面是已经记录下来的步骤。</p>
              ) : (
                <p className="m-0 text-muted text-[13px]">还没有输出。</p>
              )}
            </section>

            {plan.length > 0 && (
              <section className="grid gap-1.5">
                <h3 className="mt-1 mb-0 text-[13px] text-muted">计划</h3>
                <ul className="list-none m-0 p-0 grid gap-[3px] text-[13px]">
                  {plan.map((x, i) => (
                    <li key={i}>
                      <span className={x.status === 'completed' ? 'text-ok' : x.status === 'in_progress' ? 'text-warm' : 'text-muted'}>
                        {x.status === 'completed' ? '● ' : x.status === 'in_progress' ? '◐ ' : '○ '}
                      </span>
                      {x.content}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {steps.length > 0 && (
              <section className="grid gap-1.5">
                <h3 className="mt-1 mb-0 text-[13px] text-muted">步骤</h3>
                <ul className="list-none m-0 p-0 grid gap-[3px] text-[13px]">
                  {steps.map((t) => (
                    <li key={t.id} className={t.status === 'completed' ? 'text-ink' : t.status === 'failed' ? 'text-danger' : 'text-muted'}>
                      <span className="inline-block w-[1.2em]">{toolIcon[t.status] ?? '·'}</span>{t.title}
                    </li>
                  ))}
                </ul>
              </section>
            )}
          </>
        )}
      </Sheet>

      {viewer && <TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => setViewer(null)} />}
    </>
  )
}
