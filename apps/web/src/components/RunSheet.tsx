import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { go, ui, useChat, useWb } from '../lib/app'
import { isActive, runFromHistory, type RunView } from '../lib/runs'
import { personaFor, runMeta } from '../lib/workbench'
import type { Approval, HubEvent, Run } from '../lib/types'
import ApprovalCard from './ApprovalCard'
import { Who } from './Avatar'
import Icon from './Icon'
import { PlanList, ToolList } from './RunSteps'
import Sheet from './Sheet'
import TextViewer from './TextViewer'

const statusText: Record<string, string> = {
  queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断',
}

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
  const acts = wb.live[id] ?? []
  const approvals = view?.approvals ?? []
  const pending = approvals.filter((x) => x.status === 'pending' && x.review === 'user')
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

  const stateIcon = run?.paused ? 'pause' : ({ queued: 'clock', running: 'play', succeeded: 'check', failed: 'alert', cancelled: 'x', interrupted: 'alert' } as Record<string, string>)[run?.status ?? ''] ?? 'dot'
  const title = task?.title || (run?.persona ? `${run.persona} 的 Run` : '运行详情')
  const head = run ? (
    <div className="flex items-center gap-3 min-w-0">
      <Who persona={persona} agent={run.agent || 'sous'} size={40} />
      <div className="grid min-w-0 leading-tight gap-1">
        <h2 className="m-0 text-[17px] font-bold tracking-tight overflow-hidden text-ellipsis whitespace-nowrap">{title}</h2>
        <span className="flex items-center gap-2 text-[11.5px] text-muted min-w-0 flex-wrap">
          <span className={`pill tone-${tone} h-5`}><Icon name={stateIcon} size={10} stroke={2.4} />{runState}</span>
          <span className="inline-flex items-center gap-0.5"><Icon name="user" size={11} />{persona?.name ?? '未分配'}</span>
          {meta?.model && <span className="inline-flex items-center gap-0.5"><Icon name="cpu" size={11} />{meta.model}</span>}
          {meta?.effort && <span className="inline-flex items-center gap-0.5"><Icon name="brain" size={11} />{meta.effort}</span>}
          {node && <span className="inline-flex items-center gap-0.5"><Icon name="laptop" size={11} />{node.name}</span>}
          {run.branch && <span className="inline-flex items-center gap-0.5 min-w-0"><Icon name="branch" size={11} /><span className="overflow-hidden text-ellipsis whitespace-nowrap max-w-[160px]">{run.branch}</span></span>}
        </span>
      </div>
    </div>
  ) : undefined

  return (
    <>
      <Sheet title={title} wide onClose={onClose} head={head}>
        {!run ? (
          <p className="text-muted text-[13px] m-0">{error || '这个 Run 不在了。'}</p>
        ) : (
          <>
            <div className="flex gap-1.5 items-center flex-wrap">
              {isActive(run) && run.paused ? (
                <button className="btn small" disabled={!!busy} onClick={() => void act('resume', () => api.resumeRun(run.id))}><Icon name="play" size={12} />继续</button>
              ) : run.status === 'running' && run.purpose !== 'deliver' ? (
                <button className="btn small ghost" disabled={!!busy} onClick={() => void act('pause', () => api.pauseRun(run.id))}><Icon name="pause" size={12} />暂停</button>
              ) : null}
              {isActive(run) && <button className="btn small ghost hover:!text-danger" disabled={!!busy} onClick={() => void act('cancel', () => api.cancelRun(run.id))}><Icon name="stop" size={12} />取消</button>}
              {run.diffstat && run.purpose !== 'deliver' && <button className="icon-btn solid" aria-label="改动" data-tip="改动" data-tip-pos="bottom" disabled={!!busy} onClick={() => void open('diff')}><Icon name="diff" size={16} /></button>}
              {run.status !== 'queued' && <button className="icon-btn solid" aria-label="日志" data-tip="日志" data-tip-pos="bottom" disabled={!!busy} onClick={() => void open('log')}><Icon name="log" size={16} /></button>}
              {run.pr_url && <a className="icon-btn solid" aria-label="PR" data-tip="PR" data-tip-pos="bottom" href={run.pr_url} target="_blank" rel="noreferrer"><Icon name="pr" size={16} /></a>}
              <span className="flex-1"></span>
              {task && (
                <button className="btn small ghost" onClick={() => { ui.state.runSheet = ''; ui.emit(); go({ view: 'task', id: task.id }) }}>
                  <Icon name="target" size={14} />任务<Icon name="forward" size={12} />
                </button>
              )}
            </div>

            {error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere]" role="alert">{error}</p>}
            {run.summary && <p className="m-0 text-[14px] whitespace-pre-wrap [overflow-wrap:anywhere]">{run.summary}</p>}
            {run.error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere] flex gap-1.5 items-start"><Icon name="alert" size={14} className="mt-[2px]" />{run.error}</p>}
            {run.diffstat && <p className="m-0 text-muted text-[12px] font-mono flex gap-1.5 items-center"><Icon name="diff" size={13} />{run.diffstat}</p>}

            {pending.map((x) => <ApprovalCard key={x.id} approval={x} context={task?.title} />)}

            <section className="grid gap-2 card p-3">
              <h3 className="m-0 text-[12px] font-semibold text-muted flex items-center gap-1.5"><Icon name="pulse" size={14} />活动</h3>
              {acts.length > 0 ? (
                <ol className="list-none m-0 p-0 grid gap-1 text-[12.5px] font-mono">
                  {acts.map((x) => (
                    <li key={x.id} className={`flex gap-2 items-start [overflow-wrap:anywhere] ${x.kind === 'thought' ? 'text-faint italic' : x.kind === 'permission' || x.kind === 'status' ? 'text-warm' : ''}`}>
                      <span className="mt-[3px] grid">
                        {x.kind === 'tool' ? (
                          <span className={`mt-[2px] w-2 h-2 rounded-full ${x.status === 'completed' ? 'bg-ok' : x.status === 'failed' ? 'bg-danger' : x.status === 'in_progress' ? 'bg-info live-dot' : 'bg-faint'}`}></span>
                        ) : (
                          <Icon name={x.kind === 'thought' ? 'brain' : x.kind === 'permission' ? 'shield' : x.kind === 'status' ? 'bulb' : 'forward'} size={12} />
                        )}
                      </span>
                      <span className="flex-1 min-w-0">{x.text}</span>
                    </li>
                  ))}
                </ol>
              ) : (
                <p className="m-0 text-faint text-[12.5px] flex items-center gap-1.5">
                  <Icon name="clock" size={13} />{steps.length || plan.length ? '没有实时输出' : '还没有输出'}
                </p>
              )}
            </section>

            {plan.length > 0 && (
              <section className="grid gap-2 card p-3">
                <h3 className="m-0 text-[12px] font-semibold text-muted flex items-center gap-1.5"><Icon name="list" size={14} />计划</h3>
                <PlanList plan={plan} />
              </section>
            )}

            {steps.length > 0 && (
              <section className="grid gap-2 card p-3">
                <h3 className="m-0 text-[12px] font-semibold text-muted flex items-center gap-1.5"><Icon name="terminal" size={14} />步骤</h3>
                <ToolList tools={steps} />
              </section>
            )}
          </>
        )}
      </Sheet>

      {viewer && <TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => setViewer(null)} />}
    </>
  )
}
