import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api } from '../lib/api'
import { chat, useChat } from '../lib/app'
import { extractLinks, jobTitle, mergeJobTail } from '../lib/jobs'

const keys: [string, string][] = [['回车', '\r'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['空格', ' '], ['Esc', '\x1b'], ['Ctrl+C', '\x03']]

export default function JobLog({ nodeId, jobId }: { nodeId: string; jobId: string }) {
  const chatState = useChat()
  const job = chatState.jobs[jobId]
  const jobRef = useRef(job)
  jobRef.current = job

  const box = useRef<HTMLDivElement>(null)
  const term = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const written = useRef(0)
  const epoch = useRef(-1)
  const observer = useRef<ResizeObserver | null>(null)
  const [cols, setCols] = useState(80)
  const [line, setLine] = useState('')
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')

  const links = job ? extractLinks(job.data, cols) : { urls: [], codes: [] }

  function color(name: string, fallback: string) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
  }

  // Keystrokes are sent in small batches.
  const pending = useRef('')
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  function send(data: string) {
    if (!jobRef.current?.running) return
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
        setError((e as Error).message)
      }
    }, 30)
  }

  const resizeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  function refit() {
    if (!term.current || !fit.current) return
    try {
      fit.current.fit()
    } catch {
      return
    }
    setCols(term.current.cols)
    if (resizeTimer.current) clearTimeout(resizeTimer.current)
    resizeTimer.current = setTimeout(() => {
      if (jobRef.current?.running && term.current) api.jobResize(nodeId, jobId, term.current.cols, term.current.rows).catch(() => {})
    }, 200)
  }

  useEffect(() => {
    if (!box.current) return
    const t = new Terminal({
      convertEol: false,
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, Menlo, Consolas, monospace',
      scrollback: 5000,
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
    observer.current = ro
    ro.observe(box.current)
    api.jobTail(nodeId, jobId).then((tail) => {
      mergeJobTail(chat.state.jobs, nodeId, tail)
      chat.emit()
    }).catch((e) => setError((e as Error).message))
    return () => {
      observer.current?.disconnect()
      t.dispose()
      if (timer.current) clearTimeout(timer.current)
      if (resizeTimer.current) clearTimeout(resizeTimer.current)
    }
  }, [nodeId, jobId])

  // Write what arrived since the last time; start over when it was replaced.
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

  function submitLine() {
    if (!job?.running) return
    send(line + '\r')
    setLine('')
  }

  async function copy(text: string) {
    await navigator.clipboard?.writeText(text)
    setCopied(text)
    setTimeout(() => setCopied(''), 1500)
  }

  async function stop() {
    try {
      await api.jobCancel(nodeId, jobId)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="grid gap-2 min-w-0">
      <div className="flex gap-2.5 items-baseline flex-wrap text-[13px]">
        <span className="font-semibold">{jobTitle[job?.kind ?? ''] ?? '作业'}</span>
        {job?.command && <code className="font-mono text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap max-w-full flex-1 min-w-0" title={job.command}>{job.command}</code>}
        <span className={`${!job || job.running ? 'text-muted' : job.exit_code === 0 ? 'text-done' : 'text-danger'}`}>
          {!job || job.running ? '进行中…' : job.error === '已停止' ? '已停止' : job.exit_code === 0 ? '已完成' : `失败（退出码 ${job.exit_code}）`}
        </span>
        {job?.running && <button className="link danger" onClick={() => void stop()}>停止</button>}
      </div>

      {(links.urls.length > 0 || links.codes.length > 0) && (
        <div className="border border-accent rounded-[12px] px-2.5 py-2 grid gap-1.5 text-[13px]">
          {links.urls.map((u) => (
            <div key={u} className="flex gap-2.5 items-baseline min-w-0">
              <a className="text-accent font-semibold whitespace-nowrap" href={u} target="_blank" rel="noreferrer noopener" title={u}>打开链接</a>
              <span className="text-muted overflow-hidden text-ellipsis whitespace-nowrap flex-1 min-w-0 font-mono text-[12px]">{u}</span>
              <button className="link" onClick={() => void copy(u)}>{copied === u ? '已复制' : '复制'}</button>
            </div>
          ))}
          {links.codes.map((c) => (
            <div key={c} className="flex gap-2.5 items-baseline min-w-0">
              <span>验证码</span><strong className="font-mono font-semibold text-[15px] tracking-[1px]">{c}</strong>
              <button className="link" onClick={() => void copy(c)}>{copied === c ? '已复制' : '复制'}</button>
            </div>
          ))}
        </div>
      )}

      <div className="h-[300px] bg-sunken rounded-[12px] p-1.5 overflow-hidden max-[760px]:h-[240px]" ref={box}></div>

      {job?.running ? (
        <>
          <div className="flex gap-2">
            <input
              className="flex-1 min-w-0 border border-line bg-page text-ink rounded-[10px] px-2.5 py-1.5 font-mono text-[13px]"
              value={line}
              onChange={(e) => setLine(e.currentTarget.value)}
              placeholder="粘贴验证码或输入后按回车发送"
              aria-label="发送到终端"
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              onKeyDown={(e) => e.key === 'Enter' && !e.nativeEvent.isComposing && (e.preventDefault(), submitLine())}
            />
            <button className="btn small px-3.5 text-[13px]" onClick={submitLine}>发送</button>
          </div>
          <div className="flex gap-1.5 flex-wrap">
            {keys.map(([label, seq]) => (
              <button key={label} className="border border-line bg-panel text-ink rounded-[8px] px-[9px] py-[3px] text-[12px]" onClick={() => send(seq)}>{label}</button>
            ))}
          </div>
        </>
      ) : job?.error && job.error !== '已停止' ? (
        <p className="text-muted text-[13px] m-0">{job.error}</p>
      ) : null}
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
    </div>
  )
}
