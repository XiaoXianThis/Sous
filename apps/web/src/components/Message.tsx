import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import { showProject, ui, useChat, useWb } from '../lib/app'
import { attachmentUrl, formatSize, isImage } from '../lib/attachments'
import { splitMentions } from '../lib/mention'
import { renderRefs, supersededBy } from '../lib/meetings'
import { triggerView } from '../lib/trigger'
import type { ToolCall } from '../lib/runs'
import type { Attachment, Message, ToolNote } from '../lib/types'
import AgentIcon, { agentTitle } from './AgentIcon'
import Icon from './Icon'
import Markdown, { CopyButton } from './Markdown'
import NewMeetingDialog from './NewMeetingDialog'
import { ToolList } from './RunSteps'

// Turns http(s) links in plain text into parts, so PR links are clickable.
function linkify(text: string): { text: string; url?: string }[] {
  const out: { text: string; url?: string }[] = []
  let last = 0
  for (const m of text.matchAll(/https?:\/\/[^\s<>"'（）)，。]+/g)) {
    if (m.index! > last) out.push({ text: text.slice(last, m.index) })
    out.push({ text: m[0], url: m[0] })
    last = m.index! + m[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last) })
  return out
}

const noticeLook: Record<string, { icon: string; text: string }> = {
  notice: { icon: 'bell', text: '提醒' },
  report: { icon: 'check-circle', text: '任务汇报' },
  inspector: { icon: 'shield', text: '巡检' },
}

const triggerIcon: Record<string, string> = { schedule: 'calendar', followup: 'refresh', task: 'refresh', other: 'refresh' }

/** The files of a user message: pictures as thumbnails, the rest as download chips. */
export function Attachments({ list }: { list: Attachment[] }) {
  const images = list.filter((a) => isImage(a.mime))
  const files = list.filter((a) => !isImage(a.mime))
  return (
    <div className="grid gap-1.5 justify-items-end max-w-[min(85%,620px)]">
      {images.length > 0 && (
        <div className="flex gap-1.5 flex-wrap justify-end">
          {images.map((a) => (
            <a key={a.id} href={attachmentUrl(a.id)} target="_blank" rel="noreferrer" title={a.name} className="block rounded-[12px] overflow-hidden bg-sunken">
              <img src={attachmentUrl(a.id)} alt={a.name} loading="lazy" className="block max-w-[260px] max-h-[220px] min-w-12 min-h-12 object-cover max-[760px]:max-w-[200px]" />
            </a>
          ))}
        </div>
      )}
      {files.map((a) => (
        <a key={a.id} href={attachmentUrl(a.id)} download={a.name} className="flex items-center gap-2 h-11 px-3 rounded-[12px] bg-sunken text-ink no-underline max-w-full" title={a.name}>
          <Icon name="file" size={16} className="text-muted flex-none" />
          <span className="grid min-w-0 text-[12.5px] leading-tight">
            <span className="overflow-hidden text-ellipsis whitespace-nowrap">{a.name}</span>
            <span className="text-faint">{formatSize(a.size)}</span>
          </span>
        </a>
      ))}
    </div>
  )
}

/**
 * Long content folded to a fixed height with a fade and an expand toggle
 * (task reports, consultant answers, a run's output). Short content is shown
 * as it is; `follow` keeps the newest lines in view while it streams.
 */
export function Clamp({ children, max = 280, follow = false, label = '内容' }: { children: ReactNode; max?: number; follow?: boolean; label?: string }) {
  const box = useRef<HTMLDivElement>(null)
  const [over, setOver] = useState(false)
  const [open, setOpen] = useState(false)

  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const check = () => {
      setOver(el.scrollHeight > max + 24)
      if (follow && !open) el.scrollTop = el.scrollHeight
    }
    check()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(check)
    if (el.firstElementChild) ro.observe(el.firstElementChild)
    return () => ro.disconnect()
  }, [max, follow, open])

  const folded = over && !open
  return (
    <div className="grid grid-cols-[minmax(0,1fr)] gap-1.5 min-w-0">
      <div
        ref={box}
        className={`min-w-0 ${folded ? (follow ? 'overflow-hidden [mask-image:linear-gradient(to_bottom,transparent,black_18%)]' : 'overflow-hidden [mask-image:linear-gradient(to_bottom,black_72%,transparent)]') : ''}`}
        style={folded ? { maxHeight: max } : undefined}
      >
        <div className="min-w-0">{children}</div>
      </div>
      {over && (
        <button
          type="button"
          className="justify-self-start inline-flex items-center gap-1 h-7 -ml-1.5 px-1.5 rounded-[8px] border-0 bg-transparent text-[12.5px] text-muted hover:bg-hover hover:text-ink"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          aria-label={open ? `收起${label}` : `展开${label}`}
        >
          <Icon name="chevron" size={12} className={`transition-transform duration-200 ${open ? 'rotate-180' : ''}`} />
          {open ? '收起' : '展开全部'}
        </button>
      )}
    </div>
  )
}

