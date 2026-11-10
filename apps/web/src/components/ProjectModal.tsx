import { frame, useAnimate } from 'motion/react'
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

// The dialog's animations run on Motion (motion.dev), with the curves of the hand-written ones they replace.
type Ease = [number, number, number, number]
const GROW = { duration: 0.26, ease: [0.2, 0.85, 0.25, 1] as Ease }
const SHRINK = { duration: 0.22, ease: [0.4, 0, 0.6, 1] as Ease }
const PUSH = { duration: 0.42, ease: [0.2, 0.9, 0.25, 1] as Ease }
const POP = { duration: 0.26, ease: [0.3, 0.7, 0.4, 1] as Ease }
/** Keyframes that move a box from one rectangle to another. */
const between = (a: DOMRect, b: DOMRect) => ({
  left: [a.left, b.left], top: [a.top, b.top], width: [a.width, b.width], height: [a.height, b.height],
})
/** No animation at all when the system asks for reduced motion: open in place, close at once. */
const motion = () => !window.matchMedia('(prefers-reduced-motion: reduce)').matches
/** How far a swipe (or an animation) has moved the page to the right. */
function shiftOf(el: HTMLElement): number {
  const m = /^matrix\((.+)\)$/.exec(getComputedStyle(el).transform)
  return m ? parseFloat(m[1].split(',')[4]) || 0 : 0
}
/** Hands the box back to its classes once Motion has written its last frame. */
function release(el: HTMLElement, ...props: ('left' | 'top' | 'width' | 'height' | 'borderRadius' | 'transform')[]) {
  frame.postRender(() => { for (const p of props) el.style[p] = '' })
}

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
  // The panel is Motion's scope: its animations stop if the dialog goes away mid-way.
  const [panel, animate] = useAnimate<HTMLDivElement>()
  const backdrop = useRef<HTMLDivElement>(null)
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
    const bd = backdrop.current
    if (el && motion()) {
      if (phone) {
        // A pushed page: in from the right, the backdrop fading in with it.
        const w = el.offsetWidth
        el.style.transform = `translateX(${w}px)`
        void animate(el, { x: [w, 0] }, PUSH).then(() => release(el, 'transform'))
        if (bd) void animate(bd, { opacity: [0, 1] }, { duration: PUSH.duration * 0.7, ease: 'easeOut' })
      } else if (from) {
        // Grows out of the card it was opened from, then goes back to its classes (they follow resizes).
        const to = el.getBoundingClientRect()
        Object.assign(el.style, { left: `${from.left}px`, top: `${from.top}px`, width: `${from.width}px`, height: `${from.height}px`, borderRadius: '16px' })
        void animate(el, { ...between(from, to), borderRadius: [16, 18] }, GROW).then(() => {
          if (!closing.current) release(el, 'left', 'top', 'width', 'height', 'borderRadius')
        })
        if (bd) void animate(bd, { opacity: [0, 1] }, { duration: 0.18, ease: 'easeOut' })
      }
    }
    setShown(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function close() {
    const el = panel.current
    const bd = backdrop.current
    // Once: a second close while one animates does nothing.
    if (closing.current) return
    closing.current = true
    if (!el || !motion()) return onClose()
    if (phone) {
      // Slide out to the right, from wherever an edge swipe left it (its CSS transition would smear every frame).
      el.style.transition = 'none'
      void animate(el, { x: [shiftOf(el), el.offsetWidth] }, POP).then(onClose)
      if (bd) void animate(bd, { opacity: 0 }, POP)
      return
    }
    // Shrinks back into its card.
    const cardEl = document.querySelector<HTMLElement>(`[data-project-card="${CSS.escape(p.id)}"]`)
    if (!cardEl) return onClose()
    void animate(el, { ...between(el.getBoundingClientRect(), cardEl.getBoundingClientRect()), borderRadius: [18, 16] }, SHRINK).then(onClose)
    if (bd) void animate(bd, { opacity: 0 }, SHRINK)
  }

  // Phone: the project chat is a page pushed in from the right.
  const chatPage = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const el = chatPage.current
    if (!chatOpen || !phone || !el || !motion()) return
    const w = el.offsetWidth
    el.style.transform = `translateX(${w}px)`
    void animate(el, { x: [w, 0] }, PUSH).then(() => release(el, 'transform'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chatOpen])

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
      {/* Motion fades this backdrop with the panel, in place of .backdrop's CSS fade-in. */}
      <div ref={backdrop} className="backdrop" style={{ animation: 'none' }} role="presentation" onClick={close}></div>
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={`项目 ${p.name}`}
        className={`fixed z-[31] overflow-hidden bg-panel shadow-pop grid left-[calc((100vw-var(--pm-w))/2)] top-[5dvh] h-[90dvh] w-[var(--pm-w)] rounded-[18px] max-[760px]:inset-0 max-[760px]:left-0 max-[760px]:top-0 max-[760px]:h-full max-[760px]:w-full max-[760px]:rounded-none max-[760px]:shadow-none`}
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
            ref={chatPage}
            className="relative min-w-0 min-h-0 bg-chrome border-l border-line max-[760px]:absolute max-[760px]:inset-0 max-[760px]:z-10 max-[760px]:border-l-0 max-[760px]:bg-page"
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
