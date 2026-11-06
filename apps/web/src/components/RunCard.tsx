import { useState } from 'react'
import { api } from '../lib/api'
import { gitOpText, isActive, type RunView } from '../lib/runs'
import AgentIcon, { agentTitle } from './AgentIcon'
import ApprovalCard from './ApprovalCard'
import { errorKindText } from '../lib/tasks'
import Icon from './Icon'
import { PlanList, ToolList } from './RunSteps'
import TextViewer from './TextViewer'

const statusText: Record<string, string> = {
  queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断',
}
const purposeText: Record<string, string> = { rebase: 'rebase', deliver: '交付', clone: 'clone', create: '新建项目' }

export default function RunCard({ view, nodeName, full = false }: { view: RunView; nodeName: (id: string) => string; full?: boolean }) {
  const run = view.run
  const pending = view.approvals.filter((a) => a.status === 'pending')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [viewer, setViewer] = useState<{ title: string; text: string; diff: boolean } | null>(null)
  const [showOutput, setShowOutput] = useState(false)
  const [allSteps, setAllSteps] = useState(false)

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

  const tone = run.paused && isActive(run)
    ? 'warning'
    : run.status === 'running' || run.status === 'queued'
      ? 'accent'
      : run.status === 'succeeded'
        ? 'success'
        : run.status === 'failed' || run.status === 'interrupted'
          ? 'danger'
          : 'neutral'
  const stateIcon: Record<string, string> = { queued: 'clock', running: 'play', succeeded: 'check', failed: 'alert', cancelled: 'x', interrupted: 'alert' }
  const system = run.purpose === 'deliver' || run.purpose === 'clone' || run.purpose === 'create'
  const who = system ? 'sous' : run.agent
  const tools = allSteps ? view.tools : view.tools.slice(full ? -6 : -8)

  return (
    <>
      <section className="card p-3.5 grid gap-2.5 min-w-0" data-testid="run-card">
        <header className="flex items-center gap-2.5 min-w-0">
          <AgentIcon name={who} size={30} />
          <div className="grid min-w-0 flex-1 leading-tight">
            <span className="font-semibold text-[14px] overflow-hidden text-ellipsis whitespace-nowrap">
              {run.persona && (run.purpose === 'work' || run.purpose === 'rebase') ? `${run.persona} · ` : ''}{agentTitle(who)}
              {purposeText[run.purpose] && <span className="text-muted font-normal"> · {purposeText[run.purpose]}</span>}
            </span>
            <span className="flex items-center gap-2 text-[11.5px] text-muted min-w-0 mt-0.5">
              <span className="inline-flex items-center gap-0.5 whitespace-nowrap"><Icon name="laptop" size={11} />{nodeName(run.node_id)}</span>
              {run.branch && <span className="inline-flex items-center gap-0.5 min-w-0"><Icon name="branch" size={11} /><span className="overflow-hidden text-ellipsis whitespace-nowrap">{run.branch}</span></span>}
              {run.recovery_note && <span className="inline-flex items-center gap-0.5 text-warm" title={run.recovery_note}><Icon name="refresh" size={11} />重启</span>}
            </span>
          </div>
          <span className={`pill tone-${tone}`}>
            <Icon name={run.paused && isActive(run) ? 'pause' : stateIcon[run.status] ?? 'dot'} size={11} stroke={2.4} />
            {run.paused && isActive(run) ? '已暂停' : (statusText[run.status] ?? run.status)}
          </span>
        </header>
        {run.purpose === 'deliver' ? (
          <p className="m-0 line-clamp-2 text-[13px] text-muted flex gap-1.5 items-start"><Icon name="pr" size={14} className="mt-[3px]" />push 分支并创建 PR{run.title ? `：${run.title}` : ''}</p>
        ) : run.purpose === 'git' ? (
          <p className="m-0 line-clamp-2 text-[13px] text-muted flex gap-1.5 items-start">
            <Icon name="branch" size={14} className="mt-[3px]" />{gitOpText(run.git)}{run.summary && !isActive(run) ? `：${run.summary}` : ''}
          </p>
        ) : run.purpose === 'clone' || run.purpose === 'create' ? (
          <p className="m-0 line-clamp-2 text-[13px] text-muted">{run.title}</p>
        ) : (
          <p className="m-0 line-clamp-2 text-[13px] text-muted" title={run.prompt}>{run.prompt}</p>
        )}

        {view.plan.length > 0 && <PlanList plan={view.plan} />}
        {tools.length > 0 && <ToolList tools={tools} live={isActive(run)} more={view.tools.length - tools.length} onMore={full ? () => setAllSteps(true) : undefined} />}
        {run.paused && isActive(run) ? (
          <p className="m-0 text-[12.5px] text-warm flex items-center gap-1.5" role="status"><Icon name="pause" size={12} />{run.pause_reason || '已暂停'}</p>
        ) : view.statusLine && isActive(run) ? (
          <p className="m-0 text-[12.5px] text-muted flex items-center gap-1.5"><span className="w-1.5 h-1.5 rounded-full bg-info live-dot"></span>{view.statusLine}</p>
        ) : null}

        {pending.map((a) => <ApprovalCard key={a.id} approval={a} />)}

        {view.output && isActive(run) && showOutput && (
          <pre className="m-0 max-h-[220px] overflow-auto font-mono text-[11.5px] leading-[1.5] bg-sunken rounded-[12px] px-2.5 py-2 whitespace-pre-wrap [overflow-wrap:anywhere]">
            {view.output}
          </pre>
        )}

        {!isActive(run) && (
          <>
            {run.summary && <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere] text-[13.5px]">{run.summary}</p>}
            {run.error && (run.error_kind && errorKindText[run.error_kind] ? (
              <div className="grid gap-1">
                <p className="m-0 text-danger text-[12.5px] flex gap-1.5 items-start"><Icon name="alert" size={14} className="mt-[2px]" />{errorKindText[run.error_kind]}</p>
                <details className="text-[12px] text-muted">
                  <summary className="cursor-pointer">原始错误</summary>
                  <pre className="m-0 mt-1 whitespace-pre-wrap [overflow-wrap:anywhere] font-mono text-[11.5px]">{run.error}</pre>
                </details>
              </div>
            ) : (
              <p className="m-0 text-danger text-[12.5px] [overflow-wrap:anywhere] flex gap-1.5 items-start"><Icon name="alert" size={14} className="mt-[2px]" />{run.error}</p>
            ))}
            {run.outcome === 'no_action' && (
              <p className="m-0 text-warm text-[12.5px] flex gap-1.5 items-start"><Icon name="alert" size={14} className="mt-[2px]" />agent 没调用任何工具、没有改动就结束了，没有算作完成。</p>
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

        <footer className="flex gap-1 items-center -mb-1 -ml-1.5">
          {isActive(run) && run.paused ? (
            <button className="btn small" disabled={!!busy} onClick={() => void act('resume', () => api.resumeRun(run.id))}><Icon name="play" size={12} />继续</button>
          ) : run.status === 'running' && run.purpose !== 'deliver' ? (
            <button className="icon-btn" aria-label="暂停" data-tip="暂停" data-tip-pos="bottom" disabled={!!busy} onClick={() => void act('pause', () => api.pauseRun(run.id))}><Icon name="pause" size={15} /></button>
          ) : null}
          {isActive(run) && (
            <button className="icon-btn hover:!text-danger" aria-label="取消" data-tip="取消" data-tip-pos="bottom" disabled={!!busy} onClick={() => void act('cancel', () => api.cancelRun(run.id))}><Icon name="stop" size={15} /></button>
          )}
          {view.output && isActive(run) && (
            <button className={`icon-btn ${showOutput ? '!text-ink bg-hover' : ''}`} aria-label="agent 的输出" data-tip="输出" data-tip-pos="bottom" onClick={() => setShowOutput(!showOutput)}><Icon name="terminal" size={16} /></button>
          )}
          {run.diffstat && run.purpose !== 'deliver' && (
            <button className="icon-btn" aria-label="查看改动" data-tip="改动" data-tip-pos="bottom" disabled={!!busy} onClick={() => void open('diff')}><Icon name="diff" size={16} /></button>
          )}
          {run.status !== 'queued' && (
            <button className="icon-btn" aria-label="日志" data-tip="日志" data-tip-pos="bottom" disabled={!!busy} onClick={() => void open('log')}><Icon name="log" size={16} /></button>
          )}
        </footer>
        {error && <p className="m-0 text-danger text-[12.5px] [overflow-wrap:anywhere]" role="alert">{error}</p>}
      </section>

      {viewer && <TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => setViewer(null)} />}
    </>
  )
}
