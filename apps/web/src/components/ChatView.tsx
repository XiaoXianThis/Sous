import { Fragment, useEffect, useId, useLayoutEffect, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { api } from '../lib/api'
import { ui as uiStore, useChat, useUi, useWb } from '../lib/app'
import { filesOf, formatSize, isImage, MAX_FILE, MAX_FILES, nameOf, readyIds, shrinkImage, uploading, type Pending } from '../lib/attachments'
import { usePhone } from '../lib/phone'
import { scrollAfterResize } from '../lib/gesture'
import { activeProjects, filterProjects, insertMention, keptMentions, mentionQuery, splitMentions, type MentionQuery } from '../lib/mention'
import { isAnswering, visibleMessages } from '../lib/reduce'
import type { RunView } from '../lib/runs'
import type { Mention, Message, Project, Thread } from '../lib/types'
import { ContextPanel, ContextRing, useThreadContext } from './ContextMeter'
import { dividerAt } from '../lib/context'
import ModelSwitch from './ModelSwitch'
import Icon from './Icon'
import MessageView from './Message'
import RunChip from './RunChip'
import TopicList from './TopicList'

type Item = { kind: 'message'; seq: number; message: Message } | { kind: 'run'; seq: number; run: RunView }

const statusText = { connecting: '连接中', online: '在线', offline: '离线，正在重连' }

type ChatMode = 'topics' | 'timeline'
const MODE_KEY = 'sous.chatView'

function storedMode(): ChatMode {
  try {
    return localStorage.getItem(MODE_KEY) === 'timeline' ? 'timeline' : 'topics'
  } catch {
    return 'topics'
  }
}

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
export default function ChatView({ threadId, threads, title, avatar, threadLabel, threadIcon, onPick, onNew, onSend, onSendIn, inbox = false, context, onDropContext, placeholder, empty, actions, leading, label }: {
  /** The shown thread; '' before a project chat's first message. */
  threadId: string
  threads: Thread[]
  title: string
  avatar: ReactNode
  threadLabel: (t: Thread) => string
  threadIcon: (t: Thread) => string
  onPick: (id: string) => void | Promise<void>
  onNew: () => void | Promise<unknown>
  /** Sends from the main input; may return the topic the message started. */
  onSend: (text: string, attachments: string[], mentions: Mention[]) => Promise<unknown>
  /** Replies inside a topic; without it the chat has no topic view. */
  onSendIn?: (topicId: string, text: string) => Promise<unknown>
  /** The topic view starts with what needs the user. */
  inbox?: boolean
  context?: ChatContext | null
  onDropContext?: () => void
  placeholder: string
  empty: ReactNode
  actions?: ReactNode
  /** Drawn before the chat picker, e.g. a phone back button. */
  leading?: ReactNode
  label: string
}) {
  const chatState = useChat()
  const uiState = useUi()
  const wbState = useWb()
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState('')
  const [picking, setPicking] = useState(false)
  const [pending, setPending] = useState<Pending[]>([])
  const [dragging, setDragging] = useState(false)
  const [typing, setTyping] = useState(false)
  const prevHeight = useRef<number | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  const stick = useRef(true)
  const phone = usePhone()
  const section = useRef<HTMLElement>(null)
  const composer = useRef<HTMLFormElement>(null)
  const [ctxView, reloadContext] = useThreadContext(threadId)
  const [contextOpen, setContextOpen] = useState(false)
  // @ references: the projects picked so far, the query at the caret and the highlighted candidate.
  const [picked, setPicked] = useState<Mention[]>([])
  const [mq, setMq] = useState<MentionQuery | null>(null)
  const [active, setActive] = useState(0)
  const [dismissed, setDismissed] = useState(-1)
  const nextCaret = useRef<number | null>(null)
  const layer = useRef<HTMLDivElement>(null)
  const listId = useId()
  const [mode, setModeState] = useState<ChatMode>(storedMode)
  const topics = !!onSendIn && mode === 'topics'
  function setMode(m: ChatMode) {
    stick.current = true // the timeline opens at its end
    setModeState(m)
    try {
      localStorage.setItem(MODE_KEY, m)
    } catch {
      /* a convenience only */
    }
  }

  // Phone: the composer floats over the timeline, which scrolls under it; keep its height as padding.
  useEffect(() => {
    const el = composer.current
    if (!phone || !el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => {
      section.current?.style.setProperty('--composer-h', `${el.offsetHeight}px`)
      if (stick.current && scroller.current) scroller.current.scrollTo({ top: scroller.current.scrollHeight })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [phone])

  useEffect(() => {
    const el = scroller.current
    if (!phone || !el || typeof ResizeObserver === 'undefined') return
    prevHeight.current = null
    const ro = new ResizeObserver(() => {
      const height = el.clientHeight
      if (prevHeight.current !== null && prevHeight.current !== height) {
        el.scrollTop = scrollAfterResize({ prevHeight: prevHeight.current, height, scrollTop: el.scrollTop, scrollHeight: el.scrollHeight, stick: stick.current })
      }
      prevHeight.current = height
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [phone])

  const nodeName = (id: string) => wbState.data?.nodes.find((n) => n.id === id)?.name ?? '机器'
  const messages = chatState.messages[threadId] ?? []
  const answering = threadId !== '' && isAnswering(chatState, threadId)

  const timeline: Item[] = visibleMessages(messages).map((m) => ({ kind: 'message', seq: m.seq, message: m }))
  for (const r of Object.values(chatState.runs)) {
    if (threadId && r.run.thread_id === threadId) timeline.push({ kind: 'run', seq: r.seq || r.run.created_at, run: r })
  }
  timeline.sort((a, b) => a.seq - b.seq)
  const divider = ctxView ? dividerAt(timeline.map((i) => i.seq), ctxView.covers_up_to) : -1
  const last = messages[messages.length - 1]
  const lastLength =
    (last ? last.content.length + (last.thinking?.length ?? 0) : 0) +
    Object.values(chatState.runs).reduce((n, r) => n + r.approvals.length + r.run.status.length, 0)

  useEffect(() => {
    // The topic list reads from the top; only the timeline follows the end.
    if (topics) return
    if (stick.current && scroller.current) scroller.current.scrollTo({ top: scroller.current.scrollHeight })
  }, [topics, threadId, messages.length, lastLength, uiState.tab, uiState.chatOpen, answering])

  useLayoutEffect(() => {
    resize()
    const el = input.current
    if (el && nextCaret.current !== null) {
      el.focus()
      el.setSelectionRange(nextCaret.current, nextCaret.current)
      nextCaret.current = null
      syncLayer()
    }
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

  // Files are uploaded as soon as they are added; the message refers to them by id.
  function addFiles(files: File[]) {
    if (files.length === 0) return
    const room = MAX_FILES - pending.length
    if (files.length > room) setSendError(`一条消息最多 ${MAX_FILES} 个附件`)
    else setSendError('')
    for (const original of files.slice(0, Math.max(room, 0))) {
      const key = `${Date.now()}-${Math.random().toString(36).slice(2)}`
      const name = nameOf(original)
      if (original.size > MAX_FILE) {
        setPending((l) => [...l, { key, name, size: original.size, status: 'error', error: '超过 20 MB' }])
        continue
      }
      const preview = isImage(original.type) ? URL.createObjectURL(original) : undefined
      setPending((l) => [...l, { key, name, size: original.size, preview, status: 'uploading' }])
      void (async () => {
        try {
          const file = await shrinkImage(original)
          const attachment = await api.upload(file, file === original ? name : file.name)
          setPending((l) => l.map((p) => (p.key === key ? { ...p, status: 'ready', attachment, size: attachment.size } : p)))
        } catch (e) {
          setPending((l) => l.map((p) => (p.key === key ? { ...p, status: 'error', error: (e as Error).message } : p)))
        }
      })()
    }
  }

  function removeFile(key: string) {
    setPending((l) => {
      const gone = l.find((p) => p.key === key)
      if (gone?.preview) URL.revokeObjectURL(gone.preview)
      return l.filter((p) => p.key !== key)
    })
  }

  function onPaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    const files = filesOf(e.clipboardData, { paste: true })
    if (files.length === 0) return
    e.preventDefault()
    addFiles(files)
  }

  function onDrop(e: DragEvent) {
    setDragging(false)
    const files = filesOf(e.dataTransfer)
    if (files.length === 0) return
    e.preventDefault()
    addFiles(files)
  }

  const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files')
  const mentions = keptMentions(draft, picked)
  const projects = activeProjects(wbState.data?.projects)
  const mentionOpen = mq !== null && mq.start !== dismissed
  const candidates = mentionOpen ? filterProjects(projects, mq.query) : []
  const highlighted = Math.min(active, Math.max(candidates.length - 1, 0))

  // Looks for an @query at the caret after every edit or caret move.
  function syncMention() {
    const el = input.current
    if (!el) return
    const q = el.selectionStart === el.selectionEnd ? mentionQuery(el.value, el.selectionStart) : null
    if (q?.start !== mq?.start || q?.query !== mq?.query) setActive(0)
    if (!q) setDismissed(-1)
    setMq(q)
  }

  function chooseMention(p: Project) {
    const el = input.current
    if (!el || !mq) return
    const r = insertMention(el.value, mq, el.selectionStart, p.name)
    nextCaret.current = r.caret
    setDraft(r.text)
    setPicked((l) => [...l.filter((m) => m.project_id !== p.id), { project_id: p.id, name: p.name }])
    setMq(null)
  }

  function syncLayer() {
    if (layer.current && input.current) layer.current.scrollTop = input.current.scrollTop
  }

  const ids = readyIds(pending)
  const canSend = (draft.trim() !== '' || ids.length > 0) && !uploading(pending) && !sending

  async function submit() {
    const text = draft.trim()
    if ((!text && ids.length === 0) || uploading(pending) || sending) return
    setSending(true)
    setSendError('')
    try {
      const topic = await onSend(text, ids, mentions)
      if (typeof topic === 'string' && topic && topics) {
        // Bring the topic the message started into view, folded: opening it is the user's click.
        uiStore.state.revealTopic = topic
        uiStore.emit()
      }
      setDraft('')
      setPicked([])
      setMq(null)
      for (const p of pending) if (p.preview) URL.revokeObjectURL(p.preview)
      setPending([])
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
    const composing = e.nativeEvent.isComposing || e.keyCode === 229
    if (mentionOpen && !composing) {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        setDismissed(mq.start)
        return
      }
      if (candidates.length > 0) {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault()
          const step = e.key === 'ArrowDown' ? 1 : -1
          setActive((highlighted + step + candidates.length) % candidates.length)
          return
        }
        if ((e.key === 'Enter' && !e.shiftKey) || (e.key === 'Tab' && !e.shiftKey)) {
          e.preventDefault()
          chooseMention(candidates[highlighted])
          return
        }
      }
    }
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
    <section ref={section} className={`flex flex-col min-h-0 h-full min-w-0 ${phone ? 'chat-phone' : ''}`} aria-label={label} data-typing={phone && typing ? '' : undefined}>
      <header className="chat-head flex items-center gap-1 pt-[calc(10px+var(--safe-t))] pr-2.5 pb-2 pl-3 border-b border-line">
        <div className="flex items-center gap-2 min-w-0">
        {leading}
        <div className="relative picker min-w-0">
          <button
            className={phone ? 'glass-pill glass max-w-full' : 'flex items-center gap-2 min-w-0 border-0 bg-transparent font-semibold pl-1 pr-2 h-9 rounded-[10px] hover:bg-hover'}
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
            <div className="absolute top-full left-0 z-[25] mt-1 w-[272px] max-h-[55dvh] overflow-y-auto bg-raised rounded-[12px] p-1 grid gap-0.5 shadow-pop rise" role="listbox" aria-label="对话">
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
        </div>
        {phone ? <ModelSwitch placement="header" /> : <span className="flex-1"></span>}
        <div className={phone ? 'glass-group glass gap-0.5 px-1.5 min-w-0' : 'contents'}>
          {onSendIn && (
            <button className="icon-btn" aria-label={topics ? '按时间看' : '按话题看'} data-tip={topics ? '按时间看' : '按话题看'} data-tip-pos="bottom"
              aria-pressed={!topics} onClick={() => setMode(topics ? 'timeline' : 'topics')}>
              <Icon name={topics ? 'list' : 'layers'} size={17} />
            </button>
          )}
          <button className="icon-btn" aria-label="新对话" data-tip="新对话" data-tip-pos="bottom" onClick={() => void create()}><Icon name="edit" size={17} /></button>
          {actions}
        </div>
      </header>

      <div className="chat-scroll flex-1 min-w-0 overflow-y-auto overflow-x-hidden overscroll-contain" ref={scroller} onScroll={onScroll}>
        {topics && onSendIn ? (
          // .topic-col: with a topic open (TopicList's data-filled) app.css makes the column a flex box the
          // card fills, so only the card's messages scroll; all folded, the list flows and scrolls here.
          <div className="topic-col mx-auto max-w-[820px] px-3 pt-3 pb-4 grid grid-cols-[minmax(0,1fr)]">
            <TopicList threadId={threadId} sendIn={onSendIn} empty={empty} inbox={inbox} />
          </div>
        ) : (
        <div className="mx-auto max-w-[820px] px-4 pt-4 pb-4 grid grid-cols-[minmax(0,1fr)] gap-5">
          {timeline.length === 0 && !answering && <div className="grid justify-items-center gap-3 text-center pt-[14dvh] px-2 rise">{empty}</div>}
          {timeline.map((item, i) => (
            <Fragment key={item.kind === 'message' ? item.message.id : item.run.run.id}>
              {i === divider && (
                <button
                  className="flex items-center gap-3 border-0 bg-transparent text-[12px] text-faint hover:text-muted p-0 w-full"
                  onClick={() => setContextOpen(true)}
                >
                  <span className="h-px bg-line flex-1"></span>
                  <Icon name="layers" size={13} />以上内容已整理进摘要
                  <span className="h-px bg-line flex-1"></span>
                </button>
              )}
              {item.kind === 'message' ? (
              <MessageView message={item.message} />
            ) : (
              <RunChip view={item.run} nodeName={nodeName} />
            )}
            </Fragment>
          ))}
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
        )}
      </div>

      <form ref={composer} className="chat-composer px-3 pt-1 pb-[calc(12px+var(--safe-b))] grid gap-1.5 mx-auto w-full max-w-[852px]" onSubmit={(e) => { e.preventDefault(); void submit() }}>
        {phone && typing && <button type="button" className="kb-hide glass-btn glass" aria-label="收起键盘"
          onPointerDown={(e) => e.preventDefault()} onClick={() => input.current?.blur()}><Icon name="chevron" size={18} /></button>}
        {sendError && <p className="text-danger text-[12.5px] m-0 ml-2 flex items-center gap-1" role="alert"><Icon name="alert" size={13} />{sendError}</p>}
        <div
          className={phone
            ? 'composer-box glass relative grid gap-1 rounded-[26px] px-3.5 pt-2 pb-2 cursor-text'
            : 'relative grid grid-cols-[minmax(0,1fr)_auto] gap-1 bg-panel rounded-[16px] border border-line-strong px-3 pt-2.5 pb-2 shadow-[0_1px_2px_rgb(0_0_0/.04)] focus-within:border-[color-mix(in_srgb,var(--text)_28%,transparent)] transition-colors cursor-text'}
          onClick={(e) => { if (!(e.target as HTMLElement).closest('button')) input.current?.focus() }}
          onDragOver={(e) => { if (hasFiles(e)) { e.preventDefault(); setDragging(true) } }}
          onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false) }}
          onDrop={onDrop}
        >
          {dragging && (
            <div className="absolute inset-0 z-10 grid place-items-center rounded-[inherit] border-2 border-dashed border-accent bg-[color-mix(in_srgb,var(--accent)_8%,var(--panel))] text-[13px] text-accent pointer-events-none">
              松开以添加图片或文件
            </div>
          )}
          {pending.length > 0 && (
            <ul className="composer-tray col-span-2 m-0 p-0 list-none flex gap-2 flex-wrap pt-0.5" aria-label="附件">
              {pending.map((p) => (
                <li key={p.key} className="relative group/att" title={p.error ? `${p.name}：${p.error}` : `${p.name}（${formatSize(p.size)}）`}>
                  {p.preview ? (
                    <img src={p.preview} alt={p.name} className={`block w-14 h-14 rounded-[10px] object-cover bg-sunken ${p.status === 'error' ? 'opacity-40' : ''}`} />
                  ) : (
                    <span className={`flex items-center gap-1.5 h-14 max-w-[190px] px-2.5 rounded-[10px] bg-sunken text-[12px] ${p.status === 'error' ? 'text-danger' : 'text-muted'}`}>
                      <Icon name="file" size={16} className="flex-none" />
                      <span className="grid min-w-0">
                        <span className="overflow-hidden text-ellipsis whitespace-nowrap text-ink">{p.name}</span>
                        <span className="text-faint">{p.error ?? formatSize(p.size)}</span>
                      </span>
                    </span>
                  )}
                  {p.status === 'uploading' && (
                    <span className="absolute inset-0 grid place-items-center rounded-[10px] bg-[color-mix(in_srgb,var(--panel)_55%,transparent)]" aria-label="正在上传">
                      <Icon name="refresh" size={16} className="spin text-muted" />
                    </span>
                  )}
                  {p.status === 'error' && p.preview && (
                    <span className="absolute inset-0 grid place-items-center text-danger" aria-label={p.error}><Icon name="alert" size={16} /></span>
                  )}
                  <button
                    type="button"
                    aria-label={`移除 ${p.name}`}
                    className="absolute -top-1.5 -right-1.5 w-[18px] h-[18px] rounded-full border-0 grid place-items-center bg-ink text-page shadow-soft"
                    onClick={() => removeFile(p.key)}
                  >
                    <Icon name="x" size={10} stroke={3} />
                  </button>
                </li>
              ))}
            </ul>
          )}
          {mentionOpen && (
            <div className="mention-menu absolute bottom-full left-0 z-[30] mb-2 w-[min(320px,100%)] max-h-[min(300px,40dvh)] overflow-y-auto bg-raised rounded-[12px] p-1 grid gap-0.5 shadow-pop rise">
              <p className="m-0 px-2 pt-1 pb-0.5 text-[11.5px] text-faint">引用项目</p>
              {candidates.length === 0 ? (
                <p className="m-0 px-2 py-2 text-[12.5px] text-muted" role="status">
                  {projects.length === 0 ? '没有可引用的项目' : `没有匹配“${mq.query}”的项目`}
                </p>
              ) : (
                <div id={listId} role="listbox" aria-label="引用项目" className="grid gap-0.5">
                  {candidates.map((p, i) => (
                    <button
                      key={p.id}
                      id={`${listId}-${i}`}
                      type="button"
                      role="option"
                      tabIndex={-1}
                      aria-selected={i === highlighted}
                      className={`border-0 text-left px-2 h-8 rounded-[8px] flex items-center text-[13px] min-w-0 ${i === highlighted ? 'bg-hover text-ink' : 'bg-transparent text-ink'}`}
                      onMouseDown={(e) => e.preventDefault()}
                      onMouseEnter={() => setActive(i)}
                      onClick={() => chooseMention(p)}
                    >
                      <span className="overflow-hidden text-ellipsis whitespace-nowrap">{p.name}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
          <div className="composer-input relative col-span-2 min-w-0">
            {mentions.length > 0 && (
              <div ref={layer} className="mention-layer" aria-hidden="true">
                {splitMentions(draft, mentions).map((seg, i) => (seg.mention ? <mark key={i}>{seg.text}</mark> : <span key={i}>{seg.text}</span>))}
                {'\n'}
              </div>
            )}
            <textarea
              ref={input}
              value={draft}
              rows={1}
              placeholder={topics ? `${placeholder.replace(/…$/, '')}（开一个新话题）…` : placeholder}
              title={touch ? undefined : 'Enter 发送，Shift+Enter 换行，@ 引用项目'}
              onChange={(e) => { setDraft(e.target.value); syncMention() }}
              onSelect={syncMention}
              onFocus={() => {
                setTyping(true)
                if (phone && stick.current) requestAnimationFrame(() => {
                  if (scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight
                })
              }}
              onBlur={() => { setTyping(false); setMq(null) }}
              onScroll={syncLayer}
              onInput={resize}
              onKeyDown={onKey}
              onPaste={onPaste}
              aria-label="消息"
              aria-autocomplete="list"
              aria-expanded={mentionOpen && candidates.length > 0}
              aria-controls={mentionOpen && candidates.length > 0 ? listId : undefined}
              aria-activedescendant={mentionOpen && candidates.length > 0 ? `${listId}-${highlighted}` : undefined}
              className="relative w-full border-0 outline-0 resize-none bg-transparent px-0.5 py-0.5 min-h-[44px] max-h-[220px] max-[760px]:min-h-[36px] max-[760px]:max-h-[160px] leading-[1.55] text-[14px] !shadow-none placeholder:text-faint"
            ></textarea>
          </div>
          <div className="composer-tools flex items-center gap-1 min-w-0">
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              onChange={(e) => { addFiles(Array.from(e.currentTarget.files ?? [])); e.currentTarget.value = '' }}
            />
            <button
              type="button"
              className="icon-btn flex-none"
              aria-label="添加图片或文件"
              data-tip="添加图片或文件（也可以直接粘贴、拖进来）"
              onClick={() => fileInput.current?.click()}
            >
              <Icon name="paperclip" size={17} />
            </button>
            {!phone && <ModelSwitch />}
            <span className="w-1 flex-none"></span>
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
            ) : null}
            <span className="flex-1"></span>
            {/* Last in the tools row, so it sits right next to the send button: on desktop after the flex-1
                spacer; on phone (spacer hidden) ml-auto pushes it to the end of the stretched tools cell. The
                ring is a type-less button, i.e. a submit inside this form — preventDefault stops that. */}
            {ctxView && ctxView.segment > 0 && (
              <span className="composer-ring flex-none ml-auto grid" onClick={(e) => e.preventDefault()}>
                <ContextRing view={ctxView} onOpen={() => setContextOpen(true)} />
              </span>
            )}
          </div>
            <button
              className="composer-send self-end w-8 h-8 rounded-full border-0 flex-none bg-ink text-page grid place-items-center transition-transform active:scale-95 disabled:bg-sunken disabled:text-faint disabled:opacity-100 max-[760px]:w-9 max-[760px]:h-9 max-[760px]:bg-accent max-[760px]:text-accent-text"
              type="submit"
              disabled={!canSend}
              aria-label="发送"
            >
              <Icon name={sending ? 'refresh' : 'send'} size={16} stroke={2.2} className={sending ? 'spin' : ''} />
            </button>
        </div>
      </form>
      {contextOpen && ctxView && (
        <ContextPanel threadId={threadId} view={ctxView} onClose={() => setContextOpen(false)} onChanged={reloadContext} />
      )}
    </section>
  )
}
