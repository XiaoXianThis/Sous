import { useEffect, useRef, useState } from 'react'
import { showProject } from '../lib/app'
import { attachmentUrl, formatSize, isImage } from '../lib/attachments'
import { splitMentions } from '../lib/mention'
import { triggerView } from '../lib/trigger'
import type { Attachment, Message } from '../lib/types'
import AgentIcon, { agentTitle } from './AgentIcon'
import Icon from './Icon'
import Markdown, { CopyButton } from './Markdown'

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

const noticeLook: Record<string, { icon: string; text: string; color: string }> = {
  notice: { icon: 'bell', text: '提醒', color: 'var(--warm)' },
  report: { icon: 'check-circle', text: '任务汇报', color: 'var(--ok)' },
  inspector: { icon: 'shield', text: '巡检', color: 'var(--info)' },
}

/** The files of a user message: pictures as thumbnails, the rest as download chips. */
function Attachments({ list }: { list: Attachment[] }) {
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
 * The model's reasoning. Open while it is being written (a short box that
 * follows the newest line), folded once the answer starts; a click overrides.
 */
function Thinking({ text, live }: { text: string; live: boolean }) {
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

export default function MessageView({ message }: { message: Message }) {
  const [showTrigger, setShowTrigger] = useState(false)
  const time = new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })

  if (message.kind === 'silent') {
    return (
      <p className="m-0 -mt-1.5 flex items-center gap-1 text-[11.5px] text-faint px-1 overflow-hidden" title={message.content || '已读，不需要回复'}>
        <Icon name="check" size={12} stroke={2.4} /><Icon name="check" size={12} stroke={2.4} className="-ml-2" />
        {message.content && <span className="overflow-hidden text-ellipsis whitespace-nowrap">{message.content}</span>}
      </p>
    )
  }

  if (message.kind === 'trigger') {
    const t = triggerView(message.content)
    const scheduled = t.kind === 'schedule'
    return (
      <div className="grid grid-cols-[minmax(0,1fr)] gap-2" data-testid="message-trigger">
        <button
          type="button"
          className="group/trig flex items-center gap-2.5 w-full min-w-0 border-0 bg-transparent p-0 text-[12px] text-muted"
          onClick={() => setShowTrigger(!showTrigger)}
          aria-expanded={showTrigger}
          title={t.title}
        >
          <span className="h-px bg-line flex-1 min-w-4"></span>
          <span className="flex items-center gap-1.5 min-w-0 max-w-[calc(100%-48px)] h-7 pl-2 pr-2.5 rounded-full bg-sunken group-hover/trig:text-ink transition-colors">
            <Icon name={scheduled ? 'calendar' : 'refresh'} size={13} className="text-warm flex-none" />
            <span className="font-medium text-warm flex-none">{scheduled ? '定时任务' : t.kind === 'followup' ? '跟进' : '系统'}</span>
            {t.title && <span className="overflow-hidden text-ellipsis whitespace-nowrap">{t.title}</span>}
            <time className="text-faint tabular-nums flex-none">{time}</time>
            <Icon name="chevron" size={11} className={`text-faint flex-none transition-transform duration-200 ${showTrigger ? '' : '-rotate-90'}`} />
          </span>
          <span className="h-px bg-line flex-1 min-w-4"></span>
        </button>
        {showTrigger && (
          <div className="selectable justify-self-center w-full max-w-[640px] whitespace-pre-wrap [overflow-wrap:anywhere] text-[13px] leading-[1.7] text-muted bg-sunken rounded-[14px] px-4 py-3 rise">
            {t.body || message.content}
          </div>
        )}
      </div>
    )
  }

  if (message.kind === 'consult') {
    // A project consultant's answer, shown as it is (decision 0022): a bar on
    // the left and who said it, so it is never mistaken for the supervisor.
    const src = message.source
    const [open, setOpen] = [showTrigger || message.content.length < 1600, setShowTrigger]
    return (
      <article className="grid gap-2 min-w-0 group border-l-[3px] border-[color-mix(in_srgb,var(--accent)_55%,transparent)] pl-3.5" data-testid="message-consult">
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
        <div className={open ? '' : 'max-h-[260px] overflow-hidden [mask-image:linear-gradient(to_bottom,black_70%,transparent)]'}>
          <Markdown text={message.content} />
        </div>
        {!open && (
          <button type="button" className="justify-self-start link text-[12.5px]" onClick={() => setOpen(true)}>展开全部</button>
        )}
      </article>
    )
  }

  const notice = message.kind ? noticeLook[message.kind] : undefined
  const user = message.role === 'user'

  if (user) {
    return (
      <article className="grid gap-1 justify-items-end group" data-testid={`message-${message.kind ?? message.role}`}>
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
          <time className="text-[11px] text-faint px-1 tabular-nums">{time}</time>
          {message.content.trim() !== '' && <CopyButton text={message.content} />}
        </div>
      </article>
    )
  }

  if (notice) {
    return (
      <article className="grid gap-2 min-w-0 group bg-panel shadow-card rounded-[16px] px-4 pt-3 pb-3.5" data-testid={`message-${message.kind}`}>
        <header className="flex items-center gap-2 min-w-0 h-6">
          <span
            className="grid place-items-center w-6 h-6 rounded-[8px] flex-none"
            style={{ color: notice.color, background: `color-mix(in srgb, ${notice.color} 14%, transparent)` }}
          >
            <Icon name={notice.icon} size={14} stroke={2} />
          </span>
          <span className="text-[13px] font-semibold" style={{ color: notice.color }}>{notice.text}</span>
          <time className="text-[11.5px] text-faint tabular-nums">{time}</time>
          <span className="flex-1"></span>
          {!message.streaming && message.content && (
            <CopyButton text={message.content} className="-my-1 -mr-1.5 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity" />
          )}
        </header>
        {message.thinking && <Thinking text={message.thinking} live={!!message.streaming && !message.content} />}
        {(message.content || message.streaming) && <Markdown text={message.content} streaming={message.streaming} />}
      </article>
    )
  }

  return (
    <article className="grid gap-2 min-w-0 group" data-testid={`message-${message.kind ?? message.role}`}>
      {message.thinking && <Thinking text={message.thinking} live={!!message.streaming && !message.content} />}
      {message.error ? (
        <div className="flex gap-2 items-start text-[13.5px] text-danger rounded-[12px] bg-[color-mix(in_srgb,var(--danger)_8%,transparent)] px-3 py-2.5">
          <Icon name="alert" size={15} className="mt-[3px] flex-none" />
          <span className="selectable whitespace-pre-wrap [overflow-wrap:anywhere]">{message.content}</span>
        </div>
      ) : (
        (message.content || message.streaming) && <Markdown text={message.content} streaming={message.streaming} />
      )}
      {!message.streaming && message.content && (
        <div className="flex items-center gap-0.5 -ml-1.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity">
          <CopyButton text={message.content} />
          <time className="text-[11px] text-faint px-1 tabular-nums">{time}</time>
        </div>
      )}
    </article>
  )
}
