import { ui, wb } from '../lib/app'
import { taskStates } from '../lib/status'
import type { ProjectCard as Card } from '../lib/workbench'
import AgentCard from './AgentCard'
import ApprovalCard from './ApprovalCard'
import Icon from './Icon'
import Locations from './Locations'
import TaskRow from './TaskRow'

export default function ProjectCard({ card, onOpenTask, onOpenProject }: {
  card: Card
  onOpenTask: (id: string) => void
  onOpenProject: (id: string) => void
}) {
  const idle = card.allAgents.length === 0 && card.allTasks.length === 0
  const info = idle ? { text: '空闲', icon: 'circle', tone: 'neutral' as const } : taskStates[card.rank]
  const runIds = new Set(card.agents.map((a) => a.run.id))
  const approvals = (wb.state.inbox?.approvals ?? []).filter((a) => a.review === 'user' && runIds.has(a.run_id))
  const counts = [
    card.counts.active ? `${card.counts.active} 进行中` : '',
    card.counts.todo ? `${card.counts.todo} 待办` : '',
    card.counts.closed ? `${card.counts.closed} 已结束` : '',
  ].filter(Boolean).join(' · ')
  const hidden = card.hiddenTasks + card.hiddenAgents
  const titleOf = (runId: string) => card.agents.find((a) => a.run.id === runId)?.title ?? ''

  return (
    <article className="grid gap-[7px] content-start border border-line bg-panel rounded-[14px] px-3 py-[11px] min-w-0 self-start hover:border-[color-mix(in_srgb,var(--accent)_30%,var(--line))]" aria-label={`项目 ${card.project.name}`}>
      <header className="flex items-center gap-1.5 min-w-0">
        <button
          className="inline-flex items-center gap-1.5 min-w-0 border-0 bg-none px-1 py-0.5 rounded-[8px] text-left hover:bg-sunken"
          aria-label={`打开项目 ${card.project.name}`}
          onClick={() => onOpenProject(card.project.id)}
        >
          <span className={`tone-${info.tone}`}><Icon name={info.icon} size={14} /></span>
          <strong className="text-[15px] overflow-hidden text-ellipsis whitespace-nowrap">{card.project.name}</strong>
        </button>
        <span className="flex-1"></span>
        {!!card.counts.attention && (
          <span className="inline-flex items-center gap-[3px] text-[12px] font-semibold text-warm border border-[color-mix(in_srgb,var(--warm)_40%,var(--line))] rounded-full px-[7px]" title="需要你处理">
            <Icon name="hand" size={12} />{card.counts.attention}
          </span>
        )}
        <button
          className="icon-btn w-7 h-7"
          title="项目设置"
          aria-label={`${card.project.name} 的设置`}
          onClick={() => onOpenProject(card.project.id)}
        >
          <Icon name="settings" size={14} />
        </button>
      </header>

      <Locations locations={card.locations} />
      {card.project.notes && <p className="m-0 text-[13px] text-muted line-clamp-2 [overflow-wrap:anywhere]">{card.project.notes}</p>}

      {card.agents.map((a) => (
        <AgentCard
          key={a.run.id}
          agent={a}
          onOpen={() => {
            ui.state.runSheet = a.run.id
            ui.emit()
          }}
        />
      ))}

      {card.tasks.length > 0 && (
        <div className="grid gap-0.5 border-t border-line pt-[5px]" aria-label="任务">
          {card.tasks.map((t) => (
            <TaskRow key={t.task.id} task={t} onOpen={() => onOpenTask(t.task.id)} />
          ))}
        </div>
      )}

      {hidden > 0 && (
        <button className="justify-self-start border-0 bg-none text-muted px-1.5 text-[12px] hover:text-accent" onClick={() => onOpenProject(card.project.id)}>
          还有 {hidden} 项…
        </button>
      )}

      {approvals.length > 0 && (
        <div className="grid gap-1.5">
          {approvals.map((a) => <ApprovalCard key={a.id} approval={a} context={titleOf(a.run_id)} />)}
        </div>
      )}

      {idle && <p className="m-0 mx-1.5 text-[13px] text-muted">还没有任务</p>}

      <footer className="flex items-center gap-2.5 border-t border-line pt-1.5">
        <span className="text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap">{counts || (idle ? '空闲' : '')}</span>
        <span className="flex-1"></span>
        <button
          className="link text-[12px] whitespace-nowrap"
          onClick={() => {
            ui.state.newTask = card.project.id
            ui.emit()
          }}
        >
          新建任务
        </button>
        {card.project.deploy && <button className="link text-[12px] whitespace-nowrap" onClick={() => onOpenProject(card.project.id)}>部署</button>}
      </footer>
    </article>
  )
}
