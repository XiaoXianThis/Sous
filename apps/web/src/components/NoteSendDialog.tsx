import { useState } from 'react'
import { api } from '../lib/api'
import { chat, sendToProject, ui, useWb } from '../lib/app'
import { joinNotes } from '../lib/notes'
import type { Note } from '../lib/types'
import Icon from './Icon'
import { Dialog, DialogContent, DialogTitle } from './ui/dialog'

type Target = { id: string; icon: string; name: string; desc: string }

/** Hands the chosen notes to sous: pick where, edit the joined text, send. */
export default function NoteSendDialog({ notes, close, sent }: { notes: Note[]; close: () => void; sent: () => void }) {
  const wb = useWb()
  const joined = joinNotes(notes)
  const [target, setTarget] = useState<Target | null>(null)
  const [text, setText] = useState(joined.text)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const projects = (wb.data?.projects ?? []).filter((p) => !p.archived_at)
  const targets: Target[] = [
    { id: '', icon: 'chat', name: '主线', desc: 'sous 看得到所有项目' },
    ...projects.map((p) => ({ id: p.id, icon: 'folder', name: p.name, desc: '项目对话：只看这个项目' })),
    { id: 'meeting', icon: 'meeting', name: '开一个会', desc: '在独立上下文里讨论，之后把纪要发回来' },
  ]

  const submit = async () => {
    if (!target) return
    setBusy(true)
    setError('')
    try {
      const ids = notes.map((n) => n.id)
      const files = joined.attachments.map((a) => a.id)
      if (target.id === 'meeting') {
        const main = chat.state.threads.find((t) => t.kind === 'main')
        if (!main) throw new Error('找不到主线')
        const { meeting } = await api.createMeeting({ parent_thread_id: main.id, question: text, ...(ids.length && { note_ids: ids }) })
        ui.state.meeting = meeting.id
        ui.emit()
      } else if (target.id === '') {
        const main = chat.state.threads.find((t) => t.kind === 'main')
        if (!main) throw new Error('找不到主线')
        await api.send(main.id, text, { note_ids: ids }, files)
      } else {
        await sendToProject(target.id, target.name, text, undefined, files, undefined, '', ids)
      }
      ui.state.noteSend = []
      ui.emit()
      sent()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && close()}>
      <DialogContent className="sheet !p-0 overflow-hidden" aria-label="发给 sous">
        <header className="flex items-center gap-3 px-5 pt-4 pb-3 border-b border-line">
          {target && (
            <button className="icon-btn" aria-label="换一个地方" onClick={() => setTarget(null)}>
              <Icon name="back" size={18} />
            </button>
          )}
          <div className="min-w-0 flex-1">
            <DialogTitle className="text-[17px] font-semibold m-0">{target ? (target.id === 'meeting' ? '开会' : `发到${target.name}`) : '发给 sous'}</DialogTitle>
            <div className="text-[13px] text-muted mt-0.5">{notes.length} 条笔记{target ? '，发之前可以改' : '，选一个对话'}</div>
          </div>
          <button className="icon-btn" data-back aria-label="关闭" onClick={close}>
            <Icon name="x" size={18} />
          </button>
        </header>
        {!target ? (
          <div className="overflow-y-auto p-3 grid">
            {targets.map((t) => (
              <button key={t.id || 'main'} className="flex items-center gap-3 min-h-[56px] px-3 rounded-[12px] text-left hover:bg-hover" onClick={() => setTarget(t)}>
                <span className="w-9 h-9 rounded-[10px] bg-sunken grid place-items-center shrink-0">
                  <Icon name={t.icon} size={17} />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-[14px] font-semibold truncate">{t.name}</span>
                  <span className="block text-[12px] text-muted">{t.desc}</span>
                </span>
                <Icon name="chevron" size={14} className="text-faint -rotate-90" />
              </button>
            ))}
          </div>
        ) : (
          <div className="p-5 grid gap-3">
            <textarea className="w-full min-h-[220px] max-h-[50dvh] resize-y rounded-[12px] bg-sunken p-3 text-[16px] md:text-[14px] leading-[1.6] outline-none focus-visible:!shadow-none focus:ring-2 focus:ring-accent/40" value={text} onChange={(e) => setText(e.target.value)} />
            {joined.attachments.length > 0 && <div className="text-[12px] text-muted">带 {joined.attachments.length} 个附件</div>}
            {joined.dropped && <div className="text-[12px] text-muted">附件最多 8 个，多出来的不会发出</div>}
            {error && <div className="text-[13px] text-danger">{error}</div>}
            <div className="flex justify-end gap-2">
              <button className="btn ghost small" onClick={() => setTarget(null)}>
                返回
              </button>
              <button className="btn small" disabled={busy || !text.trim()} onClick={() => void submit()}>
                {busy ? '正在发…' : '发送'}
              </button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
