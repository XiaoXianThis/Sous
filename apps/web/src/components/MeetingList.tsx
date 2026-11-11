import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { ui, useChat } from '../lib/app'
import { ago } from '../lib/time'
import type { Thread } from '../lib/types'
import Icon from './Icon'
import NewMeetingDialog from './NewMeetingDialog'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'

/**
 * The meetings people can return to: a popover from the chat header, grouped
 * by whether they are still open. "开会" starts one from this thread.
 */
export default function MeetingList({ parentThreadId }: { parentThreadId: string }) {
  const chat = useChat()
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [meetings, setMeetings] = useState<Thread[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    let alive = true
    api.meetings().then((r) => { if (alive) setMeetings(r.meetings) }).catch((e) => { if (alive) setError((e as Error).message) })
    return () => { alive = false }
  }, [open, chat.meetingsRev])

  const live = meetings.filter((m) => m.meeting_status !== 'closed')
  const done = meetings.filter((m) => m.meeting_status === 'closed')

  const row = (m: Thread) => (
    <button
      key={m.id}
      className="border-0 bg-transparent text-left rounded-[8px] px-2 py-1.5 flex items-center gap-2 min-w-0 hover:bg-hover"
      onClick={() => {
        ui.state.meeting = m.id
        ui.emit()
        setOpen(false)
      }}
    >
      <Icon name="meeting" size={14} className="text-muted flex-none" />
      <span className="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-[13px]">{m.title}</span>
      <time className="text-[11.5px] text-faint tabular-nums flex-none">{ago(m.updated_at)}</time>
    </button>
  )

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button className="icon-btn" aria-label="会议" data-tip="会议" data-tip-pos="bottom"><Icon name="meeting" size={17} /></button>
        </PopoverTrigger>
        <PopoverContent aria-label="会议列表" sideOffset={4} className="w-[min(300px,calc(100vw-16px))] max-h-[min(60dvh,var(--radix-popover-content-available-height))] p-1 grid gap-1 content-start">
          <button className="btn small mx-1 mt-1" onClick={() => { setOpen(false); setCreating(true) }}>开会</button>
          {error && <p className="m-0 px-2 py-1 text-[12px] text-danger">{error}</p>}
          {meetings.length === 0 && <p className="m-0 px-2 py-2 text-[12.5px] text-muted">还没有会议。</p>}
          {live.length > 0 && <p className="m-0 px-2 pt-2 pb-0.5 text-[11.5px] text-faint">进行中</p>}
          {live.map(row)}
          {done.length > 0 && <p className="m-0 px-2 pt-2 pb-0.5 text-[11.5px] text-faint">已散会</p>}
          {done.map(row)}
        </PopoverContent>
      </Popover>
      {creating && <NewMeetingDialog parentThreadId={parentThreadId} onClose={() => setCreating(false)} />}
    </>
  )
}
