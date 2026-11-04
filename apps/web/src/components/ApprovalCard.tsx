import { useState } from 'react'
import { api } from '../lib/api'
import { categoryText } from '../lib/tasks'
import type { Approval } from '../lib/types'

export default function ApprovalCard({ approval, context = '' }: { approval: Approval; context?: string }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [withNote, setWithNote] = useState(false)

  async function decide(optionId: string) {
    setBusy(true)
    setError('')
    try {
      await api.decide(approval.id, optionId, note.trim())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className={`border border-warm rounded-[12px] px-3 py-2.5 grid gap-2 bg-[color-mix(in_srgb,var(--warm)_8%,var(--panel))] ${approval.review === 'ai' ? 'border-dashed' : ''}`}
      role="group"
      aria-label="审批"
      data-testid="approval"
    >
      <p className="m-0 [overflow-wrap:anywhere]">
        {approval.category && (
          <span className="inline-block text-[11px] font-semibold border border-line rounded-full px-[7px] mr-1.5 text-muted align-[1px]">
            {categoryText[approval.category] ?? approval.category}
          </span>
        )}
        {approval.review === 'ai' ? '审批 agent 正在判断：' : '需要你批准：'}<strong>{approval.title}</strong>
      </p>
      {context && <p className="m-0 [overflow-wrap:anywhere] text-[13px] text-muted">{context}</p>}
      {approval.escalation && <p className="m-0 [overflow-wrap:anywhere] text-[13px] text-warm">审批 agent 交给你：{approval.escalation}</p>}
      {approval.detail && (
        <pre className="m-0 max-h-[200px] overflow-auto font-mono text-[12px] leading-[1.45] bg-page rounded-[8px] px-2.5 py-2 whitespace-pre-wrap [overflow-wrap:anywhere]">
          {approval.detail}
        </pre>
      )}
      {withNote && (
        <input
          className="border border-line bg-page rounded-[8px] px-2.5 py-1.5"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="附一句话（会一起转给 agent）"
          aria-label="说明"
        />
      )}
      <div className="flex gap-2 flex-wrap items-center">
        {approval.options.map((o) => (
          <button key={o.option_id} className={o.kind.startsWith('allow') ? 'btn small' : 'btn small ghost'} disabled={busy} onClick={() => void decide(o.option_id)}>
            {o.name}
          </button>
        ))}
        {!withNote && <button className="link" onClick={() => setWithNote(true)}>附言</button>}
      </div>
      {error && <p className="m-0 text-danger text-[13px]" role="alert">{error}</p>}
    </div>
  )
}
