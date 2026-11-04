import { useState } from 'react'
import type { Message } from '../lib/types'

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

export default function MessageView({ message }: { message: Message }) {
  const [showThinking, setShowThinking] = useState(false)
  const [showTrigger, setShowTrigger] = useState(false)

  // A trigger's first line says what fired: 【定时任务「…」】 or 【跟进】…
  const triggerHead = message.kind === 'trigger' ? message.content.split('\n')[0].replace(/^【(定时任务|跟进)?|】$/g, '') : ''
  const triggerLabel = message.content.startsWith('【定时任务') ? '定时任务' : '自动跟进'
  const time = new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })

  if (message.kind === 'silent') {
    return (
      <p className="mt-[-6px] mb-0 mx-0 text-[12px] text-muted px-1 overflow-hidden text-ellipsis whitespace-nowrap" title={message.content || undefined}>
        已读，不需要回复{message.content ? `：${message.content}` : ''}
      </p>
    )
  }

  if (message.kind === 'trigger') {
    return (
      <div className="grid grid-cols-[minmax(0,1fr)] gap-1 justify-items-center" data-testid="message-trigger">
        <button
          className="flex items-baseline gap-2 max-w-full border border-dashed border-line bg-transparent rounded-full px-3 py-[3px] text-muted text-[13px]"
          onClick={() => setShowTrigger(!showTrigger)}
          aria-expanded={showTrigger}
        >
          <span className="justify-self-start text-[12px] font-semibold text-warm flex-none">{triggerLabel}</span>
          <span className="overflow-hidden text-ellipsis whitespace-nowrap">{triggerHead}</span>
          <time className="text-[12px] text-muted px-1">{time}</time>
        </button>
        {showTrigger && (
          <div className="whitespace-pre-wrap [overflow-wrap:anywhere] text-[13px] text-muted border-l-2 border-line py-0.5 pl-2.5 justify-self-stretch">
            {message.content}
          </div>
        )}
      </div>
    )
  }

  const notice = message.kind === 'notice' || message.kind === 'report' || message.kind === 'inspector'
  return (
    <article
      className={`grid gap-1 max-w-full ${message.role === 'user' ? 'justify-items-end' : ''} ${
        notice ? 'border border-line border-l-[3px] border-l-warm rounded-[12px] bg-panel px-3.5 py-2.5' : ''
      }`}
      data-testid={`message-${message.kind ?? message.role}`}
    >
      {message.kind === 'notice' && <span className="justify-self-start text-[12px] font-semibold text-warm">提醒</span>}
      {message.kind === 'report' && <span className="justify-self-start text-[12px] font-semibold text-warm">任务汇报</span>}
      {message.kind === 'inspector' && <span className="justify-self-start text-[12px] font-semibold text-warm">巡检</span>}
      {message.thinking && (
        <>
          <button
            className="justify-self-start border-0 bg-sunken text-muted text-[12px] px-2.5 py-[3px] rounded-full"
            onClick={() => setShowThinking(!showThinking)}
          >
            {showThinking ? '收起思考过程' : '查看思考过程'}
          </button>
          {showThinking && (
            <div className="whitespace-pre-wrap [overflow-wrap:anywhere] text-[13px] text-muted border-l-2 border-line py-0.5 pl-2.5">
              {message.thinking}
            </div>
          )}
        </>
      )}
      <div
        className={`whitespace-pre-wrap [overflow-wrap:anywhere] ${message.error ? 'text-danger' : ''} ${
          message.role === 'user'
            ? 'bg-user-bubble text-user-text px-3.5 py-2.5 rounded-[18px_18px_4px_18px] max-w-[min(85%,560px)]'
            : 'px-0.5 py-0.5'
        }`}
      >
        {linkify(message.content).map((part, i) =>
          part.url ? (
            <a key={i} href={part.url} target="_blank" rel="noreferrer" className={message.role === 'user' ? 'text-inherit' : 'text-accent'}>
              {part.text}
            </a>
          ) : (
            <span key={i}>{part.text}</span>
          ),
        )}
        {message.streaming && <span className="inline-block w-2 h-[1em] ml-0.5 align-[-2px] bg-accent stream-caret"></span>}
      </div>
      {!message.streaming && <time className="text-[12px] text-muted px-1">{time}</time>}
    </article>
  )
}
