import { useState } from 'react'
import type { Message } from '../lib/types'
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

export default function MessageView({ message }: { message: Message }) {
  const [showThinking, setShowThinking] = useState(false)
  const [showTrigger, setShowTrigger] = useState(false)

  // A trigger's first line says what fired: 【定时任务「…」】 or 【跟进】…
  const triggerHead = message.kind === 'trigger' ? message.content.split('\n')[0].replace(/^【(定时任务|跟进)?|】$/g, '') : ''
  const scheduled = message.content.startsWith('【定时任务')
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
    return (
      <div className="grid grid-cols-[minmax(0,1fr)] gap-1 justify-items-center" data-testid="message-trigger">
        <button
          className="flex items-center gap-1.5 max-w-full border-0 bg-sunken rounded-full px-2.5 py-1 text-muted text-[12px] hover:text-ink"
          onClick={() => setShowTrigger(!showTrigger)}
          aria-expanded={showTrigger}
          title={scheduled ? '定时任务' : '自动跟进'}
        >
          <Icon name={scheduled ? 'calendar' : 'refresh'} size={12} className="text-warm" />
          <span className="overflow-hidden text-ellipsis whitespace-nowrap">{triggerHead}</span>
          <time className="text-faint tabular-nums">{time}</time>
        </button>
        {showTrigger && (
          <div className="whitespace-pre-wrap [overflow-wrap:anywhere] text-[12.5px] text-muted bg-sunken rounded-[14px] px-3 py-2 justify-self-stretch">
            {message.content}
          </div>
        )}
      </div>
    )
  }

  const notice = message.kind ? noticeLook[message.kind] : undefined
  const user = message.role === 'user'

  if (user) {
    return (
      <article className="grid gap-1 justify-items-end group" data-testid={`message-${message.kind ?? message.role}`}>
        <div className="whitespace-pre-wrap [overflow-wrap:anywhere] text-[14px] leading-[1.6] bg-user-bubble text-user-text px-3.5 py-2.5 rounded-[12px] max-w-[min(85%,620px)]">
          {linkify(message.content).map((part, i) =>
            part.url ? (
              <a key={i} href={part.url} target="_blank" rel="noreferrer" className="md-link">{part.text}</a>
            ) : (
              <span key={i}>{part.text}</span>
            ),
          )}
        </div>
        <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity">
          <time className="text-[11px] text-faint px-1 tabular-nums">{time}</time>
          <CopyButton text={message.content} />
        </div>
      </article>
    )
  }

  return (
    <article
      className={`grid gap-1.5 min-w-0 group ${notice ? 'rounded-[12px] border border-line bg-panel px-3.5 py-3' : ''}`}
      data-testid={`message-${message.kind ?? message.role}`}
    >
      {notice && (
        <span className="flex items-center gap-1.5 text-[12px] font-semibold" style={{ color: notice.color }}>
          <Icon name={notice.icon} size={14} stroke={2} />{notice.text}
        </span>
      )}
      {message.thinking && (
        <div className="grid gap-1.5">
          <button
            className="toggle justify-self-start inline-flex items-center gap-1.5 border-0 bg-transparent p-0 text-muted text-[13px] hover:text-ink"
            onClick={() => setShowThinking(!showThinking)}
            aria-expanded={showThinking}
            aria-label={showThinking ? '收起思考过程' : '查看思考过程'}
          >
            <Icon name="brain" size={14} />
            <span>{message.streaming && !message.content ? '正在思考' : '思考过程'}</span>
            <Icon name="chevron" size={12} className={`transition-transform ${showThinking ? 'rotate-180' : ''}`} />
          </button>
          {showThinking && (
            <div className="whitespace-pre-wrap [overflow-wrap:anywhere] text-[13px] leading-[1.65] text-muted border-l-2 border-line py-0.5 pl-3">
              {message.thinking}
            </div>
          )}
        </div>
      )}
      {message.error ? (
        <div className="flex gap-2 items-start text-[13.5px] text-danger rounded-[12px] bg-[color-mix(in_srgb,var(--danger)_8%,transparent)] px-3 py-2.5">
          <Icon name="alert" size={15} className="mt-[3px] flex-none" />
          <span className="whitespace-pre-wrap [overflow-wrap:anywhere]">{message.content}</span>
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
