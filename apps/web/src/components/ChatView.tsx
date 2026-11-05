import { Fragment, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { useChat, useUi, useWb } from '../lib/app'
import { isAnswering, visibleMessages } from '../lib/reduce'
import type { RunView } from '../lib/runs'
import type { Message, Thread } from '../lib/types'
import ApprovalCard from './ApprovalCard'
import ModelSwitch from './ModelSwitch'
import Icon from './Icon'
import MessageView from './Message'
import RunChip from './RunChip'

type Item = { kind: 'message'; seq: number; message: Message } | { kind: 'run'; seq: number; run: RunView }

const statusText = { connecting: '连接中', online: '在线', offline: '离线，正在重连' }

export interface ChatContext {
  icon: string
  text: string
}

/**
 * One conversation with sous: a header with a chat picker, the timeline
 * (messages, Run chips, approvals) and the composer. The main chat column and
 * the project chats share it; they differ in which threads they list and how
 * a message is sent.
 */
export default function ChatView({ threadId, threads, title, avatar, threadLabel, threadIcon, onPick, onNew, onSend, context, onDropContext, placeholder, empty, actions, label }: {
  /** The shown thread; '' before a project chat's first message. */
  threadId: string
  threads: Thread[]
  title: string
  avatar: ReactNode
  threadLabel: (t: Thread) => string
  threadIcon: (t: Thread) => string
  onPick: (id: string) => void | Promise<void>
  onNew: () => void | Promise<unknown>
  onSend: (text: string) => Promise<void>
  context?: ChatContext | null
  onDropContext?: () => void
  placeholder: string
  empty: ReactNode
  actions?: ReactNode
  label: string
}) {
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
  const messages = chatState.messages[threadId] ?? []
  const answering = threadId !== '' && isAnswering(chatState, threadId)

  const timeline: Item[] = visibleMessages(messages).map((m) => ({ kind: 'message', seq: m.seq, message: m }))
  for (const r of Object.values(chatState.runs)) {
    if (threadId && r.run.thread_id === threadId) timeline.push({ kind: 'run', seq: r.seq || r.run.created_at, run: r })
  }
  timeline.sort((a, b) => a.seq - b.seq)
  const last = messages[messages.length - 1]
  const lastLength =
    (last ? last.content.length + (last.thinking?.length ?? 0) : 0) +
    Object.values(chatState.runs).reduce((n, r) => n + r.approvals.length + r.run.status.length, 0)

  useEffect(() => {
    if (stick.current && scroller.current) scroller.current.scrollTo({ top: scroller.current.scrollHeight })
  }, [threadId, messages.length, lastLength, uiState.tab, uiState.chatOpen, answering])

  useEffect(() => {
    resize()
  }, [draft])

  useEffect(() => {
    if (!picking) return
    const onClick = (e: MouseEvent) => {
      if (!(e.target as HTMLElement).closest('.picker')) setPicking(false)
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
    await onPick(id)
  }

  async function create() {
    setPicking(false)
    await onNew()
    input.current?.focus()
  }

  async function submit() {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true)
    setSendError('')
    try {
      await onSend(text)
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
    input.current.style.height = Math.min(input.current.scrollHeight, 220) + 'px'
  }

  const dot = uiState.status === 'online' ? 'bg-ok' : uiState.status === 'offline' ? 'bg-danger' : 'bg-faint'

  return (
    <section className="flex flex-col min-h-0 h-full min-w-0" aria-label={label}>
      <header className="flex items-center gap-1 pt-[calc(10px+env(safe-area-inset-top))] pr-2.5 pb-2 pl-3 border-b border-line">
        <div className="relative picker min-w-0">
          <button
            className="flex items-center gap-2 min-w-0 border-0 bg-transparent font-semibold pl-1 pr-2 h-9 rounded-[10px] hover:bg-hover"
            onClick={() => setPicking(!picking)}
            aria-expanded={picking}
            aria-haspopup="listbox"
          >
            <span className="relative grid place-items-center flex-none">
              {avatar}
              <span className={`absolute -bottom-0.5 -right-0.5 w-2.5 h-2.5 rounded-full shadow-[0_0_0_2px_var(--chrome)] ${dot}`} title={statusText[uiState.status]}></span>
            </span>
            <span className="overflow-hidden text-ellipsis whitespace-nowrap text-[14px]">{title}</span>
            <Icon name="chevron" size={12} className="text-faint flex-none" />
          </button>
          {picking && (
            <div className="absolute top-full left-0 z-[25] mt-1 w-[272px] max-h-[55vh] overflow-y-auto bg-raised rounded-[12px] p-1 grid gap-0.5 shadow-pop rise" role="listbox" aria-label="对话">
              {threads.length === 0 && <p className="m-0 px-2.5 py-2 text-[12.5px] text-muted">还没有对话，发一条消息就会开始</p>}
              {threads.map((t) => (
                <button
                  key={t.id}
                  role="option"
                  aria-selected={t.id === threadId}
                  className={`border-0 text-left px-2 h-8 rounded-[8px] flex gap-2 items-center text-[13px] min-w-0 ${t.id === threadId ? 'bg-hover font-medium' : 'bg-transparent hover:bg-hover'}`}
                  onClick={() => void pick(t.id)}
                >
                  <Icon name={threadIcon(t)} size={14} className="text-muted flex-none" />
                  <span className="overflow-hidden text-ellipsis whitespace-nowrap flex-1">{threadLabel(t)}</span>
                  {t.id === threadId && <Icon name="check" size={14} className="text-accent flex-none" stroke={2.4} />}
                </button>
              ))}
            </div>
          )}
        </div>
        <span className="flex-1"></span>
        <ModelSwitch />
        <button className="icon-btn" aria-label="新对话" data-tip="新对话" data-tip-pos="bottom" onClick={() => void create()}><Icon name="edit" size={17} /></button>
        {actions}
      </header>

      <div className="flex-1 min-w-0 overflow-y-auto overflow-x-hidden overscroll-contain" ref={scroller} onScroll={onScroll}>
        <div className="mx-auto max-w-[820px] px-4 pt-4 pb-4 grid grid-cols-[minmax(0,1fr)] gap-5">
          {timeline.length === 0 && !answering && <div className="grid justify-items-center gap-3 text-center pt-[14vh] px-2 rise">{empty}</div>}
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
          )}
          {answering && !messages.some((m) => m.streaming) && (
            <div className="flex items-center gap-2 text-[13px] text-muted h-6" aria-label="正在思考">
              <span className="flex gap-1">
                <span className="typing-dot w-1.5 h-1.5 rounded-full bg-muted"></span>
                <span className="typing-dot w-1.5 h-1.5 rounded-full bg-muted"></span>
                <span className="typing-dot w-1.5 h-1.5 rounded-full bg-muted"></span>
              </span>
              正在思考
            </div>
          )}
        </div>
      </div>

      <form className="chat-composer px-3 pt-1 pb-[calc(12px+env(safe-area-inset-bottom))] grid gap-1.5 mx-auto w-full max-w-[852px]" onSubmit={(e) => { e.preventDefault(); void submit() }}>
        {sendError && <p className="text-danger text-[12.5px] m-0 ml-2 flex items-center gap-1" role="alert"><Icon name="alert" size={13} />{sendError}</p>}
        <div
          className="grid gap-1 bg-panel rounded-[16px] border border-line-strong px-3 pt-2.5 pb-2 shadow-[0_1px_2px_rgb(0_0_0/.04)] focus-within:border-[color-mix(in_srgb,var(--text)_28%,transparent)] transition-colors cursor-text"
          onClick={(e) => { if (!(e.target as HTMLElement).closest('button')) input.current?.focus() }}
        >
          <textarea
            ref={input}
            value={draft}
            rows={1}
            placeholder={placeholder}
            title={touch ? undefined : 'Enter 发送，Shift+Enter 换行'}
            onChange={(e) => setDraft(e.target.value)}
            onInput={resize}
            onKeyDown={onKey}
            aria-label="消息"
            className="w-full border-0 outline-0 resize-none bg-transparent px-0.5 py-0.5 min-h-[44px] max-h-[220px] leading-[1.55] text-[14px] !shadow-none placeholder:text-faint"
          ></textarea>
          <div className="flex items-center gap-2 min-w-0">
            {context ? (
              <span className="inline-flex gap-1 items-center text-[12px] text-muted bg-sunken rounded-[8px] h-7 pl-2 pr-0.5 max-w-full min-w-0" title="sous 会知道你在看这个">
                <Icon name={context.icon} size={13} className="flex-none" /><span className="overflow-hidden text-ellipsis whitespace-nowrap">{context.text}</span>
                {onDropContext && (
                  <button
                    type="button"
                    aria-label="不带上这个"
                    className="border-0 bg-transparent text-current p-1 grid rounded-[6px] hover:bg-hover hover:text-ink"
                    onClick={onDropContext}
                  >
                    <Icon name="x" size={12} stroke={2.4} />
                  </button>
                )}
              </span>
            ) : (
              <span className="text-[11.5px] text-faint max-[760px]:hidden">{touch ? '' : 'Enter 发送 · Shift+Enter 换行'}</span>
            )}
            <span className="flex-1"></span>
            <button
              className="w-8 h-8 rounded-full border-0 flex-none bg-ink text-page grid place-items-center transition-transform active:scale-95 disabled:bg-sunken disabled:text-faint disabled:opacity-100"
              type="submit"
              disabled={!draft.trim() || sending}
              aria-label="发送"
            >
              <Icon name={sending ? 'refresh' : 'send'} size={16} stroke={2.2} className={sending ? 'spin' : ''} />
            </button>
          </div>
        </div>
      </form>
    </section>
  )
}