/**
 * The model's reasoning. Open while it is being written (a short box that
 * follows the newest line), folded once the answer starts; a click overrides.
 */
export function Thinking({ text, live }: { text: string; live: boolean }) {
  const [choice, setChoice] = useState<boolean | null>(null)
  const open = choice ?? live
  const box = useRef<HTMLDivElement>(null)
  const follow = useRef(true)

  useEffect(() => {
    const el = box.current
    if (!el) return
    if (live && follow.current) el.scrollTop = el.scrollHeight
    el.dataset.scrolled = String(el.scrollTop > 2)
  }, [text, live, open])

  function onScroll() {
    const el = box.current
    if (!el) return
    follow.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
    el.dataset.scrolled = String(el.scrollTop > 2)
  }

  return (
    <div className="grid gap-2 min-w-0">
      <button
        type="button"
        className="justify-self-start inline-flex items-center gap-1.5 h-7 -ml-1 px-1.5 rounded-[8px] border-0 bg-transparent text-muted text-[13px] hover:bg-hover hover:text-ink transition-colors"
        onClick={() => setChoice(!open)}
        aria-expanded={open}
        aria-label={open ? '收起思考过程' : '查看思考过程'}
      >
        <Icon name="brain" size={14} className={live ? 'text-accent' : ''} />
        <span className={live ? 'think-shimmer' : ''}>{live ? '正在思考' : '思考过程'}</span>
        <Icon name="chevron" size={12} className={`text-faint transition-transform duration-200 ${open ? '' : '-rotate-90'}`} />
      </button>
      {open && (
        <div
          ref={box}
          onScroll={onScroll}
          className={`think-box selectable overflow-y-auto overscroll-contain border-l-2 pl-3.5 pr-1 ${live ? 'border-[color-mix(in_srgb,var(--accent)_45%,transparent)] max-h-[min(200px,32dvh)]' : 'border-line-strong max-h-[min(420px,55dvh)]'}`}
          data-testid="thinking"
        >
          <Markdown text={text} streaming={live} className="text-[13px] leading-[1.7] text-muted [&_p]:my-2 [&_strong]:text-muted" />
        </div>
      )}
    </div>
  )
}

/**
 * One folded line in the conversation: an icon, a label, an optional one-line
 * title (cut with an ellipsis, never wider than the column), the time and a
 * chevron; a click shows `children` below it. Used for the supervisor's tool
 * calls and for everything the system (not the user or the supervisor's
 * answer) puts into the conversation: triggers, notices, reports, inspections.
 */
export function FoldRow({ icon, label, title, extra, time, live = false, testid, children }: {
  icon: string
  label: ReactNode
  title?: string
  extra?: ReactNode
  time?: string
  live?: boolean
  testid?: string
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="grid grid-cols-[minmax(0,1fr)] gap-1.5 min-w-0" data-testid={testid}>
      <button
        type="button"
        className="justify-self-start inline-flex items-center gap-1.5 max-w-full min-w-0 h-7 -ml-1 px-1.5 rounded-[8px] border-0 bg-transparent text-left text-muted text-[13px] hover:bg-hover hover:text-ink transition-colors whitespace-nowrap"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        title={title || undefined}
      >
        <Icon name={icon} size={14} className={`flex-none ${live ? 'text-accent' : ''}`} />
        <span className={`flex-none ${live ? 'think-shimmer' : ''}`}>{label}</span>
        {title && <span className="min-w-0 overflow-hidden text-ellipsis">· {title}</span>}
        {extra}
        {time && <time className="flex-none text-[11.5px] text-faint tabular-nums">{time}</time>}
        <Icon name="chevron" size={12} className={`flex-none text-faint transition-transform duration-200 ${open ? '' : '-rotate-90'}`} />
      </button>
      {open && <div className="min-w-0 rise">{children}</div>}
    </div>
  )
}

