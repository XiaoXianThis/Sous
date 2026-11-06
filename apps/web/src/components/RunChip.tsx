import { go, wb } from '../lib/app'
import { gitOpText, type RunView } from '../lib/runs'
import { avatarOf, looks, runLook } from '../lib/status'
import { Who } from './Avatar'
import Icon from './Icon'

const what: Record<string, string> = { deliver: '交付 PR', rebase: 'rebase', clone: 'clone 项目', create: '新建项目', review: '验收', consult: '顾问在看代码' }

export default function RunChip({ view, nodeName }: { view: RunView; nodeName: (id: string) => string }) {
  const run = view.run
  // Approvals are answered on the workbench (the task's card), not here: the
  // chip only says it waits. The review agent's do not count.
  const waiting = view.approvals.some((a) => a.status === 'pending' && a.review === 'user')
  const look = runLook(run, waiting)
  const info = looks[look]
  const persona = wb.state.data?.personas.find((p) => p.name === run.persona)
  const system = run.purpose === 'deliver' || run.purpose === 'clone' || run.purpose === 'create' || run.purpose === 'git'
  const a = system ? { glyph: 's', color: 'gray' as const, title: 'sous' } : avatarOf(persona, run.agent)
  const steps = view.tools.length

  function open() {
    if (run.task_id) go({ view: 'task', id: run.task_id })
  }

  return (
    <button
      className="flex items-center gap-2 max-w-full min-w-0 justify-self-start overflow-hidden border-0 bg-panel shadow-soft rounded-[16px] py-1.5 pr-2.5 pl-1.5 text-[12.5px] enabled:hover:shadow-card disabled:opacity-100 disabled:cursor-default transition-shadow"
      onClick={open}
      disabled={!run.task_id}
      title={`${a.title} @ ${nodeName(run.node_id)} · ${info.text}${run.title ? ' · ' + run.title : ''}`}
    >
      <Who persona={system ? undefined : persona} agent={system ? 'sous' : run.agent} size={28} />
      <span className="grid min-w-0 text-left leading-tight">
        <span className="font-semibold overflow-hidden text-ellipsis whitespace-nowrap">{run.purpose === 'git' ? gitOpText(run.git) : (what[run.purpose] ?? run.title ?? '')}</span>
        <span className="flex items-center gap-1.5 text-[11px] text-muted whitespace-nowrap">
          <span className={`inline-flex items-center gap-0.5 font-semibold tone-${info.tone}`}><Icon name={info.icon} size={10} stroke={2.4} />{info.text}</span>
          <span className="inline-flex items-center gap-0.5"><Icon name="laptop" size={10} />{nodeName(run.node_id)}</span>
          {steps > 0 && <span className="inline-flex items-center gap-0.5"><Icon name="list" size={10} />{steps}</span>}
        </span>
      </span>
      {run.task_id && <Icon name="forward" size={14} className="text-faint" />}
    </button>
  )
}
