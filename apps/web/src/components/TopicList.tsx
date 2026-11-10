import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { frame, useAnimate, useReducedMotion } from 'motion/react'
import { api } from '../lib/api'
import { chat, go, ui, useChat, useUi, useWb } from '../lib/app'
import { mergeHistory, visibleMessages } from '../lib/reduce'
import { taskState, taskStates } from '../lib/status'
import { ago } from '../lib/time'
import { lastLine, markSeen, messagesOf, readSeen, taskSummary, topicCards, unread, waitingIn, type TopicCardView, type TopicGroup } from '../lib/topics'
import type { Message, Task, Topic } from '../lib/types'
import Icon from './Icon'
import MessageView, { firstLine, SystemNote } from './Message'
import TalkLog from './TalkLog'

interface TopicsData {
  topics: Topic[]
  earlier: { count: number; last?: Message }
}

/** The expanded key of "更早的对话": topic ids are message ids, which never hold a colon. */
export const EARLIER = ':earlier'

const groupInfo: Record<TopicGroup, { text: string; icon: string; tone: string }> = {
  attention: { text: '要你处理', icon: 'hand', tone: 'tone-warning' },
  active: { text: '进行中', icon: 'play', tone: 'tone-accent' },
  closed: { text: '已结束', icon: 'check', tone: 'tone-neutral' },
}

