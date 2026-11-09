import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { looks, runLook, runStatusText as statusText } from '../lib/status'
import { gitOpText, isActive, type RunView } from '../lib/runs'
import { duration } from '../lib/time'
import AgentIcon, { agentTitle } from './AgentIcon'
import ApprovalCard from './ApprovalCard'
import { errorKindText } from '../lib/tasks'
import Icon from './Icon'
import Markdown from './Markdown'
import { Clamp, Thinking } from './Message'
import { PlanList, ToolList } from './RunSteps'
import TextViewer from './TextViewer'

const purposeText: Record<string, string> = { rebase: 'rebase', deliver: '交付', clone: 'clone', create: '新建项目' }

/** The current time, ticking every second while on is true. */
function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!on) return
    setNow(Date.now())
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [on])
  return now
}

/**
 * One Run in a task's run history, as a card. The agent's steps, thinking and raw output fold
 * into one "执行过程" line with how long it ran; the latest run shows its
 * prompt and final reply, earlier ones only their header until opened.
 */
export default function RunCard({ view, nodeName, latest = true }: { view: RunView; nodeName: (id: string) => string; latest?: boolean }) {
  const run = view.run
  const pending = view.approvals.filter((a) => a.status === 'pending' && a.review === 'user')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [viewer, setViewer] = useState<{ title: string; text: string; diff: boolean } | null>(null)
  const [opened, setOpened] = useState<boolean | null>(null)
  const [process, setProcess] = useState(false)
  const active = isActive(run)
  const now = useNow(active && run.status === 'running')

  async function act(key: string, fn: () => Promise<unknown>) {
    setBusy(key)
    setError('')
    try {
      await fn()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  async function open(what: 'log' | 'diff') {
    await act(what, async () => {
      const text = await api.runText(run.id, what)
      setViewer({ title: what === 'diff' ? `改动 · ${run.branch}` : '运行日志', text, diff: what === 'diff' })
    })
  }

  const appearance = looks[runLook(run)]
  const tone = appearance.tone
  const system = run.purpose === 'deliver' || run.purpose === 'clone' || run.purpose === 'create'
  const who = system ? 'sous' : run.agent
  const start = run.started_at || 0
  const took = start ? duration((run.ended_at || (active ? now : 0)) - start) : ''
  // Earlier runs fold to their header; the latest (and any waiting for a person) stay open.
  const expanded = opened ?? (latest || pending.length > 0)
  const hasProcess = view.plan.length > 0 || view.tools.length > 0 || !!view.thinking || (!!view.output && (active || !run.summary))
  const steps = view.tools.length

  const prompt = run.purpose === 'deliver'
    ? `push 分支并创建 PR${run.title ? `：${run.title}` : ''}`
    : run.purpose === 'git'
      ? `${gitOpText(run.git)}${run.summary && !active ? `：${run.summary}` : ''}`
      : run.purpose === 'clone' || run.purpose === 'create'
        ? run.title
        : run.prompt

  return (
    <>
      <section className="rounded-[16px] bg-[color-mix(in_srgb,var(--sunken)_55%,var(--panel))] p-4 grid grid-cols-[minmax(0,1fr)] gap-2 min-w-0" data-testid="run-card">
        <header className="flex items-center gap-2 min-w-0">
          <button
            className="flex items-center gap-2 min-w-0 flex-1 border-0 bg-transparent p-0 text-left text-ink cursor-pointer"
            aria-expanded={expanded}
            aria-label={`${agentTitle(who)}，${statusText[run.status] ?? run.status}${took ? `，用时 ${took}` : ''}`}
            onClick={() => setOpened(!expanded)}
          >
            <AgentIcon name={who} size={22} />
            <span className="font-semibold text-[13.5px] overflow-hidden text-ellipsis whitespace-nowrap">
              {run.persona && (run.purpose === 'work' || run.purpose === 'rebase') ? `${run.persona} · ` : ''}{agentTitle(who)}
              {purposeText[run.purpose] && <span className="text-muted font-normal"> · {purposeText[run.purpose]}</span>}
            </span>
            <span className="flex items-center gap-2 text-[11.5px] text-muted min-w-0 max-[760px]:hidden">
              <span className="inline-flex items-center gap-0.5 whitespace-nowrap"><Icon name="laptop" size={11} />{nodeName(run.node_id)}</span>
              {run.branch && <span className="inline-flex items-center gap-0.5 min-w-0"><Icon name="branch" size={11} /><span className="overflow-hidden text-ellipsis whitespace-nowrap">{run.branch}</span></span>}
              {run.recovery_note && <span className="inline-flex items-center gap-0.5 text-warm" title={run.recovery_note}><Icon name="refresh" size={11} />重启</span>}
            </span>
            <span className="flex-1"></span>
            {took && <span className="text-[11.5px] text-muted tabular-nums whitespace-nowrap inline-flex items-center gap-0.5"><Icon name="clock" size={11} />{took}</span>}
            <span className={`pill tone-${tone}`}>
              <Icon name={appearance.icon} size={11} stroke={2.4} />
              {run.paused && active ? '已暂停' : (statusText[run.status] ?? run.status)}
            </span>
          </button>
          <span className="flex items-center -mr-1.5">
            {active && run.paused ? (
              <button className="icon-btn" aria-label="继续" data-tip="继续" data-tip-pos="bottom" disabled={!!busy} onClick={() => void act('resume', () => api.resumeRun(run.id))}><Icon name="play" size={15} /></button>
            ) : run.status === 'running' && run.purpose !== 'deliver' ? (
              <button className="icon-btn" aria-label="暂停" data-tip="暂停" data-tip-pos="bottom" disabled={!!busy} onClick={() => void act('pause', () => api.pauseRun(run.id))}><Icon name="pause" size={15} /></button>
            ) : null}
            {active && (
              <button className="icon-btn hover:!text-danger" aria-label="取消" data-tip="取消" data-tip-pos="bottom" disabled={!!busy} onClick={() => void act('cancel', () => api.cancelRun(run.id))}><Icon name="stop" size={15} /></button>
            )}
            {run.diffstat && run.purpose !== 'deliver' && (
              <button className="icon-btn" aria-label="查看改动" data-tip="改动" data-tip-pos="bottom" disabled={!!busy} onClick={() => void open('diff')}><Icon name="diff" size={16} /></button>
            )}
            {run.status !== 'queued' && (
              <button className="icon-btn" aria-label="日志" data-tip="日志" data-tip-pos="bottom" disabled={!!busy} onClick={() => void open('log')}><Icon name="log" size={16} /></button>
            )}
          </span>
        </header>

        {expanded && (
          <>
            {prompt && <p className="m-0 line-clamp-2 text-[13px] text-muted [overflow-wrap:anywhere]" title={prompt}>{prompt}</p>}

            {(hasProcess || active) && (
              <button
                className="justify-self-start inline-flex items-center gap-1.5 border-0 bg-transparent p-0 text-[12.5px] text-muted hover:text-ink min-w-0 max-w-full"
                aria-expanded={process}
                onClick={() => setProcess(!process)}
              >
                {active && !run.paused ? <span className="w-1.5 h-1.5 rounded-full bg-info live-dot flex-none"></span> : <Icon name="terminal" size={13} />}
                <span className="font-semibold whitespace-nowrap">{active ? '执行中' : '执行过程'}</span>
                {took && <span className="tabular-nums whitespace-nowrap">· {took}</span>}
                {steps > 0 && <span className="whitespace-nowrap">· {steps} 步</span>}
                {active && !run.paused && view.statusLine && !process && <span className="overflow-hidden text-ellipsis whitespace-nowrap">· {view.statusLine}</span>}
                <Icon name="chevron" size={12} className={`flex-none transition-transform ${process ? 'rotate-180' : '-rotate-90'}`} />
              </button>
            )}
            {process && (
              <div className="grid grid-cols-[minmax(0,1fr)] gap-2 pl-3 border-l-2 border-line" aria-label="执行过程">
                {view.plan.length > 0 && <PlanList plan={view.plan} />}
                {view.tools.length > 0 && <ToolList tools={view.tools} live={active} />}
                {/* The agent's own words, drawn like an assistant message in the chat. */}
                {view.thinking && <Thinking text={view.thinking} live={active && !run.paused && !view.output} />}
                {view.output && (active || !run.summary) && (
                  <Clamp max={320} follow={active} label="输出">
                    <Markdown text={view.output} streaming={active} />
                  </Clamp>
                )}
                {view.statusLine && active && !run.paused && (
                  <p className="m-0 text-[12.5px] text-muted flex items-center gap-1.5"><span className="w-1.5 h-1.5 rounded-full bg-info live-dot"></span>{view.statusLine}</p>
                )}
                {!hasProcess && !view.statusLine && <p className="m-0 text-[12.5px] text-faint">还没有动静</p>}
              </div>
            )}
            {run.paused && active && (
              <p className="m-0 text-[12.5px] text-warm flex items-center gap-1.5" role="status"><Icon name="pause" size={12} />{run.pause_reason || '已暂停'}</p>
            )}
          </>
        )}

        {pending.map((a) => <ApprovalCard key={a.id} approval={a} />)}

        {expanded && !active && (
          <>
            {run.summary && run.purpose !== 'git' && (
              <Clamp max={320} label="最终回复"><Markdown text={run.summary} /></Clamp>
            )}
            {run.error && (run.status === 'failed' && run.error_kind && errorKindText[run.error_kind] ? (
              <div className="grid gap-1">
                <p className="m-0 text-danger text-[12.5px] flex gap-1.5 items-start"><Icon name="alert" size={14} className="mt-[2px]" />{errorKindText[run.error_kind]}</p>
                <details className="text-[12px] text-muted">
                  <summary className="cursor-pointer">原始错误</summary>
                  <pre className="m-0 mt-1 whitespace-pre-wrap [overflow-wrap:anywhere] font-mono text-[11.5px]">{run.error}</pre>
                </details>
              </div>
            ) : (
              <p className={`m-0 ${run.status === 'interrupted' ? 'text-muted' : 'text-danger'} text-[12.5px] [overflow-wrap:anywhere] flex gap-1.5 items-start`}><Icon name="alert" size={14} className="mt-[2px]" />{run.error}</p>
            ))}
            {run.outcome === 'no_action' && (
              <p className="m-0 text-warm text-[12.5px] flex gap-1.5 items-start"><Icon name="alert" size={14} className="mt-[2px]" />本轮正常结束，未记录到工具调用或本轮文件改动；任务是否完成以验收结论为准。</p>
            )}
            {!!run.refused?.length && (
              <p className="m-0 text-muted text-[12.5px] [overflow-wrap:anywhere] flex gap-1.5 items-start"><Icon name="x" size={14} className="mt-[2px]" />被拒绝的操作：{run.refused.join('；')}</p>
            )}
            {run.diffstat && <p className="m-0 text-[12px] font-mono text-muted [overflow-wrap:anywhere] flex gap-1.5 items-center"><Icon name="diff" size={13} />{run.diffstat}</p>}
            {run.pr_url && (
              <a className="inline-flex gap-1.5 items-center text-[12.5px] text-accent font-semibold justify-self-start" href={run.pr_url} target="_blank" rel="noreferrer">
                <Icon name="pr" size={14} />PR{run.conflict ? <span className="text-danger">· 有冲突</span> : null}<Icon name="external" size={12} />
              </a>
            )}
          </>
        )}
        {error && <p className="m-0 text-danger text-[12.5px] [overflow-wrap:anywhere]" role="alert">{error}</p>}
      </section>

      {viewer && <TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => setViewer(null)} />}
    </>
  )
}
