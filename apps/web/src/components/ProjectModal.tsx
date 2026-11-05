import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { go, ui } from '../lib/app'
import { setArchived } from '../lib/projects'
import type { ProjectCard } from '../lib/workbench'
import AgentCard from './AgentCard'
import FileEditor from './FileEditor'
import FileTree from './FileTree'
import Icon from './Icon'
import Locations from './Locations'
import ProjectChat from './ProjectChat'
import ResizeHandle from './ResizeHandle'
import { useStoredWidth } from '../lib/resize'
import { ArchivedBadge, ProjectTabs, type ProjectTab } from './ProjectCard'
import ProjectTile from './ProjectTile'
import ShellPanel from './ShellPanel'
import TaskRow from './TaskRow'
import TaskView from './TaskView'

const rectOf = (r: DOMRect) => ({ left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px`, borderRadius: '16px' })
const motion = () => !window.matchMedia('(prefers-reduced-motion: reduce)').matches

/**
 * A project card opened as a dialog. It grows out of the card and shrinks back
 * into it, and shows the same tab the card showed.
 */
export default function ProjectModal({ card, from, tab, onTab, file, onFile, taskId, onTask, onClose }: {
  card: ProjectCard
  /** Where the card was when it was opened. */
  from: DOMRect | null
  tab: ProjectTab
  onTab: (t: ProjectTab) => void
  file: string
  onFile: (f: string) => void
  /** The task shown on the right of the tasks tab ('' for none). */
  taskId: string
  onTask: (id: string) => void
  onClose: () => void
}) {
  const panel = useRef<HTMLDivElement>(null)
  const closing = useRef(false)
  const [shown, setShown] = useState(false)
  const p = card.project
  const phone = typeof matchMedia !== 'undefined' && matchMedia('(max-width: 760px)').matches
  // The project chat is docked on the right; remembered open or closed (closed on phones).
  const [chatOpen, setChatOpenState] = useState(() => {
    if (phone) return false
    try {
      return localStorage.getItem('sous.projectChat') !== 'closed'
    } catch {
      return true
    }
  })
  const setChatOpen = (v: boolean) => {
    setChatOpenState(v)
    if (phone) return
    try {
      localStorage.setItem('sous.projectChat', v ? 'open' : 'closed')
    } catch {
      // ignore
    }
  }
  const [chatWidth, setChatWidth, resetChatWidth] = useStoredWidth('sous.projectChatWidth', 400)
  const docked = chatOpen && !phone
  const selectedTask = taskId ? card.listTasks.find((t) => t.task.id === taskId)?.task : undefined

  useLayoutEffect(() => {
    const el = panel.current
    if (el && from && motion()) {
      const to = el.getBoundingClientRect()
      el.animate([rectOf(from), rectOf(to)], { duration: 260, easing: 'cubic-bezier(.2,.85,.25,1)' })
    }
    setShown(true)
  }, [])

  function close() {
    const el = panel.current
    if (closing.current) return
    closing.current = true
    const cardEl = document.querySelector<HTMLElement>(`[data-project-card="${CSS.escape(p.id)}"]`)
    if (!el || !cardEl || !motion()) return onClose()
    const a = el.animate([rectOf(el.getBoundingClientRect()), rectOf(cardEl.getBoundingClientRect())], {
      duration: 220, easing: 'cubic-bezier(.4,0,.6,1)', fill: 'forwards',
    })
    el.parentElement?.querySelector('.backdrop')?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 220, fill: 'forwards' })
    a.onfinish = onClose
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  const files = tab === 'details' && p.id !== 'misc'
  const shell = tab === 'shell' && p.id !== 'misc'
  // Once opened, the terminals stay mounted while other tabs show, so their screens survive.
  const [shellSeen, setShellSeen] = useState(shell)
  useEffect(() => { if (shell) setShellSeen(true) }, [shell])

  return (
    <>
      <div className="backdrop" role="presentation" onClick={close}></div>
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={`项目 ${p.name}`}
        className="fixed z-[31] overflow-hidden bg-panel shadow-pop grid left-[calc((100vw-var(--pm-w))/2)] top-[5vh] h-[90vh] w-[var(--pm-w)] rounded-[18px] max-[760px]:inset-0 max-[760px]:left-0 max-[760px]:top-0 max-[760px]:h-full max-[760px]:w-full max-[760px]:rounded-none max-[760px]:pt-[env(safe-area-inset-top)] max-[760px]:pb-[env(safe-area-inset-bottom)]"
        style={{
          visibility: shown ? 'visible' : 'hidden',
          gridTemplateColumns: docked ? `minmax(0,1fr) clamp(300px, ${chatWidth}px, 50%)` : 'minmax(0,1fr)',
          '--pm-w': docked ? 'min(1560px, calc(100vw - 32px))' : 'min(1120px, calc(100vw - 32px))',
        } as React.CSSProperties}
      >
        <div className="grid grid-rows-[auto_minmax(0,1fr)] min-w-0 min-h-0">
        <header className="flex items-center gap-4 px-5 pt-4 pb-3 min-w-0 flex-wrap max-[760px]:gap-x-2 max-[760px]:gap-y-3 max-[760px]:px-3 max-[760px]:pt-2">
          <ProjectTile name={p.name} id={p.id} size={40} misc={p.id === 'misc'} />
          <div className="grid gap-1 min-w-0 flex-1">
            <div className="flex items-center gap-2 min-w-0">
              <h2 className="m-0 text-[17px] font-semibold tracking-tight overflow-hidden text-ellipsis whitespace-nowrap">{p.name}</h2>
              {card.archived && <ArchivedBadge />}
            </div>
            {p.id !== 'misc' && <Locations locations={card.locations} compact />}
          </div>
          <div className={`${p.id === 'misc' ? 'w-[220px]' : 'w-[300px]'} max-[760px]:order-last max-[760px]:w-full`}>
            <ProjectTabs tab={tab} onTab={onTab} tasks={card.allTasks.length + card.allAgents.length} shell={p.id !== 'misc'} />
          </div>
          {p.id !== 'misc' && (card.archived ? (
            <button className="icon-btn" aria-label="激活项目" data-tip="激活，放回工作台" data-tip-pos="bottom" onClick={() => void setArchived(p, false)}>
              <Icon name="unarchive" size={18} />
            </button>
          ) : (
            <button className="icon-btn" aria-label="归档项目" data-tip="归档" data-tip-pos="bottom" onClick={() => { close(); void setArchived(p, true) }}>
              <Icon name="archive" size={18} />
            </button>
          ))}
          <button
            className={`icon-btn ${chatOpen ? 'text-accent' : ''}`}
            aria-label={chatOpen ? '收起项目对话' : '和 sous 聊这个项目'}
            aria-pressed={chatOpen}
            data-tip={chatOpen ? '收起对话' : '和 sous 聊这个项目'}
            data-tip-pos="bottom"
            onClick={() => setChatOpen(!chatOpen)}
          >
            <Icon name="chat" size={18} />
          </button>
          <button className="icon-btn" aria-label="项目详情" data-tip="项目详情" data-tip-pos="bottom" onClick={() => { close(); go({ view: 'project', id: p.id }) }}>
            <Icon name="more" size={20} />
          </button>
          <button className="icon-btn bg-sunken rounded-full" aria-label="关闭" onClick={close}><Icon name="x" size={16} /></button>
        </header>

        {shellSeen && (
          <div className={shell ? 'min-h-0' : 'hidden'}>
            <ShellPanel project={p} />
          </div>
        )}
        {shell ? null : files ? (
          <div className="grid grid-cols-[272px_minmax(0,1fr)] min-h-0 border-t border-line max-[760px]:grid-cols-1">
            <div className={`overflow-y-auto p-2.5 border-r border-line min-h-0 max-[760px]:border-r-0 ${file ? 'max-[760px]:hidden' : ''}`}>
              <FileTree projectId={p.id} selected={file} onSelect={onFile} />
            </div>
            <div className="min-h-0">
              {file ? (
                <FileEditor projectId={p.id} path={file} onBack={() => onFile('')} />
              ) : (
                <div className="grid place-items-center h-full text-muted text-[13px] max-[760px]:hidden">
                  <span className="grid justify-items-center gap-2"><Icon name="file" size={28} />选一个文件来查看或编辑</span>
                </div>
              )}
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-[340px_minmax(0,1fr)] min-h-0 border-t border-line max-[760px]:grid-cols-1">
            <div className={`overflow-y-auto p-2.5 border-r border-line min-h-0 max-[760px]:border-r-0 grid gap-2 content-start ${taskId ? 'max-[760px]:hidden' : ''}`}>
              <button
                className="flex items-center justify-center gap-1.5 h-10 rounded-[12px] border border-dashed border-line-strong bg-transparent text-muted text-[13px] hover:text-accent hover:border-accent"
                onClick={() => { ui.state.newTask = p.id; ui.emit() }}
              >
                <Icon name="plus" size={16} />新任务
              </button>
              {card.allAgents.filter((a) => !a.task).map((a) => (
                <AgentCard key={a.run.id} agent={a} onOpen={() => { ui.state.runSheet = a.run.id; ui.emit() }} />
              ))}
              <div className="grid gap-0.5" aria-label="任务">
                {card.listTasks.map((t) => <TaskRow key={t.task.id} task={t} selected={t.task.id === taskId} onOpen={() => onTask(t.task.id)} />)}
              </div>
            </div>
            <div className="overflow-y-auto p-5 min-h-0">
              {taskId ? (
                <>
                  <button className="btn small ghost mb-3 min-[761px]:hidden" onClick={() => onTask('')}><Icon name="back" size={14} />任务列表</button>
                  <TaskView key={taskId} id={taskId} embedded />
                </>
              ) : (
                <div className="grid place-items-center h-full text-muted text-[13px] max-[760px]:hidden">
                  <span className="grid justify-items-center gap-2"><Icon name="target" size={28} />选一个任务来查看详情</span>
                </div>
              )}
            </div>
          </div>
        )}
        </div>
        {chatOpen && (
          <aside
            className="relative min-w-0 min-h-0 bg-chrome border-l border-line max-[760px]:absolute max-[760px]:inset-0 max-[760px]:z-10 max-[760px]:border-l-0"
            aria-label="项目对话"
          >
            {docked && (
              <ResizeHandle
                label="调整项目对话宽度"
                width={chatWidth}
                min={300}
                max={() => Math.min(760, (panel.current?.getBoundingClientRect().width ?? window.innerWidth) - 520)}
                onWidth={setChatWidth}
                onReset={resetChatWidth}
              />
            )}
            <ProjectChat project={p} task={selectedTask} onClose={() => setChatOpen(false)} />
          </aside>
        )}
      </div>
    </>
  )
}
