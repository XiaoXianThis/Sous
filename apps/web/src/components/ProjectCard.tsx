import { ui } from '../lib/app'
import { setArchived } from '../lib/projects'
import { taskStates } from '../lib/status'
import { ago, fullTime } from '../lib/time'
import type { ProjectCard as Card } from '../lib/workbench'
import AgentCard from './AgentCard'
import FileTree from './FileTree'
import Icon from './Icon'
import { LocationsChip } from './Locations'
import TaskRow from './TaskRow'

/** shell is only in the expanded dialog; a card shows it as tasks. */
export type ProjectTab = 'tasks' | 'details' | 'shell'

/** The badge an archived project shows next to its name. */
export function ArchivedBadge() {
  return (
    <span className="pill flex-none" title="已归档：不在工作台上，agent 平时不管它">
      <Icon name="archive" size={12} stroke={2.2} />已归档
    </span>
  )
}

/** The two views of a project, shared by the card and its expanded dialog. */
export function ProjectTabs({ tab, onTab, tasks, shell = false }: { tab: ProjectTab; onTab: (t: ProjectTab) => void; tasks: number; shell?: boolean }) {
  const item = (t: ProjectTab, icon: string, text: string, n?: number) => (
    <button
      role="tab"
      aria-selected={tab === t}
      className={`flex items-center justify-center gap-1.5 flex-1 h-8 rounded-[10px] border-0 text-[13px] font-medium transition-colors ${
        tab === t ? 'bg-panel text-ink shadow-soft' : 'bg-transparent text-muted hover:text-ink'
      }`}
      onClick={() => onTab(t)}
    >
      <Icon name={icon} size={16} />{text}{n ? <span className="tabular-nums text-faint">{n}</span> : null}
    </button>
  )
  return (
    <div role="tablist" className="flex gap-1 p-1 rounded-[12px] bg-sunken">
      {item('tasks', 'list', '任务', tasks)}
      {item('details', 'folder', '详情')}
      {shell && item('shell', 'terminal', '终端')}
    </div>
  )
}

