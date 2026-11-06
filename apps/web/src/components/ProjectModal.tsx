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
import { usePhone } from '../lib/phone'
import { useStoredWidth } from '../lib/resize'
import { ArchivedBadge, ProjectTabs, type ProjectTab } from './ProjectCard'
import { BackButton } from './PhoneHeader'
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
  const phone = usePhone()
  // The project chat is docked on the right and starts closed; the title bar's button opens it.
  const [chatOpen, setChatOpen] = useState(false)
  const [chatWidth, setChatWidth, resetChatWidth] = useStoredWidth('sous.projectChatWidth', 400)
  const docked = chatOpen && !phone
  const selectedTask = taskId ? card.listTasks.find((t) => t.task.id === taskId)?.task : undefined

  useLayoutEffect(() => {
    const el = panel.current
    // On a phone it is a pushed page (.push-in), not a card growing.
    if (el && from && motion() && !phone) {
      const to = el.getBoundingClientRect()
      el.animate([rectOf(from), rectOf(to)], { duration: 260, easing: 'cubic-bezier(.2,.85,.25,1)' })
    }
    setShown(true)
  }, [])

  function close() {
    const el = panel.current
    if (closing.current) return
    closing.current = true
    if (phone) {
      // Slide out to the right, from wherever an edge swipe left it.
      if (!el || !motion()) return onClose()
      const now = getComputedStyle(el).transform
      const a = el.animate([{ transform: now === 'none' ? 'translateX(0)' : now }, { transform: 'translateX(100%)' }], {
        duration: 260, easing: 'cubic-bezier(.3,.7,.4,1)', fill: 'forwards',
      })
      el.parentElement?.querySelector('.backdrop')?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 260, fill: 'forwards' })
      a.onfinish = onClose
      return
    }
    const cardEl = document.querySelector<HTMLElement>(`[data-project-card="${CSS.escape(p.id)}"]`)
    if (!el || !cardEl || !motion()) return onClose()
    const a = el.animate([rectOf(el.getBoundingClientRect()), rectOf(cardEl.getBoundingClientRect())], {
      duration: 220, easing: 'cubic-bezier(.4,0,.6,1)', fill: 'forwards',
    })
    el.parentElement?.querySelector('.backdrop')?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 220, fill: 'forwards' })
    a.onfinish = onClose
  }

  useEffect(() => {
    // On a phone Escape (and the edge swipe, through it) steps back one level, like the bar's back button.
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') (phone ? back.go : close)() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  const files = tab === 'details' && p.id !== 'misc'
  // Phone: the bar's back button steps back one level at a time, like a navigation stack.
  const back = chatOpen
    ? { label: '收起项目对话', go: () => setChatOpen(false) }
    : tab === 'tasks' && taskId
      ? { label: '返回任务列表', go: () => onTask('') }
      : files && file
        ? { label: '返回文件列表', go: () => onFile('') }
        : { label: '返回工作台', go: close }
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
        className={`fixed z-[31] overflow-hidden bg-panel shadow-pop grid left-[calc((100vw-var(--pm-w))/2)] top-[5dvh] h-[90dvh] w-[var(--pm-w)] rounded-[18px] max-[760px]:inset-0 max-[760px]:left-0 max-[760px]:top-0 max-[760px]:h-full max-[760px]:w-full max-[760px]:rounded-none max-[760px]:shadow-none ${phone ? 'push-in' : ''}`}
        style={{
          visibility: shown ? 'visible' : 'hidden',
          gridTemplateColumns: docked ? `minmax(0,1fr) clamp(300px, ${chatWidth}px, 50%)` : 'minmax(0,1fr)',
          '--pm-w': docked ? 'min(1560px, calc(100vw - 32px))' : 'min(1120px, calc(100vw - 32px))',
        } as React.CSSProperties}
      >
        <div className="grid grid-rows-[auto_minmax(0,1fr)] min-w-0 min-h-0">
        {phone ? (
          <header className="grid gap-3 px-3 pt-[calc(var(--safe-t)+6px)] pb-3 min-w-0">
            <div className="flex items-center gap-2 min-w-0">
              <BackButton label={back.label} onClick={back.go} />
              <div className="flex-1 min-w-0 flex items-center justify-center gap-2">
                <h2 className="m-0 text-[17px] font-semibold tracking-tight truncate">{p.name}</h2>
                {card.archived && <ArchivedBadge />}
              </div>
              <div className="glass-group glass">
                <button className={`phone-bar-btn ${chatOpen ? '!text-accent' : ''}`} aria-label={chatOpen ? '收起项目对话' : '和 sous 聊这个项目'} aria-pressed={chatOpen} onClick={() => setChatOpen(!chatOpen)}>
                  <Icon name="chat" size={20} />
                </button>
                {p.id !== 'misc' && (card.archived ? (
                  <button className="phone-bar-btn" aria-label="激活项目" onClick={() => void setArchived(p, false)}><Icon name="unarchive" size={20} /></button>
                ) : (
                  <button className="phone-bar-btn" aria-label="归档项目" onClick={() => { close(); void setArchived(p, true) }}><Icon name="archive" size={20} /></button>
                ))}
                <button className="phone-bar-btn" aria-label="项目详情" onClick={() => { close(); go({ view: 'project', id: p.id }) }}><Icon name="more" size={22} /></button>
              </div>
            </div>
            {p.id !== 'misc' && <div className="flex justify-center min-w-0"><Locations locations={card.locations} compact /></div>}
            <ProjectTabs tab={tab} onTab={onTab} tasks={card.allTasks.length + card.allAgents.length} shell={p.id !== 'misc'} />
          </header>
        ) : (
          <header className="flex items-center gap-4 h-[54px] px-5 min-w-0">
            <div className="flex items-center gap-3 min-w-0 flex-1">
              <h2 className="m-0 text-[17px] font-semibold tracking-tight overflow-hidden text-ellipsis whitespace-nowrap flex-none max-w-[40%]">{p.name}</h2>
              {card.archived && <ArchivedBadge />}
              {p.id !== 'misc' && <div className="min-w-0 overflow-hidden"><Locations locations={card.locations} compact /></div>}
            </div>
            <div className={`${p.id === 'misc' ? 'w-[220px]' : 'w-[300px]'} flex-none`}>
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
        )}

        {shellSeen && (
          <div className={shell ? 'min-h-0' : 'hidden'}>
            <ShellPanel project={p} />
          </div>
        )}
        {shell ? null : files ? (
          <div className="grid grid-cols-[272px_minmax(0,1fr)] min-h-0 border-t border-line max-[760px]:grid-cols-1">
            <div className={`overflow-y-auto p-2.5 border-r border-line min-h-0 max-[760px]:border-r-0 max-[760px]:pb-[calc(16px+var(--safe-b))] ${file ? 'max-[760px]:hidden' : ''}`}>
              <FileTree projectId={p.id} selected={file} onSelect={onFile} />
            </div>
            <div className="min-h-0" data-page={phone && file ? '' : undefined}>
              {file ? (
                <>
                  {phone && <button className="sr-only" tabIndex={-1} aria-hidden="true" data-back onClick={() => onFile('')}></button>}
                  <FileEditor projectId={p.id} path={file} />
                </>
              ) : (
                <div className="grid place-items-center h-full text-muted text-[13px] max-[760px]:hidden">
                  <span className="grid justify-items-center gap-2"><Icon name="file" size={28} />选一个文件来查看或编辑</span>
                </div>
              )}
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-[340px_minmax(0,1fr)] min-h-0 border-t border-line max-[760px]:grid-cols-1">
            <div className={`overflow-y-auto p-2.5 border-r border-line min-h-0 max-[760px]:border-r-0 max-[760px]:px-3 max-[760px]:pb-[calc(16px+var(--safe-b))] grid gap-2 content-start ${taskId ? 'max-[760px]:hidden' : ''}`}>
              <button
                className="flex items-center justify-center gap-1.5 h-10 rounded-[12px] border border-dashed border-line-strong bg-transparent text-muted text-[13px] hover:text-muted hover:border-accent"
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
            <div className={`min-h-0 ${taskId ? 'overflow-hidden' : 'overflow-y-auto p-5 max-[760px]:px-4 max-[760px]:pb-[calc(20px+var(--safe-b))]'}`} data-page={phone && taskId ? '' : undefined}>
              {taskId ? (
                <>
                  {/* For the edge swipe: it slides just this page back to the list. */}
                  {phone && <button className="sr-only" tabIndex={-1} aria-hidden="true" data-back onClick={() => onTask('')}></button>}
                  <TaskView key={taskId} id={taskId} />
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
            className={`relative min-w-0 min-h-0 bg-chrome border-l border-line max-[760px]:absolute max-[760px]:inset-0 max-[760px]:z-10 max-[760px]:border-l-0 max-[760px]:bg-page ${phone ? 'push-in' : ''}`}
            aria-label="项目对话"
            data-page={phone ? '' : undefined}
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