/** Loads a thread's topics, again whenever one may have changed. */
function useTopics(threadId: string): [TopicsData | null, () => void] {
  const chatState = useChat()
  const [data, setData] = useState<TopicsData | null>(null)
  const [rev, setRev] = useState(0)
  useEffect(() => {
    if (!threadId) {
      setData({ topics: [], earlier: { count: 0 } })
      return
    }
    let gone = false
    const timer = setTimeout(() => {
      api.topics(threadId).then((d) => { if (!gone) setData(d) }, () => {})
    }, data ? 300 : 0)
    return () => { gone = true; clearTimeout(timer) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId, chatState.topicsRev, rev])
  useEffect(() => setData(null), [threadId])
  return [data, () => setRev((r) => r + 1)]
}

/**
 * The chat by topic (work/specs/chat-topics): what needs the user first,
 * then a card per topic, grouped by what it waits for. A card opens into
 * the topic's conversation, its tasks and their talk, and a box to reply in
 * the topic.
 *
 * An accordion: one topic (or "更早的对话") is open at a time, and opening one
 * folds the other. The open one fills the chat area: the rest of the list
 * steps aside until it folds, the card's title row stays on top and its reply
 * box at the bottom, and only the messages between them scroll (no page
 * scroll around a card scroll). The root carries data-filled then, and
 * app.css (.topic-col) lays ChatView's column out for the card to fill.
 */
export default function TopicList({ threadId, sendIn, empty, inbox = false }: {
  threadId: string
  /** Sends a reply in a topic. */
  sendIn: (topicId: string, text: string) => Promise<unknown>
  empty: ReactNode
  /** Show what needs the user (the main chat column). */
  inbox?: boolean
}) {
  const chatState = useChat()
  const uiState = useUi()
  const wbState = useWb()
  const [data, reload] = useTopics(threadId)
  // The open topic's id, EARLIER, or '' when all are folded.
  const [expandedId, setExpandedId] = useState('')
  const [showClosed, setShowClosed] = useState(false)
  // The topic a link or a new message asked for: shown even in a folded group.
  const [focus, setFocus] = useState('')
  const [seen, setSeen] = useState(readSeen)
  const msgs = chatState.messages[threadId] ?? []
  const approvals = wbState.inbox?.approvals ?? []
  const answering = chatState.answering[threadId]

  // A topic to show: a push link, or the one a message just started.
  useEffect(() => {
    const id = uiState.openTopic
    if (!id) return
    setExpandedId(id)
    setFocus(id)
    ui.state.openTopic = ''
    ui.emit()
  }, [uiState.openTopic])

  // Another thread: nothing of it is open.
  const shownThread = useRef(threadId)
  useEffect(() => {
    if (shownThread.current === threadId) return
    shownThread.current = threadId
    setExpandedId('')
  }, [threadId])

  // What is open, among what is there.
  const openId = !data || !expandedId ? ''
    : expandedId === EARLIER ? (data.earlier.count > 0 ? EARLIER : '')
      : data.topics.some((t) => t.id === expandedId) ? expandedId : ''

  useEffect(() => {
    if (!focus || !data) return
    requestAnimationFrame(() => document.querySelector(`[data-topic="${CSS.escape(focus)}"]`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }))
  }, [focus, data])

  if (!data) return <p className="m-0 text-[13px] text-faint px-1">正在读取话题…</p>
  const cards = topicCards(data.topics, msgs, approvals, answering)
  const topicIds = new Set(data.topics.map((t) => t.id))
  const groups: TopicGroup[] = ['attention', 'active', 'closed']
  const toggle = (id: string) => setExpandedId((cur) => (cur === id ? '' : id))
  // Something open: it alone fills the column (same place in the tree, so it folds back in place).
  const filled = openId !== ''
  const onSeen = (id: string, seq: number) => {
    markSeen(id, seq)
    setSeen(readSeen())
  }

  return (
    <div className={`grid grid-cols-[minmax(0,1fr)] ${filled ? 'grid-rows-[minmax(0,1fr)] h-full min-h-0' : 'gap-4'}`} data-testid="topic-list" data-filled={filled ? '' : undefined}>
      {inbox && !filled && <NeedsYou />}
      {cards.length === 0 && data.earlier.count === 0 && <div className="grid justify-items-center gap-3 text-center pt-[12dvh] px-2 rise">{empty}</div>}
      {groups.map((g) => {
        const list = cards.filter((c) => c.group === g)
        const shown = filled ? list.filter((c) => c.topic.id === openId) : list
        if (shown.length === 0) return null
        const closed = g === 'closed'
        return (
          <section key={g} className={filled ? 'grid grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] min-h-0' : 'grid grid-cols-[minmax(0,1fr)] gap-2'} aria-label={groupInfo[g].text}>
            {!filled && <h3 className="m-0 flex items-center gap-1.5 text-[12px] font-semibold text-muted px-1">
              {closed ? (
                <button type="button" className="flex items-center gap-1.5 border-0 bg-transparent p-0 text-inherit font-inherit hover:text-ink" aria-expanded={showClosed} onClick={() => setShowClosed(!showClosed)}>
                  <Icon name="chevron" size={11} className={`transition-transform duration-200 ${showClosed ? '' : '-rotate-90'}`} />
                  {groupInfo[g].text} · {list.length}
                </button>
              ) : (
                <>
                  <Icon name={groupInfo[g].icon} size={13} className={groupInfo[g].tone} />
                  {groupInfo[g].text} · {list.length}
                </>
              )}
            </h3>}
            {shown.filter((c) => !closed || showClosed || c.topic.id === focus || c.topic.id === openId).map((c) => (
              <TopicCard
                key={c.topic.id}
                card={c}
                threadId={threadId}
                msgs={msgs}
                answering={answering}
                open={c.topic.id === openId}
                onToggle={() => toggle(c.topic.id)}
                isUnread={unread(c.topic, msgs, seen)}
                onSeen={onSeen}
                sendIn={sendIn}
                topicIds={topicIds}
                onChanged={reload}
              />
            ))}
          </section>
        )
      })}
      {data.earlier.count > 0 && (!filled || openId === EARLIER) && (
        <Earlier threadId={threadId} msgs={msgs} count={data.earlier.count} open={openId === EARLIER} onToggle={() => toggle(EARLIER)} />
      )}
    </div>
  )
}

