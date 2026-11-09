import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { useChat } from '../lib/app'
import { talkWho } from '../lib/talk'
import type { Talk } from '../lib/types'
import AgentIcon, { agentTitle } from './AgentIcon'
import Icon from './Icon'
import Markdown from './Markdown'
import { Clamp } from './Message'

const kindIcon: Record<Talk['kind'], string> = {
  prompt: 'send',
  message: 'message',
  progress: 'pulse',
  ask: 'bulb',
  result: 'check-circle',
  review: 'eye',
  report: 'bell',
}

/**
 * What the supervisor (or the user) and a task's agents said to each other
 * (work/specs/chat-topics §5): the prompts, follow-ups, progress, questions,
 * results, reviews and reports. Collapsed to one line until opened; loaded
 * when opened, and again when the task moves on.
 */
export default function TalkLog({ taskId, className = '' }: { taskId: string; className?: string }) {
  const chatState = useChat()
  const [open, setOpen] = useState(false)
  const [talk, setTalk] = useState<Talk[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    let gone = false
    const timer = setTimeout(() => {
      api.taskTalk(taskId).then(
        (r) => { if (!gone) { setTalk(r.talk); setError('') } },
        (e) => { if (!gone) setError((e as Error).message) },
      )
    }, talk ? 400 : 0)
    return () => { gone = true; clearTimeout(timer) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, taskId, chatState.tasksRev, chatState.runsRev, chatState.topicsRev])

  return (
    <div className={`grid grid-cols-[minmax(0,1fr)] gap-2 ${className}`}>
      <button
        type="button"
        className="flex items-center gap-1.5 border-0 bg-transparent p-0 h-6 text-[12px] text-muted hover:text-ink w-fit"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <Icon name="users" size={13} />
        主管 ↔ agent 的沟通{talk ? ` · ${talk.length} 条` : ''}
        <Icon name="chevron" size={11} className={`transition-transform duration-200 ${open ? '' : '-rotate-90'}`} />
      </button>
      {open && (
        <ol className="m-0 p-0 list-none grid grid-cols-[minmax(0,1fr)] gap-2 rise" aria-label="主管和 agent 的沟通">
          {error && <li className="text-[12.5px] text-danger">{error}</li>}
          {!talk && !error && <li className="text-[12.5px] text-faint">正在读取…</li>}
          {talk?.length === 0 && <li className="text-[12.5px] text-faint">还没有往来</li>}
          {talk?.map((t) => (
            <TalkItem key={`${t.seq}-${t.kind}`} talk={t} />
          ))}
        </ol>
      )}
    </div>
  )
}

function TalkItem({ talk }: { talk: Talk }) {
  const [open, setOpen] = useState(false)
  const who = (w: string) => (w === 'agent' ? agentTitle(talk.agent || 'agent') : talkWho(w))
  const from = who(talk.from)
  const to = who(talk.to)
  const time = new Date(talk.ts).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  const fromAgent = talk.from === 'agent'
  const first = talk.text.split('\n').find((l) => l.trim())?.trim() ?? ''
  return (
    <li className={`grid grid-cols-[minmax(0,1fr)] gap-1 rounded-[12px] px-3 py-2 ${fromAgent ? 'bg-sunken' : 'bg-[color-mix(in_srgb,var(--accent)_6%,var(--panel))]'}`} data-testid={`talk-${talk.kind}`}>
      <button type="button" className="flex items-center gap-1.5 min-w-0 border-0 bg-transparent p-0 text-left text-[12px]" aria-expanded={open} onClick={() => setOpen(!open)}>
        {fromAgent ? <AgentIcon name={talk.agent ?? ''} size={16} /> : <Icon name={kindIcon[talk.kind]} size={13} className="text-muted flex-none" />}
        <span className="font-semibold text-ink flex-none">{from}</span>
        <Icon name="forward" size={11} className="text-faint flex-none" />
        <span className="text-muted flex-none">{to}</span>
        <span className="text-faint flex-none">· {talk.title}</span>
        {!open && <span className="text-muted overflow-hidden text-ellipsis whitespace-nowrap min-w-0">{first}</span>}
        <span className="flex-1"></span>
        <time className="text-faint tabular-nums flex-none">{time}</time>
      </button>
      {open && (
        <Clamp max={320} label={talk.title}>
          {talk.kind === 'result' || talk.kind === 'report' ? (
            <Markdown text={talk.text} />
          ) : (
            <p className="selectable m-0 whitespace-pre-wrap [overflow-wrap:anywhere] text-[12.5px] leading-[1.65] text-ink">{talk.text}</p>
          )}
        </Clamp>
      )}
    </li>
  )
}

