import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { ApiError, api } from '../lib/api'
import { chat, useChat } from '../lib/app'
import { mergeJobTail } from '../lib/jobs'

// Keys a phone keyboard lacks.
const keys: [string, string][] = [['Esc', '\x1b'], ['Tab', '\t'], ['Ctrl+C', '\x03'], ['Ctrl+D', '\x04'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['←', '\x1b[D'], ['→', '\x1b[C']]

function color(name: string, fallback: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

/**
 * One shell on a machine, filling its container. Output arrives as
 * agent.job.output events (chat.jobs); keystrokes go to the job's input
 * endpoint in small batches. Stays mounted while hidden, so switching tabs
 * keeps the screen; active refits it when it is shown again.
 */
export default function ShellTerm({ nodeId, jobId, active, onGone }: {
  nodeId: string
  jobId: string
  active: boolean
  /** The machine no longer has this shell (it restarted, or it was closed long ago). */
  onGone: () => void
}) {
  const job = useChat().jobs[jobId]
  const jobRef = useRef(job)
  jobRef.current = job
  const box = useRef<HTMLDivElement>(null)
  const term = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const written = useRef(0)
  const epoch = useRef(-1)
  const [error, setError] = useState('')

  const pending = useRef('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  function send(data: string) {
    if (jobRef.current && !jobRef.current.running) return
    pending.current += data
    if (timer.current) return
    timer.current = setTimeout(async () => {
      const d = pending.current
      pending.current = ''
      timer.current = null
      try {
        await api.jobInput(nodeId, jobId, d)
        setError('')
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) onGone()
        else setError((e as Error).message)
      }
    }, 15)
  }

  const resizeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  function refit() {
    if (!term.current || !fit.current || !box.current?.offsetParent) return // hidden
    try {
      fit.current.fit()
    } catch {
      return
    }
    if (resizeTimer.current) clearTimeout(resizeTimer.current)
    resizeTimer.current = setTimeout(() => {
      if (jobRef.current?.running !== false && term.current) api.jobResize(nodeId, jobId, term.current.cols, term.current.rows).catch(() => {})
    }, 150)
  }

  useEffect(() => {
    if (!box.current) return
    const t = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, Menlo, Consolas, monospace',
      scrollback: 10000,
      theme: {
        background: color('--sunken', '#efebe3'),
        foreground: color('--text', '#22201c'),
        cursor: color('--accent', '#1f3b2d'),
        selectionBackground: color('--line', '#e2ddd2'),
      },
    })
    term.current = t
    const f = new FitAddon()
    fit.current = f
    t.loadAddon(f)
    t.open(box.current)
    t.onData(send)
    refit()
    const ro = new ResizeObserver(() => refit())
    ro.observe(box.current)
    api.jobTail(nodeId, jobId).then((tail) => {
      mergeJobTail(chat.state.jobs, nodeId, tail)
      chat.emit()
    }).catch((e) => {
      if (e instanceof ApiError && e.status === 404) onGone()
      else setError((e as Error).message)
    })
    return () => {
      ro.disconnect()
      t.dispose()
      if (timer.current) clearTimeout(timer.current)
      if (resizeTimer.current) clearTimeout(resizeTimer.current)
    }
  }, [nodeId, jobId])

  useEffect(() => {
    if (!active) return
    requestAnimationFrame(() => {
      refit()
      term.current?.focus()
    })
  }, [active])

  useEffect(() => {
    const j = job
    if (!term.current || !j) return
    if (j.epoch !== epoch.current) {
      epoch.current = j.epoch
      term.current.reset()
      written.current = 0
    }
    if (j.data.length > written.current) {
      term.current.write(j.data.slice(written.current))
      written.current = j.data.length
    }
  }, [job?.data, job?.epoch])

  const ended = job && !job.running
  return (
    <div className={`grid grid-rows-[minmax(0,1fr)_auto] gap-2 h-full min-h-0 ${active ? '' : 'hidden'}`}>
      <div className="relative min-h-0 bg-sunken rounded-[12px] p-1.5 overflow-hidden">
        <div className="h-full w-full" ref={box}></div>
      </div>
      <div className="flex items-center gap-1.5 flex-wrap min-h-[26px]">
        {ended ? (
          <span className="text-muted text-[13px]">
            {job.error === '已停止' ? '已关闭' : `已退出（退出码 ${job.exit_code ?? 0}）`}
          </span>
        ) : (
          keys.map(([label, seq]) => (
            <button key={label} className="border border-line bg-panel text-ink rounded-[8px] px-[9px] py-[3px] text-[12px]" onClick={() => { send(seq); term.current?.focus() }}>{label}</button>
          ))
        )}
        {error && <span className="text-danger text-[13px]" role="alert">{error}</span>}
      </div>
    </div>
  )
}
