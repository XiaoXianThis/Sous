import { useState } from 'react'
import { api } from '../lib/api'
import { useWb } from '../lib/app'
import { approvalTitle, categoryText, optionText, toolKindText } from '../lib/tasks'
import type { Approval, Overview } from '../lib/types'
import { agentTitle } from './AgentIcon'
import Icon from './Icon'

/** Where an approval comes from: project, machine and agent, from the
 *  overview (end-to-end review 4.7). */
export function approvalWhere(o: Overview | null | undefined, a: Pick<Approval, 'run_id' | 'task_id'>): string {
  if (!o) return ''
  const run = o.runs.find((r) => r.id === a.run_id)
  const task = o.tasks.find((t) => t.id === (a.task_id || run?.task_id))
  const pid = task?.project_id || run?.project_id
  const project = pid && pid !== 'misc' ? o.projects.find((p) => p.id === pid)?.name : ''
  const node = run ? o.nodes.find((n) => n.id === run.node_id)?.name : ''
  const agent = run && run.agent && run.agent !== 'sous' ? agentTitle(run.agent) : run?.agent === 'sous' ? 'sous' : ''
  return [project, node, agent].filter(Boolean).join(' · ')
}

/** What kind of action an approval is about: the policy's class, except an
 *  unknown non-command, which says its tool kind. */
function kindLabel(a: Approval): string {
  if (a.category === 'exec-other' && a.tool_kind && a.tool_kind !== 'execute') return toolKindText[a.tool_kind] ?? '其他操作'
  return categoryText[a.category] ?? a.category
}

const categoryIcon: Record<string, string> = {
  read: 'eye', 'edit-worktree': 'edit', 'exec-safe': 'terminal', 'exec-other': 'terminal', install: 'download',
  'outside-worktree': 'folder', delete: 'trash', 'git-remote': 'pr', irreversible: 'alert', floor: 'shield',
}

export default function ApprovalCard({ approval, context = '' }: { approval: Approval; context?: string }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  const [withNote, setWithNote] = useState(false)
  const [showDetail, setShowDetail] = useState(false)
  const ai = approval.review === 'ai'
  const wb = useWb()
  const where = approvalWhere(wb.data, approval)
  const title = approvalTitle(approval.title)

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
      className={`rounded-[16px] p-3 grid gap-2.5 bg-[color-mix(in_srgb,var(--warm)_9%,var(--panel))] shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--warm)_35%,transparent)] ${ai ? 'opacity-80' : ''}`}
      role="group"
      aria-label="审批"
      data-testid="approval"
    >
      <div className="flex items-start gap-2.5 min-w-0">
        <span className="grid place-items-center w-8 h-8 rounded-[12px] flex-none bg-warm text-white" title={ai ? '审批 agent 正在判断' : '需要你批准'}>
          <Icon name={ai ? 'sparkles' : categoryIcon[approval.category] ?? 'shield'} size={16} stroke={2} />
        </span>
        <div className="grid gap-0.5 min-w-0 flex-1">
          <p className="m-0 text-[13.5px] font-semibold leading-snug [overflow-wrap:anywhere]" title={title !== approval.title ? approval.title : undefined}>{title}</p>
          <p className="m-0 flex items-center gap-1.5 flex-wrap text-[11.5px] text-muted">
            {approval.category && (
              <span className="inline-flex items-center gap-1 font-semibold text-warm">{kindLabel(approval)}</span>
            )}
            {where && <span className="inline-flex items-center gap-1"><Icon name="server" size={11} />{where}</span>}
            {ai && <span className="inline-flex items-center gap-1"><Icon name="sparkles" size={11} />AI 审批中</span>}
            {context && <span className="overflow-hidden text-ellipsis whitespace-nowrap max-w-full">{context}</span>}
          </p>
        </div>
        {approval.detail && (
          <button
            className="icon-btn w-7 h-7 toggle"
            aria-expanded={showDetail}
            aria-label="详情"
            onClick={() => setShowDetail(!showDetail)}
          >
            <Icon name="chevron" size={15} />
          </button>
        )}
      </div>
      {approval.escalation && (
        <p className="m-0 flex gap-1.5 items-start text-[12.5px] text-warm [overflow-wrap:anywhere]"><Icon name="sparkles" size={13} className="mt-[3px]" />{approval.escalation}</p>
      )}
      {approval.detail && showDetail && (
        <pre className="m-0 max-h-[220px] overflow-auto font-mono text-[11.5px] leading-[1.5] bg-sunken rounded-[12px] px-2.5 py-2 whitespace-pre-wrap [overflow-wrap:anywhere]">
          {approval.detail}
        </pre>
      )}
      {withNote && (
        <input
          className="border border-line bg-panel rounded-[12px] px-2.5 py-1.5 text-[13px]"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="附一句话（会一起转给 agent）"
          aria-label="说明"
          autoFocus
        />
      )}
      <div className="flex gap-1.5 flex-wrap items-center">
        {approval.options.map((o) => {
          const allow = o.kind.startsWith('allow')
          const local = optionText[o.kind]
          return (
            <button
              key={o.option_id}
              className={`btn small ${allow ? '' : 'ghost'}`}
              disabled={busy}
              onClick={() => void decide(o.option_id)}
              title={local ? `${local.tip}（agent 的原文：${o.name}）` : o.name}
            >
              <Icon name={allow ? 'check' : 'x'} size={14} stroke={2.4} />{local?.text ?? o.name}
            </button>
          )
        })}
        <span className="flex-1"></span>
        {!withNote && (
          <button className="icon-btn w-8 h-8" aria-label="附言" data-tip="附言" data-tip-pos="bottom" onClick={() => setWithNote(true)}>
            <Icon name="message" size={16} />
          </button>
        )}
      </div>
      {approval.options.some((o) => o.kind === 'allow_always') && (
        <p className="m-0 text-[11.5px] text-faint leading-snug">“以后都允许”由 agent 自己记住：通常只在这次执行里有效，有的 agent 会写进项目的权限设置。</p>
      )}
      {error && <p className="m-0 text-danger text-[12.5px]" role="alert">{error}</p>}
    </div>
  )
}