/** What needs the user now: approvals they decide and tasks waiting for them. */
function NeedsYou() {
  const wbState = useWb()
  const [open, setOpen] = useState(false)
  const approvals = (wbState.inbox?.approvals ?? []).filter((a) => a.review === 'user' && a.status === 'pending')
  const tasks = wbState.inbox?.tasks ?? []
  if (approvals.length + tasks.length === 0) return null
  const taskTitle = (id: string) => tasks.find((t) => t.id === id)?.title ?? wbState.data?.tasks.find((t) => t.id === id)?.title ?? ''
  return (
    <section className="card grid grid-cols-[minmax(0,1fr)] gap-1 px-3 py-2" aria-label="需要你处理" data-testid="needs-you">
      <h3 className="m-0">
        <button type="button" className="flex items-center gap-1.5 w-full border-0 bg-transparent p-0 px-1 h-7 text-[12.5px] font-semibold tone-warning text-left" aria-expanded={open} onClick={() => setOpen(!open)}>
          <Icon name="hand" size={14} />需要你处理 · {approvals.length + tasks.length}
          <span className="flex-1"></span>
          <Icon name="chevron" size={12} className={`text-faint transition-transform duration-200 ${open ? '' : '-rotate-90'}`} />
        </button>
      </h3>
      {open && approvals.map((a) => (
        <button key={a.id} type="button" className="flex items-center gap-2 min-w-0 border-0 bg-transparent text-left rounded-[10px] px-1.5 h-9 hover:bg-hover text-[13px]"
          onClick={() => go({ view: 'task', id: a.task_id })}>
          <span className="pill tone-warning flex-none"><Icon name="shield" size={12} />等你审批</span>
          <span className="overflow-hidden text-ellipsis whitespace-nowrap min-w-0">{a.title}</span>
          {taskTitle(a.task_id) && <span className="text-faint overflow-hidden text-ellipsis whitespace-nowrap min-w-0 flex-1">· {taskTitle(a.task_id)}</span>}
        </button>
      ))}
      {open && tasks.map((t) => (
        <button key={t.id} type="button" className="flex items-center gap-2 min-w-0 border-0 bg-transparent text-left rounded-[10px] px-1.5 h-9 hover:bg-hover text-[13px]"
          onClick={() => go({ view: 'task', id: t.id })}>
          <TaskPill task={t} />
          <span className="overflow-hidden text-ellipsis whitespace-nowrap min-w-0">{t.title}</span>
          {t.status_reason && <span className="text-faint overflow-hidden text-ellipsis whitespace-nowrap min-w-0 flex-1">· {t.status_reason}</span>}
        </button>
      ))}
    </section>
  )
}

function TaskPill({ task }: { task: Task }) {
  const s = taskStates[taskState(task)]
  return <span className={`pill tone-${s.tone} flex-none`}><Icon name={s.icon} size={12} />{s.text}</span>
}

/** Loads a topic's messages the first time its card opens. */
function useTopicMessages(threadId: string, topicId: string, open: boolean) {
  const loaded = useRef(false)
  useEffect(() => {
    if (!open || loaded.current || !threadId) return
    loaded.current = true
    api.topicMessages(threadId, topicId).then((page) => {
      mergeHistory(chat.state, threadId, page.messages)
      chat.emit()
    }, () => { loaded.current = false })
  }, [open, threadId, topicId])
}

const FOLD = { duration: 0.3, ease: [0.2, 0.85, 0.25, 1] as [number, number, number, number] }

/**
 * Folds a card open and shut with Motion: its height goes from what it showed
 * to what it shows now (0 ↔ auto for the body), clipped with overflow: hidden
 * only while it moves, and the body fades. `shown` keeps the body mounted
 * until the fold has closed. A height is not a transform, so MotionConfig's
 * reducedMotion="user" (src/main.tsx) leaves it alone: this checks
 * useReducedMotion itself and jumps when the system asks for less motion.
 */
