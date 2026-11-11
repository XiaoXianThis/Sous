import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { loadThread, ui, useChat, useUi } from '../lib/app'
import type { MeetingDetail } from '../lib/types'
import ChatView from './ChatView'
import Icon from './Icon'
import Markdown from './Markdown'
import ModelSwitch from './ModelSwitch'

/**
 * A meeting in the chat column: its own thread and context, a header with
 * the host model and the close/reopen action, and the minutes draft at the
 * end of the timeline once it is closed.
 */
export default function MeetingView() {
  const chat = useChat()
  const uiState = useUi()
  const listed = chat.threads.find((t) => t.id === uiState.meeting)
  const [detail, setDetail] = useState<MeetingDetail | null>(null)
  const [draftText, setDraftText] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const id = uiState.meeting

  useEffect(() => {
    if (id) void loadThread(id)
  }, [id])

  useEffect(() => {
    if (!id) return
    let alive = true
    api.meeting(id).then((d) => { if (alive) setDetail(d) }).catch((e) => { if (alive) setError((e as Error).message) })
    return () => { alive = false }
  }, [id, chat.meetingsRev])

  const meeting = detail?.meeting ?? listed
  const draft = detail?.minutes.filter((m) => m.status === 'draft').sort((a, b) => b.version - a.version)[0]
  const posted = detail?.minutes.filter((m) => m.status === 'posted').sort((a, b) => b.version - a.version)[0]

  useEffect(() => {
    if (draft && !editing) setDraftText(draft.content)
  }, [draft?.version, editing])

  if (!meeting) {
    return (
      <section className="grid place-items-center h-full text-[13px] text-muted" aria-label="会议">
        <span className="flex items-center gap-2"><Icon name="refresh" size={14} className="spin" />正在打开会议…</span>
      </section>
    )
  }

  const closed = meeting.meeting_status === 'closed'

  async function close() {
    if (!window.confirm('散会并整理纪要？')) return
    setBusy(true)
    setError('')
    try {
      await api.closeMeeting(meeting!.id)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function reopen() {
    setBusy(true)
    setError('')
    try {
      await api.reopenMeeting(meeting!.id)
      setEditing(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function post() {
    setBusy(true)
    setError('')
    try {
      await api.postMinutes(meeting!.id, draftText)
      setDetail(await api.meeting(meeting!.id))
      setEditing(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function rewrite() {
    setBusy(true)
    setError('')
    try {
      await api.reopenMeeting(meeting!.id)
      await api.closeMeeting(meeting!.id)
      setEditing(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const foot = closed ? (
    <div className="grid gap-2 pt-1">
      {error && <p className="m-0 text-[12.5px] text-danger" role="alert">{error}</p>}
      {!draft && posted && (
        <p className="m-0 flex items-center gap-1.5 text-[12.5px] text-muted">
          <Icon name="check" size={13} className="text-ok" />纪要已发出 · 第 {posted.version} 版
        </p>
      )}
      {!draft && !posted && (
        <div className="flex items-center gap-2 text-[13px] text-muted h-7" aria-label="正在整理纪要">
          <span className="live-dot w-1.5 h-1.5 rounded-full bg-accent"></span>正在整理纪要…
        </div>
      )}
      {draft && (
        <section className="card grid gap-2.5 p-4" aria-label="纪要草稿">
          <header className="flex items-center gap-2 min-w-0">
            <Icon name="note" size={15} className="text-accent flex-none" />
            <span className="font-semibold text-[14px] min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">纪要草稿 · 第 {draft.version} 版</span>
            <span className="flex-1"></span>
            <button className="btn ghost small px-3" disabled={busy} onClick={() => setEditing(!editing)}>{editing ? '预览' : '编辑'}</button>
          </header>
          {editing ? (
            <textarea
              value={draftText}
              onChange={(e) => setDraftText(e.target.value)}
              rows={14}
              aria-label="纪要内容"
              className="w-full resize-y rounded-[10px] border border-line-strong bg-page px-3 py-2 text-[13px] leading-[1.65] font-mono outline-0"
            />
          ) : (
            <div className="min-w-0"><Markdown text={draftText} /></div>
          )}
          <div className="flex items-center gap-2 flex-wrap">
            <button className="btn small px-4" disabled={busy || !draftText.trim()} onClick={() => void post()}>发出纪要</button>
            <button className="btn ghost small px-3" disabled={busy} onClick={() => void rewrite()}>重新整理</button>
            <span className="text-[12px] text-faint">发出后回到原来的对话，sous 会接手行动项</span>
          </div>
        </section>
      )}
    </div>
  ) : error ? <p className="m-0 text-[12.5px] text-danger" role="alert">{error}</p> : null

  return (
    <ChatView
      threadId={meeting.id}
      threads={[meeting]}
      title={`会议 · ${meeting.title}`}
      avatar={<span className="grid place-items-center w-7 h-7 rounded-[9px] bg-brand text-white"><Icon name="meeting" size={15} stroke={2} /></span>}
      threadLabel={(t) => t.title}
      threadIcon={() => 'meeting'}
      onPick={() => {}}
      onNew={() => {}}
      onSend={async (text) => { await api.send(meeting.id, text) }}
      placeholder={closed ? '会议已经结束，点“续会”接着讨论' : '说点什么…'}
      empty={<p className="text-[13px] text-muted m-0">会议还没开始说话。</p>}
      leading={
        <button className="icon-btn" aria-label="回到对话" data-tip="回到对话" data-tip-pos="bottom" onClick={() => { ui.state.meeting = ''; ui.emit() }}>
          <Icon name="back" size={18} />
        </button>
      }
      actions={
        <>
          <span className={`pill ${closed ? '' : 'tone-accent'}`}>{closed ? '已散会' : '进行中'}</span>
          <ModelSwitch value={meeting.host ?? null} onChange={async (v) => { await api.setMeetingHost(meeting.id, v) }} />
          {closed ? (
            <button className="btn small px-3" disabled={busy} onClick={() => void reopen()}>续会</button>
          ) : (
            <button className="btn small px-3" disabled={busy} onClick={() => void close()}>散会</button>
          )}
        </>
      }
      disabled={closed}
      focusSeq={uiState.meetingSeq}
      foot={foot}
      label="会议"
      picker={false}
    />
  )
}
