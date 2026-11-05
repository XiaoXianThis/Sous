import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { useChat } from '../lib/app'
import { contextLook, reasonText, tokens } from '../lib/context'
import { ago, fullTime } from '../lib/time'
import type { ThreadContext } from '../lib/types'
import Icon from './Icon'
import Markdown from './Markdown'
import Sheet from './Sheet'

const levelColor = { ok: 'var(--accent)', near: 'var(--warm)', full: 'var(--danger)' }

/**
 * Loads a thread's context (window use, summary, cache hits) and reloads it
 * when the thread compacts or gets new messages.
 */
export function useThreadContext(threadId: string): [ThreadContext | null, () => void] {
  const chat = useChat()
  const [view, setView] = useState<ThreadContext | null>(null)
  const [tick, setTick] = useState(0)
  const count = chat.messages[threadId]?.filter((m) => !m.streaming).length ?? 0
  useEffect(() => {
    if (!threadId) {
      setView(null)
      return
    }
    let live = true
    const t = setTimeout(() => {
      api.threadContext(threadId).then((v) => live && setView(v)).catch(() => {})
    }, 400)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [threadId, chat.contextRev, count, tick])
  return [view, () => setTick((n) => n + 1)]
}

/** A ring showing how full the thread's context is; opens the context panel. */
export function ContextRing({ view, onOpen }: { view: ThreadContext; onOpen: () => void }) {
  const look = contextLook(view)
  const r = 7
  const c = 2 * Math.PI * r
  const tip = `上下文 ${Math.round(look.share * 100)}%` + (look.cacheRate === null ? '' : ` · 缓存命中 ${Math.round(look.cacheRate * 100)}%`)
  return (
    <button className="icon-btn" aria-label={tip} data-tip={tip} data-tip-pos="bottom" onClick={onOpen}>
      <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true">
        <circle cx="9" cy="9" r={r} fill="none" stroke="var(--line-strong)" strokeWidth="2.2" />
        <circle cx="9" cy="9" r={r} fill="none" stroke={levelColor[look.level]} strokeWidth="2.2" strokeLinecap="round"
          strokeDasharray={`${Math.max(0.02, look.share) * c} ${c}`} transform="rotate(-90 9 9)" />
      </svg>
    </button>
  )
}

/** What the supervisor keeps of a long conversation: the summary (editable), use and cache hits. */
export function ContextPanel({ threadId, view, onClose, onChanged }: {
  threadId: string
  view: ThreadContext
  onClose: () => void
  onChanged: () => void
}) {
  const look = contextLook(view)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(view.summary)
  const [busy, setBusy] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    if (!editing) setDraft(view.summary)
  }, [view.summary, editing])

  async function act(kind: string, fn: () => Promise<unknown>, done: string) {
    setBusy(kind)
    setError('')
    setNote('')
    try {
      await fn()
      setNote(done)
      onChanged()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const stat = 'grid gap-0.5 bg-sunken rounded-[12px] px-3 py-2 min-w-0'
  return (
    <Sheet title="对话的上下文" onClose={onClose}>
      <p className="m-0 text-[13px] text-muted">
        对话很长时，较早的消息会整理成下面的摘要，主管只看摘要和最近的原文；原文都还在，主管需要时会自己去查。
      </p>
      <div className="grid grid-cols-3 gap-2">
        <div className={stat}>
          <span className="text-[12px] text-faint">占用</span>
          <strong className="text-[15px]" style={{ color: look.level === 'ok' ? undefined : levelColor[look.level] }}>
            {Math.round(look.share * 100)}%
          </strong>
          <span className="text-[12px] text-muted">{tokens(view.used)} / {tokens(view.window)}</span>
        </div>
        <div className={stat}>
          <span className="text-[12px] text-faint">缓存命中</span>
          <strong className="text-[15px]">{look.cacheRate === null ? '—' : `${Math.round(look.cacheRate * 100)}%`}</strong>
          <span className="text-[12px] text-muted">最近 {view.cache.calls} 次调用</span>
        </div>
        <div className={stat}>
          <span className="text-[12px] text-faint">上次整理</span>
          <strong className="text-[15px]" title={fullTime(view.compacted_at ?? 0)}>
            {view.segment > 1 && view.compacted_at ? ago(view.compacted_at) : '还没有'}
          </strong>
          <span className="text-[12px] text-muted">
            {view.segment > 1 ? `${reasonText[view.reason ?? ''] ?? view.reason} · 第 ${view.segment} 段` : `到 ${tokens(view.soft)} 时自动整理`}
          </span>
        </div>
      </div>

      <section className="grid gap-2">
        <div className="flex items-center gap-2">
          <h3 className="m-0 text-[14px] flex-1">摘要</h3>
          {!editing && (
            <button className="btn small ghost" onClick={() => setEditing(true)}><Icon name="edit" size={14} />修改</button>
          )}
        </div>
        {editing ? (
          <>
            <textarea
              className="selectable w-full min-h-[260px] border border-line bg-page text-ink rounded-[12px] p-3 text-[13px] leading-[1.6] font-mono"
              aria-label="摘要"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
            ></textarea>
            <div className="flex gap-2 justify-end">
              <button className="btn small ghost" onClick={() => { setEditing(false); setDraft(view.summary) }}>取消</button>
              <button className="btn small" disabled={busy !== '' || draft === view.summary}
                onClick={() => void act('save', () => api.putSummary(threadId, draft), '已保存，下一轮起生效。').then(() => setEditing(false))}>
                保存
              </button>
            </div>
          </>
        ) : view.summary.trim() ? (
          <div className="card p-3 text-[13px] max-h-[44dvh] overflow-y-auto"><Markdown text={view.summary} /></div>
        ) : (
          <p className="m-0 text-[13px] text-muted">还没有摘要：对话还不长，主管能看到全部原文。</p>
        )}
      </section>

      {view.profile.length > 0 && (
        <details className="text-[13px]">
          <summary className="cursor-pointer text-muted">这一段带上的长期记忆（{view.profile.length} 条）</summary>
          <ul className="mt-2 mb-0 pl-5 grid gap-1">
            {view.profile.map((m) => <li key={m}>{m.replace(/^\[[^\]]+\] \(profile\) /, '')}</li>)}
          </ul>
        </details>
      )}

      <div className="flex items-center gap-2 flex-wrap">
        <button className="btn small" disabled={busy !== ''}
          onClick={() => void act('compact', () => api.compactThread(threadId), '已经开始整理，完成后摘要会更新。')}>
          <Icon name={busy === 'compact' ? 'refresh' : 'layers'} size={14} className={busy === 'compact' ? 'spin' : ''} />现在整理
        </button>
        {note && <span className="text-[12.5px] text-muted">{note}</span>}
        {error && <span className="text-[12.5px] text-danger" role="alert">{error}</span>}
      </div>
    </Sheet>
  )
}