/** The body of an opened system line: a quiet bar on the left, like the reasoning. */
function FoldBody({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-[minmax(0,1fr)] gap-1.5 min-w-0 border-l-2 border-line-strong pl-3.5 pr-1">{children}</div>
}

/**
 * The tools the supervisor called after a message: one folded line ("调用了
 * N 个工具"), a click lists each call as name · arguments with the status dot
 * of the Run step list. Not the Run's tools; those live in RunChip.
 */
export function SupervisorTools({ tools }: { tools: ToolNote[] }) {
  const failed = tools.filter((t) => t.error).length
  const steps: ToolCall[] = tools.map((t, i) => ({
    id: String(i),
    title: [t.name, t.args, t.error && `失败：${t.error}`].filter(Boolean).join(' · '),
    status: t.error ? 'failed' : 'completed',
    kind: '',
  }))
  return (
    <FoldRow
      icon="bolt"
      label={`调用了 ${tools.length} 个工具`}
      extra={failed > 0 && <span className="flex-none text-danger">· {failed} 个失败</span>}
      testid="supervisor-tools"
    >
      <div className="pl-1 min-w-0">
        <ToolList tools={steps} live={false} />
      </div>
    </FoldRow>
  )
}

/** The first non-empty line, without a bracketed 【head】, for a folded line. */
export function firstLine(s: string): string {
  return s.split('\n').find((l) => l.trim())?.trim().replace(/^【[^】]*】/, '').trim() ?? ''
}

/**
 * A message the system wrote into the conversation (a notice, a task report,
 * an inspection), folded to one line like the tool calls; open shows it in
 * full. `taskTitle` names the task a report is about.
 */
export function SystemNote({ message, taskTitle, time }: { message: Message; taskTitle?: string; time?: string }) {
  const look = noticeLook[message.kind ?? ''] ?? noticeLook.notice
  const live = !!message.streaming
  const head = firstLine(message.content)
  const title = [taskTitle, head].filter(Boolean).join('：')
  return (
    <FoldRow icon={look.icon} label={look.text} title={title} time={time} live={live} testid={`message-${message.kind}`}>
      <FoldBody>
        {message.thinking && <Thinking text={message.thinking} live={live && !message.content} />}
        {(message.content || live) && (
          <Clamp max={message.kind === 'report' ? 240 : 360} follow={live} label={look.text}>
            <Markdown text={message.content} streaming={live} />
          </Clamp>
        )}
        {!live && message.content && <CopyButton text={message.content} className="justify-self-start -ml-1.5" />}
      </FoldBody>
    </FoldRow>
  )
}

/** The supervisor's suggestion to hold a meeting; the user decides. */
function ProposalCard({ message, time }: { message: Message; time: string }) {
  const [open, setOpen] = useState(false)
  return (
    <article className="card flex items-center gap-2.5 px-3 py-2.5 min-w-0" data-testid="message-meeting_proposal">
      <Icon name="meeting" size={16} className="text-accent flex-none" />
      <span className="min-w-0 flex-1 text-[13px] leading-[1.5] text-ink overflow-hidden text-ellipsis whitespace-nowrap" title={message.content}>sous 建议开个会：{firstLine(message.content)}</span>
      <time className="text-[11.5px] text-faint tabular-nums flex-none">{time}</time>
      <button className="btn small px-3 flex-none" onClick={() => setOpen(true)}>开会</button>
      {open && <NewMeetingDialog parentThreadId={message.thread_id} parentTopicId={message.topic_id} initial={message.content} onClose={() => setOpen(false)} />}
    </article>
  )
}

export default function MessageView({ message }: { message: Message }) {
  const time = new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  const [expandedMinutes, setExpandedMinutes] = useState(false)
  const chat = useChat()
  const wb = useWb()

  if (message.kind === 'minutes') {
    const src = message.source
    const later = supersededBy(chat.messages[message.thread_id] ?? [], message)
    const openMeeting = () => {
      if (!src?.meeting_id) return
      ui.state.meeting = src.meeting_id
      ui.emit()
    }
    // A citation opens the meeting at that message (a link the chat handles).
    const onCite = (e: ReactMouseEvent<HTMLElement>) => {
      const href = (e.target as HTMLElement).closest('a')?.getAttribute('href') ?? ''
      if (!src?.meeting_id || !href.startsWith('#seq-')) return
      e.preventDefault()
      e.stopPropagation()
      ui.state.meeting = src.meeting_id
      ui.state.meetingSeq = Number(href.slice(5))
      ui.emit()
    }
    if (later !== null && !expandedMinutes) {
      return (
        <button className="border-0 bg-transparent text-left text-[12px] text-muted px-1 py-0.5 hover:text-ink" data-testid="message-minutes" onClick={() => setExpandedMinutes(true)}>
          会议纪要 · 第 {src?.version} 版 · 已被第 {later} 版取代
        </button>
      )
    }
    return (
      <article className="card grid grid-cols-[minmax(0,1fr)] gap-2 min-w-0 px-4 py-3 border-l-[3px] border-accent" data-testid="message-minutes" onClick={onCite}>
        <header className="flex items-center gap-2 min-w-0 h-6">
          <Icon name="note" size={15} className="text-accent flex-none" />
          <span className="text-[13px] font-semibold text-ink overflow-hidden text-ellipsis whitespace-nowrap">会议纪要 · 第 {src?.version} 版</span>
          <time className="text-[11.5px] text-faint tabular-nums flex-none">{time}</time>
          <span className="flex-1"></span>
          {src?.meeting_id && (
            <button className="link text-[12px] shrink-0" onClick={openMeeting}>查看原始记录</button>
          )}
        </header>
        <div className="min-w-0"><Markdown text={renderRefs(message.content)} /></div>
      </article>
    )
  }

  if (message.kind === 'meeting_proposal') {
    return <ProposalCard message={message} time={time} />
  }

  if (message.kind === 'silent') {
    return (
      <p className="m-0 -mt-1.5 flex items-center gap-1 text-[11.5px] text-faint px-1 overflow-hidden" title={message.content || '已读，不需要回复'}>
        <Icon name="check" size={12} stroke={2.4} /><Icon name="check" size={12} stroke={2.4} className="-ml-2" />
        {message.content && <span className="overflow-hidden text-ellipsis whitespace-nowrap">{message.content}</span>}
      </p>
    )
  }

  if (message.kind === 'trigger') {
    // Written by the hub (role user), not typed by the user: one folded line
    // like the tool calls, the full text on a click.
    const t = triggerView(message.content)
    const label = t.kind === 'schedule' ? '定时任务' : t.kind === 'followup' ? '跟进' : t.kind === 'task' ? (t.label ?? '任务') : '系统'
    return (
      <FoldRow icon={triggerIcon[t.kind]} label={label} title={t.title} time={time} testid="message-trigger">
        <FoldBody>
          <p className="selectable m-0 whitespace-pre-wrap [overflow-wrap:anywhere] text-[13px] leading-[1.7] text-muted">{t.body || message.content}</p>
        </FoldBody>
      </FoldRow>
    )
  }

  if (message.kind === 'consult') {
    // A project consultant's answer, shown as it is (decision 0022): a bar on
    // the left and who said it, so it is never mistaken for the supervisor.
    const src = message.source
    return (
      <article className="grid grid-cols-[minmax(0,1fr)] gap-2 min-w-0 group border-l-[3px] border-[color-mix(in_srgb,var(--accent)_55%,transparent)] pl-3.5" data-testid="message-consult">
        <header className="flex items-center gap-2 min-w-0 h-6">
          <AgentIcon name={src?.agent ?? ''} size={20} />
          <span className="text-[13px] font-semibold text-ink overflow-hidden text-ellipsis whitespace-nowrap">
            {agentTitle(src?.agent || 'agent')} · 顾问{src?.project ? ` · ${src.project}` : ''}
          </span>
          <time className="text-[11.5px] text-faint tabular-nums flex-none">{time}</time>
          <span className="flex-1"></span>
          {message.content && (
            <CopyButton text={message.content} className="-my-1 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity" />
          )}
        </header>
        <Clamp max={260} label="回答"><Markdown text={message.content} /></Clamp>
      </article>
    )
  }

  const notice = message.kind ? noticeLook[message.kind] : undefined
  const user = message.role === 'user'

  if (message.kind === 'dm' && !user) {
    // A task agent's answer to a direct message: the consultant's look, but
    // labelled with the agent and the task (decision 0028).
    const src = message.source
    const task = wb.data?.tasks.find((t) => t.id === (src?.task_id ?? message.task_id))
    return (
      <article className="grid grid-cols-[minmax(0,1fr)] gap-2 min-w-0 group border-l-[2px] border-line pl-3.5" data-testid="message-dm">
        <header className="flex items-center gap-2 min-w-0 h-6">
          <AgentIcon name={src?.agent ?? ''} size={18} />
          <span className="text-[13px] font-semibold text-ink overflow-hidden text-ellipsis whitespace-nowrap">
            {agentTitle(src?.agent || 'agent')} · 私聊{task ? ` · ${task.title}` : ''}
          </span>
          <time className="text-[11.5px] text-faint tabular-nums flex-none">{time}</time>
          <span className="flex-1"></span>
          {message.content && (
            <CopyButton text={message.content} className="-my-1 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity" />
          )}
        </header>
        <Clamp max={280} label="私聊"><Markdown text={message.content} /></Clamp>
      </article>
    )
  }

  if (user) {
    return (
      <article className="grid gap-1 justify-items-end group" data-testid={`message-${message.kind ?? message.role}`}>
        {message.kind === 'dm' && <span className="text-[12px] text-faint flex items-center gap-1"><Icon name="chat" size={12} />私聊 → {agentTitle(message.source?.agent || 'agent')}</span>}
        {!!message.context?.note_ids?.length && <span className="text-[12px] text-faint flex items-center gap-1"><Icon name="note" size={12} />来自笔记</span>}
        {message.attachments && message.attachments.length > 0 && <Attachments list={message.attachments} />}
        {message.content.trim() !== '' && <div className="selectable whitespace-pre-wrap [overflow-wrap:anywhere] text-[14px] leading-[1.6] bg-user-bubble text-user-text px-3.5 py-2.5 rounded-[12px] max-w-[min(85%,620px)]">
          {linkify(message.content).map((part, i) =>
            part.url ? (
              <a key={i} href={part.url} target="_blank" rel="noreferrer" className="md-link">{part.text}</a>
            ) : (
              <span key={i}>
                {splitMentions(part.text, message.context?.mentions).map((seg, j) =>
                  seg.mention ? (
                    <button
                      key={j}
                      type="button"
                      className="mention-tag"
                      title={`引用的项目：${seg.mention.name}`}
                      aria-label={`引用的项目：${seg.mention.name}，打开项目`}
                      data-project-id={seg.mention.project_id}
                      onClick={() => showProject(seg.mention!.project_id)}
                    >
                      {seg.text}
                    </button>
                  ) : (
                    seg.text
                  ),
                )}
              </span>
            ),
          )}
        </div>}
        <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity">
          {message.kind === 'dm' && message.delivered && <span className="text-[11px] text-faint px-1">已送达</span>}
          <time className="text-[11px] text-faint px-1 tabular-nums">{time}</time>
          {message.content.trim() !== '' && <CopyButton text={message.content} />}
        </div>
      </article>
    )
  }

  if (notice) return <SystemNote message={message} time={time} />


  return (
    <article className="grid grid-cols-[minmax(0,1fr)] gap-2 min-w-0 group" data-testid={`message-${message.kind ?? message.role}`}>
      {message.thinking && <Thinking text={message.thinking} live={!!message.streaming && !message.content} />}
      {message.error ? (
        <div className="flex gap-2 items-start text-[13.5px] text-danger rounded-[12px] bg-[color-mix(in_srgb,var(--danger)_8%,transparent)] px-3 py-2.5">
          <Icon name="alert" size={15} className="mt-[3px] flex-none" />
          <span className="selectable whitespace-pre-wrap [overflow-wrap:anywhere]">{message.content}</span>
        </div>
      ) : (
        (message.content || message.streaming) && <Markdown text={message.content} streaming={message.streaming} />
      )}
      {message.tools && message.tools.length > 0 && <SupervisorTools tools={message.tools} />}
      {!message.streaming && message.content && (
        <div className="flex items-center gap-0.5 -ml-1.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity">
          <CopyButton text={message.content} />
          <time className="text-[11px] text-faint px-1 tabular-nums">{time}</time>
        </div>
      )}
    </article>
  )
}
