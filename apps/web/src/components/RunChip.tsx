import { go, wb } from '../lib/app'
import type { RunView } from '../lib/runs'
import { avatarOf, looks, runLook } from '../lib/status'
import Avatar from './Avatar'
import Icon from './Icon'

const what: Record<string, string> = { deliver: '交付 PR', rebase: 'rebase', clone: 'clone 项目', create: '新建项目' }

export default function RunChip({ view, nodeName }: { view: RunView; nodeName: (id: string) => string }) {
  const run = view.run
  const waiting = view.approvals.some((a) => a.status === 'pending' && a.review === 'user')
  const look = runLook(run, waiting)
  const persona = wb.state.data?.personas.find((p) => p.name === run.persona)
  const a = run.purpose === 'deliver' || run.purpose === 'clone' || run.purpose === 'create'
    ? { glyph: 's', color: 'gray' as const, title: 'sous' }
    : avatarOf(persona, run.agent)
  const steps = view.tools.length

  function open() {
    if (run.task_id) go({ view: 'task', id: run.task_id })
  }

  return (
    <button
      className="inline-flex items-center gap-1.5 max-w-full min-w-0 justify-self-start overflow-hidden border border-line bg-panel rounded-full pt-[3px] pb-[3px] pr-2.5 pl-1 text-[12px] enabled:hover:border-accent disabled:opacity-100 disabled:cursor-default"
      onClick={open}
      disabled={!run.task_id}
      title={`${a.title} @ ${nodeName(run.node_id)} · ${looks[look].text}${run.title ? ' · ' + run.title : ''}`}
    >
      <Avatar glyph={a.glyph} color={a.color} size={22} ring={looks[look].tone === 'neutral' ? '' : looks[look].tone} />
      <span className={`grid tone-${looks[look].tone}`}><Icon name={looks[look].icon} size={12} /></span>
      <span className="overflow-hidden text-ellipsis whitespace-nowrap min-w-0 flex-[1_1_auto]">{what[run.purpose] ?? run.title ?? ''}</span>
      <span className="inline-flex gap-[3px] items-center text-muted whitespace-nowrap"><Icon name="laptop" size={11} />{nodeName(run.node_id)}{steps ? ` · ${steps} 步` : ''}</span>
      {run.task_id && <span className="grid rotate-[-90deg] text-muted"><Icon name="chevron" size={12} /></span>}
    </button>
  )
}
