import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { ui, useChat } from '../lib/app'
import Icon from './Icon'
import { Dialog, DialogContent, DialogTitle } from './ui/dialog'

/**
 * Starts a meeting from a topic or a note: the question, then the meeting
 * opens in the chat column with a brief built from the parent topic.
 */
export default function NewMeetingDialog({ parentThreadId, parentTopicId, initial = '', noteIds, onClose }: {
  parentThreadId: string
  parentTopicId?: string
  initial?: string
  noteIds?: string[]
  onClose: () => void
}) {
  const chat = useChat()
  const [question, setQuestion] = useState(initial)
  const [topic, setTopic] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const parent = chat.threads.find((t) => t.id === parentThreadId)

  useEffect(() => {
    if (!parentTopicId) return
    let alive = true
    api.topic(parentTopicId).then((t) => { if (alive) setTopic(t.title) }).catch(() => {})
    return () => { alive = false }
  }, [parentTopicId])

  async function submit() {
    if (!question.trim() || busy) return
    setBusy(true)
    setError('')
    try {
      const { meeting } = await api.createMeeting({
        parent_thread_id: parentThreadId,
        ...(parentTopicId && { parent_topic_id: parentTopicId }),
        question: question.trim(),
        ...(noteIds?.length && { note_ids: noteIds }),
      })
      ui.state.meeting = meeting.id
      ui.emit()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sheet !p-0 overflow-hidden" aria-label="开个会">
        <header className="flex items-center gap-3 px-5 pt-4 pb-3 border-b border-line">
          <div className="min-w-0 flex-1">
            <DialogTitle className="text-[17px] font-semibold m-0">开个会</DialogTitle>
            <div className="text-[13px] text-muted mt-0.5">会议有自己的上下文，散会后纪要学会回到这里</div>
          </div>
          <button className="icon-btn" data-back aria-label="关闭" onClick={onClose}><Icon name="x" size={18} /></button>
        </header>
        <div className="grid gap-3 p-4">
          <label className="grid gap-1.5">
            <span className="text-[13px] text-muted">这次要讨论清楚什么？</span>
            <textarea
              autoFocus
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              rows={4}
              placeholder="说得具体一点：目标、已经试过的做法、卡在哪里…"
              className="w-full resize-none rounded-[12px] border border-line-strong bg-panel px-3 py-2.5 text-[14px] leading-[1.6] outline-0 placeholder:text-faint"
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault()
                  void submit()
                }
              }}
            />
          </label>
          <p className="m-0 text-[12px] text-faint min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
            来自：{parent?.title ?? '当前对话'}{topic ? ` · ${topic}` : ''}
          </p>
          {error && <p className="m-0 text-[12.5px] text-danger" role="alert">{error}</p>}
          <div className="flex justify-end gap-2">
            <button className="btn ghost small px-4" onClick={onClose}>取消</button>
            <button className="btn small px-4" disabled={!question.trim() || busy} onClick={() => void submit()}>
              {busy ? '正在开会…' : '开会'}
            </button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
