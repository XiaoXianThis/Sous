import { Fragment, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { newThread, openThread, send, ui, useChat, useUi, useWb } from '../lib/app'
import { isAnswering, visibleMessages } from '../lib/reduce'
import type { RunView } from '../lib/runs'
import type { Message } from '../lib/types'
import ApprovalCard from './ApprovalCard'
import Icon from './Icon'
import MessageView from './Message'
import RunChip from './RunChip'

type Item = { kind: 'message'; seq: number; message: Message } | { kind: 'run'; seq: number; run: RunView }

const statusText = { connecting: '连接中', online: '在线', offline: '离线，正在重连' }

export default function ChatPanel({ onCollapse }: { onCollapse?: () => void }) {
  const chatState = useChat()
  const uiState = useUi()
  const wbState = useWb()
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState('')
  const [picking, setPicking] = useState(false)
  const scroller = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  const stick = useRef(true)

  const nodeName = (id: string) => wbState.data?.nodes.find((n) => n.id === id)?.name ?? '机器'
  const thread = chatState.threads.find((t) => t.id === uiState.current)
  const messages = chatState.messages[uiState.current] ?? []
  const answering = isAnswering(chatState, uiState.current)

  const timeline: Item[] = visibleMessages(messages).map((m) => ({ kind: 'message', seq: m.seq, message: m }))
  for (const r of Object.values(chatState.runs)) {
    if (r.run.thread_id === uiState.current) timeline.push({ kind: 'run', seq: r.seq || r.run.created_at, run: r })
  }
  timeline.sort((a, b) => a.seq - b.seq)
  const lastLength =
    (messages.length ? messages[messages.length - 1].content.length : 0) +
    Object.values(chatState.runs).reduce((n, r) => n + r.approvals.length + r.run.status.length, 0)

  // What the next message is about: the project or task on the workbench.
  const context = (() => {
    if (uiState.dropContext || !wbState.data) return null
    const r = uiState.route
    if (r.view === 'task') {
      const t = wbState.data.tasks.find((x) => x.id === r.id)
      return { icon: 'circle', text: t ? t.title : '这个任务' }
    }
    if (r.view === 'project') {
      const p = wbState.data.projects.find((x) => x.id === r.id)
      return p ? { icon: 'folders', text: p.name } : null
    }
    return null
  })()

  useEffect(() => {
    if (stick.current && scroller.current) scroller.current.scrollTo({ top: scroller.current.scrollHeight })
  }, [uiState.current, messages.length, lastLength])

  useEffect(() => {
    resize()
  }, [draft])

  useEffect(() => {
    if (!picking) return
    const onClick = (e: MouseEvent) => {
      if (picking && !(e.target as HTMLElement).closest('.picker')) setPicking(false)
    }
    window.addEventListener('click', onClick)
    return () => window.removeEventListener('click', onClick)
  }, [picking])

  function onScroll() {
    if (scroller.current) stick.current = scroller.current.scrollHeight - scroller.current.scrollTop - scroller.current.clientHeight < 80
  }

  async function pick(id: string) {
    setPicking(false)
    stick.current = true
    await openThread(id)
  }

  async function create() {
    setPicking(false)
    await newThread()
    input.current?.focus()
  }

  async function submit() {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true)
    setSendError('')
    try {
      await send(text)
      setDraft('')
      stick.current = true
    } catch (e) {
      setSendError((e as Error).message)
    } finally {
      setSending(false)
      input.current?.focus()
    }
  }

  const [touch] = useState(() => typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches)
  function onKey(e: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (!touch && e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      void submit()
    }
  }
  function resize() {
    if (!input.current) return
    input.current.style.height = 'auto'
    input.current.style.height = Math.min(input.current.scrollHeight, 180) + 'px'
  }

  return (
    <section className="flex flex-col min-h-0 h-full bg-page" aria-label="和 sous 对话">
      <header className="flex items-center gap-1.5 pt-[calc(10px+env(safe-area-inset-top))] pr-2.5 pb-2 pl-3.5 border-b border-line">
        <span className="text-accent grid" aria-hidden="true"><Icon name="wok" size={18} /></span>
        <div className="relative picker">
          <button
            className="inline-flex items-center gap-1 border-0 bg-none font-semibold px-1 py-0.5 rounded-[8px] hover:bg-sunken"
            onClick={() => setPicking(!picking)}
            aria-expanded={picking}
            aria-haspopup="listbox"
          >
            <span>{thread?.kind === 'main' ? 'sous' : (thread?.title ?? 'sous')}</span><Icon name="chevron" size={14} />
          </button>
          {picking && (
            <div className="absolute top-full left-0 z-[25] mt-1 min-w-[220px] max-h-[50vh] overflow-y-auto bg-panel border border-line rounded-[12px] p-1 grid shadow-[0_8px_24px_rgb(0_0_0/.12)]" role="listbox" aria-label="对话">
              {chatState.threads.map((t) => (
                <button
                  key={t.id}
                  role="option"
                  aria-selected={t.id === uiState.current}
                  className={`border-0 bg-none text-left px-2.5 py-[7px] rounded-[8px] flex gap-1.5 items-center text-[14px] hover:bg-sunken ${t.id === uiState.current ? 'bg-sunken' : ''}`}
                  onClick={() => void pick(t.id)}
                >
                  {t.kind === 'main' ? <><span className="w-[7px] h-[7px] rounded-full bg-warm"></span>主线</> : t.title}
                </button>
              ))}
              <button className="border-0 bg-none text-left px-2.5 py-[7px] rounded-b-[8px] flex gap-1.5 items-center text-[14px] text-accent border-t border-line hover:bg-sunken" onClick={() => void create()}>
                <Icon name="plus" size={14} />新对话
              </button>
            </div>
          )}
        </div>
        <span className={`w-[7px] h-[7px] rounded-full ${uiState.status === 'online' ? 'bg-ok' : uiState.status === 'offline' ? 'bg-danger' : 'bg-muted'}`} title={statusText[uiState.status]}></span>
        <span className="flex-1"></span>
        {onCollapse && (
          <button className="icon-btn" aria-label="收起对话" title="收起对话" onClick={onCollapse}><Icon name="panel" /></button>
        )}
      </header>

      <div className="flex-1 min-w-0 overflow-y-auto overflow-x-hidden overscroll-contain" ref={scroller} onScroll={onScroll}>
        <div className="px-3.5 pt-3.5 pb-2 grid grid-cols-[minmax(0,1fr)] gap-3">
          {timeline.length === 0 && (
            <div className="text-center text-muted pt-[14vh] px-2 text-[14px]">
              <p className="text-[18px] text-ink m-0 mb-1 font-semibold">有什么要交给我的？</p>
              <p className="m-0">告诉我要做什么，我来安排岗位、挑机器。</p>
            </div>
          )}
          {timeline.map((item) =>
            item.kind === 'message' ? (
              <MessageView key={item.message.id} message={item.message} />
            ) : (
              <Fragment key={item.run.run.id}>
                <RunChip view={item.run} nodeName={nodeName} />
                {item.run.approvals.filter((a) => a.status === 'pending').map((a) => (
                  <ApprovalCard key={a.id} approval={a} context={item.run.run.title} />
                ))}
              </Fragment>
            ),
          )}          {answering && !messages.some((m) => m.streaming) && (
            <div className="flex gap-1 py-2 px-0.5" aria-label="正在思考">
              <span className="typing-dot w-1.5 h-1.5 rounded-full bg-muted"></span>
              <span className="typing-dot w-1.5 h-1.5 rounded-full bg-muted"></span>
              <span className="typing-dot w-1.5 h-1.5 rounded-full bg-muted"></span>
            </div>
          )}
        </div>
      </div>

      <form className="px-3 pt-1.5 pb-[calc(12px+env(safe-area-inset-bottom))] grid gap-1.5" onSubmit={(e) => { e.preventDefault(); void submit() }}>
        {sendError && <p className="text-danger text-[13px] m-0 ml-2" role="alert">发送失败：{sendError}</p>}
        {context && (
          <span className="justify-self-start inline-flex gap-1 items-center text-[12px] text-muted bg-sunken rounded-full pt-0.5 pr-1 pb-0.5 pl-[9px] max-w-full" title="sous 会知道你在看这个">
            <Icon name={context.icon} size={12} />{context.text}
            <button
              type="button"
              aria-label="不带上这个"
              className="border-0 bg-none text-muted p-0.5 grid rounded-full hover:text-ink hover:bg-line"
              onClick={() => {
                ui.state.dropContext = true
                ui.emit()
              }}
            >
              <Icon name="x" size={11} />
            </button>
          </span>
        )}
        <div className="flex items-end gap-2 bg-panel border border-line rounded-[20px] p-[5px] pl-3.5 focus-within:border-accent">
          <textarea
            ref={input}
            value={draft}
            rows={1}
            placeholder="和 sous 说…"
            title={touch ? undefined : 'Enter 发送，Shift+Enter 换行'}
            onChange={(e) => setDraft(e.target.value)}
            onInput={resize}
            onKeyDown={onKey}
            aria-label="消息"
            className="flex-1 border-0 outline-0 resize-none bg-transparent py-[7px] max-h-[180px] leading-[1.5]"
          ></textarea>
          <button className="w-[34px] h-[34px] rounded-full border-0 flex-none bg-accent text-accent-text grid place-items-center" type="submit" disabled={!draft.trim() || sending} aria-label="发送">
            <Icon name="send" />
          </button>
        </div>
      </form>
    </section>
  )
}