function useFold(open: boolean) {
  const [scope, animate] = useAnimate<HTMLElement>()
  const body = useRef<HTMLDivElement>(null)
  const reduce = useReducedMotion()
  const [was, setWas] = useState(open)
  const [leaving, setLeaving] = useState(false)
  if (was !== open) {
    setWas(open)
    setLeaving(!open)
  }
  // The height the card last showed: the next fold starts there.
  const size = useRef(0)
  useEffect(() => {
    const el = scope.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => { size.current = el.getBoundingClientRect().height })
    ro.observe(el)
    return () => ro.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const run = useRef(0)
  const first = useRef(true)
  useLayoutEffect(() => {
    if (first.current) {
      first.current = false
      return
    }
    const el = scope.current
    if (!el) return
    const n = ++run.current
    const from = size.current
    el.style.height = ''
    const b = body.current
    // Open: whatever the layout gives it now. Shut: everything above the body.
    const to = open || !b ? el.getBoundingClientRect().height : b.offsetTop - (parseFloat(getComputedStyle(b).marginTop) || 0)
    const settle = () => {
      if (run.current !== n) return
      el.style.height = ''
      el.style.overflow = ''
      if (!open) setLeaving(false)
    }
    if (reduce || !from || !to || Math.abs(from - to) < 1) return settle()
    // Done: back to the layout's height (it follows resizes), after Motion's last frame is written.
    const done = () => frame.postRender(settle)
    el.style.overflow = 'hidden'
    el.style.height = `${from}px`
    // Folding back into the list: keep it where the eye is.
    if (!open) el.scrollIntoView?.({ block: 'nearest' })
    void animate(el, { height: [from, to] }, FOLD).then(done)
    if (b) {
      if (open) b.style.opacity = '0'
      void animate(b, { opacity: open ? [0, 1] : 0 }, { duration: open ? 0.22 : 0.16, ease: 'easeOut' })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  return { scope, body, shown: open || leaving }
}

/** Keeps a scrolling list at its end while it grows, if the reader was there. */
function useFollow(shown: boolean, tail: unknown) {
  const ref = useRef<HTMLDivElement>(null)
  const atEnd = useRef(false)
  const measure = () => {
    const el = ref.current
    if (el) atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }
  // A body that opens reads from the top.
  useEffect(() => { atEnd.current = false }, [shown])
  useEffect(() => {
    const el = ref.current
    if (!el) return
    // Following: stay at the end however much was added (the scroll event re-measures).
    if (atEnd.current) el.scrollTop = el.scrollHeight
    else measure()
  }, [shown, tail])
  return { ref, onScroll: measure }
}

function TopicCard({ card, threadId, msgs, answering, open, onToggle, isUnread, onSeen, sendIn, topicIds, onChanged }: {
  card: TopicCardView
  threadId: string
  msgs: Message[]
  answering: string | undefined
  open: boolean
  onToggle: () => void
  isUnread: boolean
  onSeen: (id: string, seq: number) => void
  sendIn: (topicId: string, text: string) => Promise<unknown>
  topicIds: Set<string>
  onChanged: () => void
}) {
  const t = card.topic
  const [renaming, setRenaming] = useState(false)
  const [title, setTitle] = useState(t.title)
  const [error, setError] = useState('')
  useTopicMessages(threadId, t.id, open)
  const last = lastLine(t, msgs)
  const own = visibleMessages(messagesOf(msgs, t.id, answering))
  const waiting = waitingIn(msgs, t.id)
  const streaming = own.some((m) => m.streaming)
  const fold = useFold(open)
  const tip = own[own.length - 1]
  const follow = useFollow(fold.shown, `${own.length}:${tip?.content.length ?? 0}:${tip?.thinking?.length ?? 0}:${answering === t.id}`)

  useEffect(() => {
    if (open && last) onSeen(t.id, last.seq)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, last?.seq])

  async function patch(p: { title?: string; status?: 'open' | 'closed' }) {
    setError('')
    try {
      await api.patchTopic(t.id, p)
      onChanged()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const info = groupInfo[card.group]
  return (
    // Open, the card fills the chat area: title row on top, reply box at the bottom, the rest scrolls between.
    <article ref={fold.scope} className="card relative flex flex-col min-w-0 min-h-0" data-topic={t.id} data-testid="topic-card">
      <header className="flex-none flex items-start gap-2 min-w-0 px-3.5 pt-3 pb-2.5">
        <button type="button" className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1 items-center flex-1 min-w-0 border-0 bg-transparent p-0 text-left" aria-expanded={open} onClick={onToggle}>
          <Icon name={card.busy ? 'refresh' : info.icon} size={14} className={`${card.busy ? 'spin tone-accent' : info.tone} flex-none`} />
          <span className="flex items-center gap-1.5 min-w-0">
            {renaming ? null : <span className="font-semibold text-[14px] text-ink overflow-hidden text-ellipsis whitespace-nowrap">{t.title}</span>}
            {isUnread && <span className="w-2 h-2 rounded-full bg-accent flex-none" aria-label="有新消息"></span>}
            <span className="flex-1"></span>
            <time className="text-[11.5px] text-faint tabular-nums flex-none">{ago(t.updated_at)}</time>
            <Icon name="chevron" size={12} className={`text-faint flex-none transition-transform duration-200 ${open ? '' : '-rotate-90'}`} />
          </span>
          <span></span>
          {!open && (
            <span className="grid gap-0.5 min-w-0 text-[12.5px] leading-[1.5]">
              {t.summary ? (
                <span className="text-ink overflow-hidden text-ellipsis whitespace-nowrap">{t.summary}</span>
              ) : last ? (
                <span className="text-muted overflow-hidden text-ellipsis whitespace-nowrap">{last.role === 'user' ? '你：' : ''}{firstLine(last.content)}</span>
              ) : null}
              {(t.tasks.length > 0 || card.busy) && (
                <span className="text-faint overflow-hidden text-ellipsis whitespace-nowrap">
                  {[taskSummary(t.tasks), card.busy ? (answering === t.id ? 'sous 正在处理' : '排队中') : ''].filter(Boolean).join(' · ')}
                </span>
              )}
            </span>
          )}
        </button>
        {open && !renaming && (
          <span className="flex items-center gap-0.5 -mt-1 -mr-1.5 flex-none">
            <button type="button" className="icon-btn" aria-label="改标题" data-tip="改标题" data-tip-pos="bottom" onClick={() => { setTitle(t.title); setRenaming(true) }}><Icon name="edit" size={15} /></button>
            {t.status === 'open' ? (
              <button type="button" className="icon-btn" aria-label="结束话题" data-tip="结束话题" data-tip-pos="bottom" onClick={() => void patch({ status: 'closed' })}><Icon name="archive" size={15} /></button>
            ) : (
              <button type="button" className="icon-btn" aria-label="重新打开" data-tip="重新打开" data-tip-pos="bottom" onClick={() => void patch({ status: 'open' })}><Icon name="unarchive" size={15} /></button>
            )}
          </span>
        )}
      </header>
      {renaming && (
        <form className="flex-none flex items-center gap-2 px-3.5 pb-2" onSubmit={(e) => { e.preventDefault(); setRenaming(false); if (title.trim() && title.trim() !== t.title) void patch({ title: title.trim() }) }}>
          <input autoFocus value={title} onChange={(e) => setTitle(e.target.value)} aria-label="话题标题" className="flex-1 min-w-0 h-8 rounded-[9px] border border-line-strong bg-panel px-2.5 text-[14px]"
            onKeyDown={(e) => { if (e.key === 'Escape') setRenaming(false) }} />
          <button type="submit" className="btn small">保存</button>
        </form>
      )}
      {error && <p className="flex-none m-0 px-3.5 pb-2 text-[12.5px] text-danger" role="alert">{error}</p>}
      {fold.shown && (
        <div ref={fold.body} className="flex-1 min-h-0 flex flex-col min-w-0" data-testid="topic-body">
        <div ref={follow.ref} onScroll={follow.onScroll} className="flex-1 min-h-0 overflow-y-auto overscroll-contain grid grid-cols-[minmax(0,1fr)] content-start gap-4 px-3.5 pt-0.5 pb-3 min-w-0">
          {t.summary && (
            <p className="m-0 flex gap-1.5 text-[12.5px] leading-[1.55] text-muted bg-sunken rounded-[10px] px-2.5 py-2">
              <Icon name="target" size={13} className="mt-[3px] flex-none" />
              <span className="selectable">{t.status === 'closed' ? '总结：' : '当前结论：'}{t.summary}</span>
            </p>
          )}
          {t.tasks.length > 0 && (
            <section className="grid grid-cols-[minmax(0,1fr)] gap-2" aria-label="这个话题的任务">
              {t.tasks.map((task) => (
                <div key={task.id} className="grid grid-cols-[minmax(0,1fr)] gap-1.5 rounded-[12px] bg-sunken px-3 py-2">
                  <button type="button" className="flex items-center gap-2 min-w-0 border-0 bg-transparent p-0 text-left text-[13px]" onClick={() => go({ view: 'task', id: task.id })}>
                    <TaskPill task={task} />
                    <span className="font-medium overflow-hidden text-ellipsis whitespace-nowrap min-w-0">{task.title}</span>
                    {task.status_reason && <span className="text-faint overflow-hidden text-ellipsis whitespace-nowrap min-w-0 flex-1 text-[12px]">{task.status_reason}</span>}
                  </button>
                  <TalkLog taskId={task.id} />
                </div>
              ))}
            </section>
          )}
          <div className="grid grid-cols-[minmax(0,1fr)] gap-4 min-w-0">
            {own.map((m) => (
              <TopicMessage key={m.id} message={m} waiting={waiting.some((w) => w.id === m.id)} answering={answering === t.id} attached={m.role === 'user' && m.id !== t.id && topicIds.has(m.id)} tasks={t.tasks} />
            ))}
            {answering === t.id && !streaming && (
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
        <div className="flex-none px-3.5 pt-2 pb-3 border-t border-line">
          <TopicReply onSend={(text) => sendIn(t.id, text)} />
        </div>
        </div>
      )}
    </article>
  )
}

/** A message inside a topic: reports fold to one line; the user's own show
 * whether sous has got to them, and an attached one can be taken back out. */
function TopicMessage({ message, waiting, answering, attached, tasks }: { message: Message; waiting: boolean; answering: boolean; attached: boolean; tasks: Task[] }) {
  const [undoing, setUndoing] = useState(false)
  if (message.kind === 'report') {
    // Folds to one line like every system line; the task title is cut with
    // an ellipsis instead of pushing the line out of the card.
    const task = tasks.find((t) => t.id === message.task_id)
    return <SystemNote message={message} taskTitle={task?.title} />
  }
  const human = message.role === 'user' && !message.kind
  return (
    <div className="grid grid-cols-[minmax(0,1fr)] gap-1">
      <MessageView message={message} />
      {human && (waiting || attached) && (
        <p className="m-0 flex items-center justify-end gap-2 text-[11.5px] text-faint" data-testid="message-state">
          {waiting && (message.seen || answering ? <><Icon name="check" size={12} />已看到</> : <><Icon name="clock" size={12} />排队中</>)}
          {attached && (
            <>
              <span>已从新话题并入</span>
              <button type="button" className="link" disabled={undoing} onClick={() => {
                setUndoing(true)
                api.moveMessage(message.id, message.id).catch(() => {}).finally(() => setUndoing(false))
              }}>撤销</button>
            </>
          )}
        </p>
      )}
    </div>
  )
}

/** A short box to reply inside a topic. */
function TopicReply({ onSend }: { onSend: (text: string) => Promise<unknown> }) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  async function submit() {
    const text = draft.trim()
    if (!text || sending) return
    setSending(true)
    setError('')
    try {
      await onSend(text)
      setDraft('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSending(false)
    }
  }
  function onKey(e: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      void submit()
    }
  }
  return (
    <form className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 items-end" onSubmit={(e) => { e.preventDefault(); void submit() }}>
      <textarea
        value={draft}
        rows={1}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKey}
        placeholder="在这个话题里回复…"
        aria-label="在这个话题里回复"
        className="w-full min-w-0 resize-none rounded-[12px] border border-line bg-page px-3 py-2 text-[14px] leading-[1.5] max-h-[160px] placeholder:text-faint"
      />
      <button type="submit" className="w-8 h-8 mb-0.5 rounded-full border-0 bg-ink text-page grid place-items-center disabled:bg-sunken disabled:text-faint" disabled={!draft.trim() || sending} aria-label="发送到这个话题">
        <Icon name={sending ? 'refresh' : 'send'} size={15} stroke={2.2} className={sending ? 'spin' : ''} />
      </button>
      {error && <p className="col-span-2 m-0 text-[12.5px] text-danger" role="alert">{error}</p>}
    </form>
  )
}

/** Messages from before topics, as one collapsed group. */
function Earlier({ threadId, msgs, count, open, onToggle }: { threadId: string; msgs: Message[]; count: number; open: boolean; onToggle: () => void }) {
  useTopicMessages(threadId, '', open)
  const own = visibleMessages(msgs.filter((m) => !m.topic_id && !m.streaming))
  const fold = useFold(open)
  // Open, it fills the chat area like a topic: the toggle stays on top, the messages scroll under it.
  return (
    <section ref={fold.scope} className="relative flex flex-col min-w-0 min-h-0" aria-label="更早的对话">
      <button type="button" className="flex-none flex items-center gap-1.5 border-0 bg-transparent p-0 px-1 text-[12px] font-semibold text-muted hover:text-ink w-fit" aria-expanded={open} onClick={onToggle}>
        <Icon name="chevron" size={11} className={`transition-transform duration-200 ${open ? '' : '-rotate-90'}`} />
        更早的对话 · {count} 条
      </button>
      {fold.shown && (
        <div ref={fold.body} className="mt-3 flex-1 min-h-0 overflow-y-auto overscroll-contain grid grid-cols-[minmax(0,1fr)] content-start gap-5">
          {own.map((m) => <MessageView key={m.id} message={m} />)}
        </div>
      )}
    </section>
  )
}
