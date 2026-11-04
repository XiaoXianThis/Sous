import { useState } from 'react'
import { api } from '../lib/api'
import { isActive, type RunView } from '../lib/runs'
import ApprovalCard from './ApprovalCard'
import TextViewer from './TextViewer'

const statusText: Record<string, string> = {
  queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', interrupted: '已中断',
}
const purposeText: Record<string, string> = { rebase: 'rebase', deliver: '交付', clone: 'clone', create: '新建项目' }
const toolIcon: Record<string, string> = { completed: '✓', failed: '✕', in_progress: '…', pending: '·' }

export default function RunCard({ view, nodeName, full = false }: { view: RunView; nodeName: (id: string) => string; full?: boolean }) {
  const run = view.run
  const pending = view.approvals.filter((a) => a.status === 'pending')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [viewer, setViewer] = useState<{ title: string; text: string; diff: boolean } | null>(null)
  const [showOutput, setShowOutput] = useState(false)

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

  const edge = run.status === 'running' || run.status === 'queued'
    ? 'var(--accent)'
    : run.status === 'succeeded'
      ? 'var(--done)'
      : run.status === 'failed' || run.status === 'interrupted'
        ? 'var(--danger)'
        : 'var(--muted)'
  const badge = run.status === 'succeeded'
    ? 'text-done'
    : run.status === 'failed' || run.status === 'interrupted'
      ? 'text-danger'
      : 'text-warm'

  return (
    <>
      <section
        className="border border-line rounded-[14px] bg-panel px-3.5 py-3 grid gap-1.5"
        style={{ borderLeft: `3px solid ${edge}` }}
        data-testid="run-card"
      >
        <header className="flex items-baseline gap-2 flex-wrap">
          <span className={`text-[12px] font-semibold ${badge}`}>
            {run.paused && isActive(run) ? '已暂停' : (statusText[run.status] ?? run.status)}
          </span>
          {run.recovery_note && <span className="text-[12px] text-muted border border-line rounded-full px-[7px]" title={run.recovery_note}>中断后重新开始</span>}
          {purposeText[run.purpose] && <span className="text-[12px] text-muted border border-line rounded-full px-[7px]">{purposeText[run.purpose]}</span>}
          <strong>{run.persona && (run.purpose === 'work' || run.purpose === 'rebase') ? `${run.persona} · ` : ''}{run.purpose === 'deliver' ? 'sous' : run.agent}</strong>
          <span className="text-muted text-[13px] [overflow-wrap:anywhere]">@ {nodeName(run.node_id)}{run.branch ? ` · ${run.branch}` : ''}</span>
        </header>
        {run.purpose === 'deliver' ? (
          <p className="m-0 line-clamp-3">push 分支并创建 PR{run.title ? `：${run.title}` : ''}</p>
        ) : run.purpose === 'clone' || run.purpose === 'create' ? (
          <p className="m-0 line-clamp-3">{run.title}</p>
        ) : (
          <p className="m-0 line-clamp-3" title={run.prompt}>{run.prompt}</p>
        )}
        {run.repo_path && <p className="m-0 text-[13px] text-muted [overflow-wrap:anywhere]">仓库：{run.repo_path}</p>}

        {view.plan.length > 0 && (
          <ul className="m-0 p-0 list-none grid gap-0.5 text-[13px]">
            {view.plan.map((e, i) => (
              <li key={i}>
                <span className={e.status === 'completed' ? 'text-done' : e.status === 'in_progress' ? 'text-warm' : 'text-muted'}>
                  {e.status === 'completed' ? '● ' : e.status === 'in_progress' ? '◐ ' : '○ '}
                </span>
                {e.content}
              </li>
            ))}
          </ul>
        )}
        {view.tools.length > 0 && (
          <ul className="m-0 p-0 list-none grid gap-0.5 text-[13px]">
            {(full ? view.tools : view.tools.slice(-8)).map((t) => {
              const done = t.status === 'completed' || t.status === 'failed'
              const show = done || isActive(run)
              return (
                <li
                  key={t.id}
                  className={`overflow-hidden text-ellipsis whitespace-nowrap ${t.status === 'completed' ? 'text-ink' : t.status === 'failed' ? 'text-danger' : 'text-muted'}`}
                >
                  <span className="inline-block w-[1.2em]">{show ? (toolIcon[t.status] ?? '·') : '–'}</span>{t.title}
                </li>
              )
            })}
            {!full && view.tools.length > 8 && <li className="text-muted italic">还有 {view.tools.length - 8} 个更早的步骤</li>}
          </ul>
        )}
        {run.paused && isActive(run) ? (
          <p className="m-0 text-[13px] text-warm" role="status">已暂停{run.pause_reason ? `：${run.pause_reason}` : ''}</p>
        ) : view.statusLine && isActive(run) ? (
          <p className="m-0 text-[13px] text-muted">{view.statusLine}</p>
        ) : null}

        {pending.map((a) => <ApprovalCard key={a.id} approval={a} />)}

        {view.output && isActive(run) && (
          <>
            <button className="link justify-self-start" onClick={() => setShowOutput(!showOutput)}>{showOutput ? '收起' : '查看'} agent 的输出</button>
            {showOutput && (
              <pre className="m-0 max-h-[200px] overflow-auto font-mono text-[12px] leading-[1.45] bg-page rounded-[8px] px-2.5 py-2 whitespace-pre-wrap [overflow-wrap:anywhere]">
                {view.output}
              </pre>
            )}
          </>
        )}

        {!isActive(run) && (
          <>
            {run.summary && <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{run.summary}</p>}
            {run.error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere]">{run.error}</p>}
            {run.diffstat && <p className="m-0 text-[13px] text-muted [overflow-wrap:anywhere]">{run.diffstat}</p>}
            {run.pr_url && (
              <p className="m-0 text-[13px] text-muted [overflow-wrap:anywhere]">
                <a className="text-accent" href={run.pr_url} target="_blank" rel="noreferrer">{run.pr_url}</a>{run.conflict ? '（有冲突）' : ''}
              </p>
            )}
          </>
        )}

        <footer className="flex gap-3.5">
          {isActive(run) && run.paused ? (
            <button className="link strong justify-self-start" disabled={!!busy} onClick={() => void act('resume', () => api.resumeRun(run.id))}>继续</button>
          ) : run.status === 'running' && run.purpose !== 'deliver' ? (
            <button className="link justify-self-start" disabled={!!busy} onClick={() => void act('pause', () => api.pauseRun(run.id))}>暂停</button>
          ) : null}
          {isActive(run) && (
            <button className="link justify-self-start" disabled={!!busy} onClick={() => void act('cancel', () => api.cancelRun(run.id))}>取消</button>
          )}
          {run.diffstat && run.purpose !== 'deliver' && (
            <button className="link justify-self-start" disabled={!!busy} onClick={() => void open('diff')}>查看改动</button>
          )}
          {run.status !== 'queued' && (
            <button className="link justify-self-start" disabled={!!busy} onClick={() => void open('log')}>日志</button>
          )}
        </footer>
        {error && <p className="m-0 text-danger text-[13px] [overflow-wrap:anywhere]" role="alert">{error}</p>}
      </section>

      {viewer && <TextViewer title={viewer.title} text={viewer.text} diff={viewer.diff} onClose={() => setViewer(null)} />}
    </>
  )
}