export default function ProjectCard({ card, tab, onTab, onExpand, onOpenTask, onOpenProject }: {
  card: Card
  tab: ProjectTab
  onTab: (t: ProjectTab) => void
  /** Open the card as a dialog; file is the file to show there. */
  onExpand: (file?: string) => void
  onOpenTask: (id: string) => void
  onOpenProject: (id: string) => void
}) {
  const p = card.project
  const idle = card.allAgents.length === 0 && card.allTasks.length === 0
  const hidden = card.hiddenTasks + card.hiddenAgents
  const counts = (['active', 'todo', 'closed'] as const).filter((k) => card.counts[k] > 0)
  const newTask = () => {
    ui.state.newTask = p.id
    ui.emit()
  }

  return (
    <article
      className="card flex flex-col gap-3.5 p-4 min-w-0 h-full transition-shadow hover:shadow-[0_0_0_1px_var(--line-strong)]"
      aria-label={`项目 ${p.name}`}
      data-project-card={p.id}
    >
      <header className="flex items-start gap-3 min-w-0">
        <div className="grid gap-1 min-w-0 flex-1 self-center">
          <div className="flex items-center gap-1.5 min-w-0">
            <button
              className="max-w-full border-0 bg-transparent p-0 text-left text-[14.5px] font-semibold tracking-tight leading-tight overflow-hidden text-ellipsis whitespace-nowrap hover:text-muted"
              onClick={() => onOpenProject(p.id)}
              title={p.notes || p.name}
            >
              {p.name}
            </button>
            {card.archived && <ArchivedBadge />}
          </div>
        </div>
        <div className="flex items-center gap-0.5 -mr-1.5 -mt-1">
          {p.id !== 'misc' && (card.archived ? (
            <button className="icon-btn" aria-label={`激活 ${p.name}`} data-tip="激活，放回工作台" data-tip-pos="bottom" onClick={() => void setArchived(p, false)}>
              <Icon name="unarchive" size={17} />
            </button>
          ) : (
            <button className="icon-btn" aria-label={`归档 ${p.name}`} data-tip="归档" data-tip-pos="bottom" onClick={() => void setArchived(p, true)}>
              <Icon name="archive" size={17} />
            </button>
          ))}
          <button className="icon-btn" aria-label={`在 ${p.name} 里新建任务`} data-tip="新建任务" data-tip-pos="bottom" onClick={newTask}>
            <Icon name="plus" size={18} />
          </button>
          {p.deploy && (
            <button className="icon-btn" aria-label={`部署 ${p.name}`} data-tip="部署" data-tip-pos="bottom" onClick={() => onOpenProject(p.id)}>
              <Icon name="rocket" size={17} />
            </button>
          )}
          <button className="icon-btn" aria-label={`放大 ${p.name}`} data-tip="放大" data-tip-pos="bottom" onClick={() => onExpand()}>
            <Icon name="expand" size={16} />
          </button>
          <button className="icon-btn" aria-label={`${p.name} 的设置`} data-tip="项目详情" data-tip-pos="bottom" onClick={() => onOpenProject(p.id)}>
            <Icon name="more" size={20} stroke={2.6} />
          </button>
        </div>
      </header>

      <ProjectTabs tab={tab} onTab={onTab} tasks={card.allTasks.length + card.allAgents.length} />

      {tab === 'details' ? (
        <div className="grid gap-2 content-start flex-1 min-h-0">
          {p.notes && <p className="m-0 text-[12.5px] text-muted line-clamp-2 [overflow-wrap:anywhere]">{p.notes}</p>}
          {p.id === 'misc' ? (
            <p className="m-0 text-[13px] text-muted">杂项任务没有自己的目录。</p>
          ) : (
            <div className="h-[248px] overflow-y-auto -mx-1 px-1">
              <FileTree projectId={p.id} selected="" onSelect={(f) => onExpand(f)} />
            </div>
          )}
        </div>
      ) : (
      <div className="grid gap-2 content-start flex-1">
      {card.agents.length > 0 && (
        <div className="grid gap-2">
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
        </div>
      )}


      {card.tasks.length > 0 && (
        <div className="grid gap-0.5 -mx-2" aria-label="任务">
          {card.tasks.map((t) => (
            <TaskRow key={t.task.id} task={t} onOpen={() => onOpenTask(t.task.id)} />
          ))}
        </div>
      )}

      {idle && (
        <button
          className="flex items-center justify-center gap-1.5 h-16 rounded-[14px] border border-dashed border-line-strong bg-transparent text-muted text-[13px] hover:text-muted hover:border-accent"
          onClick={newTask}
        >
          <Icon name="plus" size={16} />新任务
        </button>
      )}
      </div>
      )}

      <footer className="flex items-center gap-1.5 pt-0.5 min-h-[22px]">
          {p.id !== 'misc' && <LocationsChip locations={card.locations} />}
          {counts.map((k) => (
            <span key={k} className={`pill tone-${taskStates[k].tone}`} title={taskStates[k].text}>
              <Icon name={taskStates[k].icon} size={12} stroke={2.2} />{card.counts[k]}
            </span>
          ))}
          {card.unmerged > 0 && (
            <span className="pill" title={`${card.unmerged} 个任务的改动还在分支上，没有合并`}>
              <Icon name="branch" size={12} stroke={2.2} />{card.unmerged} 未合并
            </span>
          )}
          <span className="flex-1"></span>
          {hidden > 0 && (
            <button className="pill border-0 hover:text-ink" onClick={() => onOpenProject(p.id)} aria-label={`还有 ${hidden} 项`}>
              +{hidden}<Icon name="forward" size={12} stroke={2.4} />
            </button>
          )}
          {card.touchedAt > 0 && (
            <time
              className="text-[12px] text-faint tabular-nums whitespace-nowrap"
              dateTime={new Date(card.touchedAt).toISOString()}
              title={`上次修改：${fullTime(card.touchedAt)}`}
            >
              {ago(card.touchedAt)}
            </time>
          )}
        </footer>
    </article>
  )
}
