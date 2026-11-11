import { lazy, Suspense, useCallback, useEffect, useState, type ComponentProps } from 'react'
import { go, ui, useUi, useWb } from '../lib/app'
import { resolved, setTheme, useTheme } from '../lib/theme'
import { taskState, taskStateDetail } from '../lib/status'
import { latestRun, projectCards, taskSummary, type CardTask, type ProjectCard } from '../lib/workbench'
import ApprovalCard from './ApprovalCard'
import ArchiveModal from './ArchiveModal'
import Icon from './Icon'
import ProjectCardComponent, { type ProjectTab } from './ProjectCard'
const ProjectModal = lazy(() => import('./ProjectModal'))
const Dispatch = lazy(() => import('./Dispatch'))
import ProjectSetupSheet from './ProjectSetupSheet'
import PhoneHeader from './PhoneHeader'
import TaskRow from './TaskRow'
import { usePhone } from '../lib/phone'
import { Dialog, DialogContent } from './ui/dialog'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'

function Stat({ icon, value, label, tone, onClick, expanded, tip, idle = false, ...rest }: {
  icon: string
  value: string | number
  label: string
  tone: string
  onClick?: () => void
  expanded?: boolean
  /** What the number counts. */
  tip?: string
  /** Clicking does nothing (drawn without the hover ring). */
  idle?: boolean
} & Omit<ComponentProps<'button'>, 'onClick'>) {
  return (
    <button
      {...rest}
      className={`inline-flex items-center gap-1.5 h-7 px-2.5 rounded-[8px] border-0 bg-transparent text-[12.5px] font-semibold tabular-nums transition-colors ${
        onClick && !idle ? 'hover:bg-hover' : 'cursor-default'
      }`}
      onClick={onClick}
      aria-label={`${label}：${value}`}
      aria-haspopup={expanded === undefined ? undefined : 'dialog'}
      aria-expanded={expanded}
      data-tip={tip ?? label}
      data-tip-pos="top"
    >
      <span className={`tone-${tone} grid`} aria-hidden="true"><Icon name={icon} size={15} /></span>
      <span aria-hidden="true">{value}</span>
      <span className="font-medium text-muted" aria-hidden="true">{label}</span>
    </button>
  )
}

/** A summary tile on the phone header: icon and number on one line, the label under them. */
function Tile({ icon, value, label, tone, onClick }: { icon: string; value: string | number; label: string; tone: string; onClick?: () => void }) {
  return (
    <button
      className={`grid gap-0.5 content-start text-left px-2.5 py-1.5 rounded-[12px] border-0 bg-panel shadow-soft min-w-0 transition-transform ${onClick ? 'active:scale-[.97]' : 'cursor-default'}`}
      onClick={onClick}
      aria-label={`${label}：${value}`}
    >
      <span className="flex items-center justify-between gap-1" aria-hidden="true">
        <span className={`grid tone-${tone}`}><Icon name={icon} size={14} /></span>
        <span className="text-[16px] font-semibold tabular-nums leading-none tracking-tight">{value}</span>
      </span>
      <span className="text-[11px] font-medium text-muted truncate" aria-hidden="true">{label}</span>
    </button>
  )
}

type View = 'projects' | 'scene'
const VIEW_KEY = 'sous.wbView'

function storedView(): View {
  try {
    return localStorage.getItem(VIEW_KEY) === 'scene' ? 'scene' : 'projects'
  } catch {
    return 'projects'
  }
}

/** The two ways to look at the workbench: project cards, or the live scene of who is doing what. */
function ViewSwitch({ view, onView }: { view: View; onView: (v: View) => void }) {
  const tab = (v: View, icon: string, text: string, tip: string) => (
    <button
      role="tab"
      aria-selected={view === v}
      className={`h-7 px-2.5 rounded-[8px] inline-flex items-center gap-1.5 text-[13px] border-0 ${view === v ? 'bg-panel shadow-soft font-semibold text-ink' : 'bg-transparent text-muted hover:text-ink'}`}
      data-tip={tip}
      data-tip-pos="bottom"
      onClick={() => onView(v)}
    >
      <Icon name={icon} size={14} />
      {text}
    </button>
  )
  return (
    <div className="inline-flex p-0.5 rounded-[10px] bg-sunken" role="tablist" aria-label="工作台视图">
      {tab('projects', 'folders', '项目', '按项目看任务')}
      {tab('scene', 'users', '现场', '你、主管、agent 和机器之间怎么流转')}
    </div>
  )
}

export default function Workbench() {
  useTheme()
  const theme = resolved()
  const phone = usePhone()
  const wbState = useWb()
  const uiState = useUi()
  const [setup, setSetup] = useState(false)
  const [attention, setAttention] = useState(false)
  const [tabs, setTabs] = useState<Record<string, ProjectTab>>({})
  const [open, setOpen] = useState<{ id: string; from: DOMRect | null; file: string } | null>(null)
  const [archive, setArchive] = useState(false)
  const closeArchive = useCallback(() => setArchive(false), [])
  // "在现场里追踪" links (view 'system') open the scene filtered to one thing.
  const [stored, setStored] = useState<View>(storedView)
  const traced = uiState.route.view === 'system'
  const view: View = traced ? 'scene' : stored
  const setView = (v: View) => {
    setStored(v)
    try {
      localStorage.setItem(VIEW_KEY, v)
    } catch {
      /* the choice just is not remembered */
    }
    if (traced && v === 'projects') go({ view: 'overview', id: '' })
  }
  useEffect(() => {
    if (traced && stored !== 'scene') setStored('scene')
  }, [traced, stored])
  // Redraw every minute so "3 分钟前" on the cards keeps up.
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = window.setInterval(() => setTick((n) => n + 1), 60_000)
    return () => window.clearInterval(t)
  }, [])

  const data = wbState.data
  const inboxTasks = wbState.inbox?.tasks ?? []
  // Every card, archived ones included (the archive dialog and the project
  // dialog use them); the workbench shows only the active ones.
  const allCards = data ? projectCards(data, inboxTasks) : []
  const cards = allCards.filter((c) => !c.archived)
  const archivedCount = allCards.length - cards.length
  const approvals = (wbState.inbox?.approvals ?? []).filter((a) => a.review === 'user')
  const pending = approvals.length + inboxTasks.length
  // "In progress" counts tasks the way the cards do: executing, queued for
  // a run, or being reviewed by a model or an agent. Waiting for the user
  // is in "needs you" instead (end-to-end review 4.19).
  const running = cards.reduce((n, c) => n + c.counts.active, 0)
  const todo = cards.reduce((n, c) => n + c.counts.todo, 0)
  const online = data?.nodes.filter((n) => n.online).length ?? 0
  const projectOf = (id: string) => data?.projects.find((p) => p.id === (id || 'misc'))
  const taskTitle = (id: string) => data?.tasks.find((t) => t.id === id)?.title ?? ''
  // A task link opens its project's dialog with the task selected.
  const routeTask = uiState.route.view === 'task'
    ? data?.tasks.find((t) => t.id === uiState.route.id) ?? wbState.inbox?.tasks.find((t) => t.id === uiState.route.id)
    : undefined
  const modalCard = allCards.find((c) => c.project.id === (routeTask ? routeTask.project_id || 'misc' : open?.id))
  const attentionRows: CardTask[] = data
    ? inboxTasks
      .map((t) => ({ task: t, state: taskState(t), detail: taskStateDetail(t), summary: taskSummary(data, t), stages: [], ...latestRun(data, t.id) }))
      .sort((a, b) => (projectOf(a.task.project_id)?.name ?? '').localeCompare(projectOf(b.task.project_id)?.name ?? '') || b.task.updated_at - a.task.updated_at)
    : []

  // A link to a project chat opens its project's dialog.
  useEffect(() => {
    const id = uiState.openProject
    if (!id || !data) return
    ui.state.openProject = ''
    ui.emit()
    const el = document.querySelector(`[data-project-card="${CSS.escape(id)}"]`)
    setOpen({ id, from: el?.getBoundingClientRect() ?? null, file: '' })
  }, [uiState.openProject, !!data])

  // Old "inbox" links (push notifications) open the "needs you" popover.
  useEffect(() => {
    if (uiState.route.view === 'inbox' && pending > 0) setAttention(true)
  }, [uiState.route.view, pending > 0])

  useEffect(() => { if (pending === 0) setAttention(false) }, [pending])

  const attentionBody = (
    <>
    <h2 className="m-0 flex items-center gap-1.5 text-[12px] font-semibold text-muted">
      <Icon name="hand" size={14} className="text-warm" />要你处理<span className="tabular-nums text-faint">{pending}</span>
    </h2>
    {approvals.map((a) => <ApprovalCard key={a.id} approval={a} context={taskTitle(a.task_id)} />)}
    {attentionRows.length > 0 && (
      <div className="grid gap-0.5">
        {attentionRows.map((t, i) => {
          const p = projectOf(t.task.project_id)
          const first = i === 0 || attentionRows[i - 1].task.project_id !== t.task.project_id
          return (
            <div key={t.task.id} className="grid min-w-0">
            {first && <p className="m-0 mt-1 pl-3 text-[11.5px] font-semibold text-faint">{p?.id === 'misc' ? '杂项' : p?.name ?? '杂项'}</p>}
            <div className="flex items-center gap-1 pl-1 min-w-0">
              <div className="flex-1 min-w-0">
                <TaskRow task={t} onOpen={() => { setAttention(false); go({ view: 'task', id: t.task.id }) }} />
              </div>
            </div>
            </div>
          )
        })}
      </div>
    )}
    </>
  )
  const openSettings = (tab: string) => { ui.state.settings = tab; ui.emit() }
  const openMachines = () => openSettings('machines')

  const renderCard = (card: ProjectCard) => (
    <ProjectCardComponent
      key={card.project.id}
      card={card}
      tab={tabs[card.project.id] ?? 'tasks'}
      onTab={(t) => setTabs((m) => ({ ...m, [card.project.id]: t }))}
      onExpand={(file = '') => {
        const el = document.querySelector(`[data-project-card="${CSS.escape(card.project.id)}"]`)
        if (file) setTabs((m) => ({ ...m, [card.project.id]: 'details' }))
        setOpen({ id: card.project.id, from: el?.getBoundingClientRect() ?? null, file })
      }}
      onOpenTask={(id) => {
        const el = document.querySelector(`[data-project-card="${CSS.escape(card.project.id)}"]`)
        setOpen({ id: card.project.id, from: el?.getBoundingClientRect() ?? null, file: '' })
        go({ view: 'task', id })
      }}
      onOpenProject={(id) => go({ view: 'project', id })}
    />
  )

  const scene = view === 'scene'
  // On a phone the scene's top bar carries the summary: icon and number, the words in the label.
  const chip = (icon: string, value: string | number, label: string, tone: string, onClick?: () => void) => (
    <button
      className={`shrink-0 inline-flex items-center gap-1 h-8 px-[5px] rounded-[9px] border-0 bg-transparent text-[13px] font-semibold tabular-nums ${onClick ? 'active:bg-hover' : 'cursor-default'}`}
      aria-label={`${label}：${value}`}
      onClick={onClick}
    >
      <span className={`grid tone-${tone}`} aria-hidden="true"><Icon name={icon} size={14} /></span>
      <span aria-hidden="true">{value}</span>
    </button>
  )
  const phoneStats = data && (
    <div className="flex items-center" aria-label="概况">
      {chip('play', running, '进行中', 'accent')}
      {chip('hand', pending, '要你处理', 'warning', pending ? () => setAttention(true) : undefined)}
      {chip('circle-dashed', todo, '待办', 'neutral')}
      {chip('laptop', `${online}/${data.nodes.length}`, '机器在线', 'success', openMachines)}
    </div>
  )
  return (
    // The scene fills the work area exactly (nothing scrolls); the cards scroll as before.
    // On a desktop both views fill the work area: content above, the status bar
    // pinned at the bottom. A phone's project list scrolls the page as before.
    <div className={scene || !phone ? 'h-full min-h-0 flex flex-col' : 'contents'}>
      {phone && (
        <PhoneHeader
          title="工作台"
          compact
          leading={<ViewSwitch view={view} onView={setView} />}
          actions={
            <>
              <button className="phone-bar-btn" aria-label={`打开已归档的项目（${archivedCount} 个）`} onClick={() => setArchive(true)}>
                <Icon name="folder-open" size={22} />
              </button>
              <button className="phone-bar-btn" aria-label="新建或登记项目" onClick={() => setSetup(true)}>
                <Icon name="plus" size={24} />
              </button>
            </>
          }
        >
          {data && !scene && (
            <div className="grid grid-cols-4 gap-1.5 px-3 pb-1" aria-label="概况">
              <Tile icon="play" value={running} label="进行中" tone="accent" />
              <Tile icon="hand" value={pending} label="要你处理" tone="warning" onClick={pending ? () => setAttention(true) : undefined} />
              <Tile icon="circle-dashed" value={todo} label="待办" tone="neutral" />
              <Tile icon="laptop" value={`${online}/${data.nodes.length}`} label="机器在线" tone="success" onClick={openMachines} />
            </div>
          )}
        </PhoneHeader>
      )}
      <div
        className={
          scene
            ? 'flex-1 min-h-0 flex flex-col gap-4 px-7 pt-5 pb-4 min-w-0 max-[760px]:px-3 max-[760px]:pt-2 max-[760px]:pb-2'
            : phone
              ? 'px-4 pt-4 pb-8 grid gap-4 content-start min-w-0'
              : 'flex-1 min-h-0 overflow-y-auto overscroll-contain px-7 pt-5 pb-8 grid gap-6 content-start min-w-0'
        }
      >
        <header className="flex items-center flex-wrap gap-2.5 min-h-9 max-[760px]:hidden">
          <img src="/icon.svg" alt="" width={26} height={26} className="rounded-[8px]" />
          <h1 className="text-[18px] font-semibold tracking-tight m-0 whitespace-nowrap">工作台</h1>
          <ViewSwitch view={view} onView={setView} />
          <span className="flex-1"></span>
          <button className="btn small ghost" aria-label="打开已归档的项目" data-tip={`已归档 ${archivedCount} 个`} data-tip-pos="bottom" onClick={() => setArchive(true)}>
            <Icon name="folder-open" size={16} />打开
          </button>
          <button className="btn small" aria-label="新建或登记项目" onClick={() => setSetup(true)}>
            <Icon name="plus" size={16} />项目
          </button>
          <span className="w-px h-5 bg-line mx-1" aria-hidden="true" />
          <button
            className={`icon-btn ${uiState.notesOpen ? 'solid' : ''}`}
            aria-label="记事本"
            aria-pressed={uiState.notesOpen}
            data-tip="记事本（⌘⇧N）"
            data-tip-pos="bottom"
            onClick={() => {
              ui.state.notesOpen = !ui.state.notesOpen
              ui.emit()
            }}
          >
            <Icon name="note" size={18} />
          </button>
          <button className="icon-btn" aria-label={theme === 'dark' ? '切换到浅色' : '切换到深色'} data-tip={theme === 'dark' ? '切换到浅色' : '切换到深色'} data-tip-pos="bottom" onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>
            <Icon name={theme === 'dark' ? 'sun' : 'moon'} size={18} />
          </button>
          <button className="icon-btn" aria-label="设置" data-tip="设置" data-tip-pos="bottom" onClick={() => openSettings('machines')}>
            <Icon name="settings" size={18} />
          </button>
          {!uiState.chatOpen && (
            <button
              className="icon-btn"
              aria-label="打开对话"
              data-tip="打开对话"
              data-tip-pos="bottom"
              onClick={() => {
                ui.state.chatOpen = true
                ui.emit()
              }}
            >
              <Icon name="panel" size={18} />
            </button>
          )}
        </header>

        {view === 'scene' ? (
          <section aria-label="现场" className="flex-1 min-h-0 rounded-[16px] bg-panel shadow-soft overflow-hidden max-[760px]:rounded-[22px]">
            <Suspense fallback={null}>
              <Dispatch trace={traced ? uiState.route.id : ''} lead={phone && data ? phoneStats : undefined} />
            </Suspense>
          </section>
        ) : wbState.error && !data ? (
          <p className="text-danger flex items-center gap-2"><Icon name="alert" size={16} />{wbState.error}</p>
        ) : !data ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-3.5" aria-label="加载中">
            {[0, 1, 2].map((i) => <div key={i} className="h-44 rounded-[14px] bg-panel shadow-soft opacity-60 animate-pulse"></div>)}
          </div>
        ) : !cards.length ? (
          <div className="grid justify-items-center gap-2 pt-[10dvh] px-4 text-muted text-center rise">
            <span className="grid place-items-center w-14 h-14 rounded-[16px] bg-sunken text-muted">
              <Icon name="folders" size={26} />
            </span>
            <p className="text-[15px] text-ink font-semibold mt-2 mb-0">还没有项目</p>
            <button className="btn mt-2" onClick={() => setSetup(true)}><Icon name="plus" size={15} />新建或登记</button>
          </div>
        ) : (
          <section aria-label="项目">
            <div className="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] auto-rows-fr gap-4 max-[760px]:grid-cols-1 max-[760px]:auto-rows-auto max-[760px]:gap-3.5">
              {cards.map(renderCard)}
            </div>
          </section>
        )}
      </div>
      {/* The status bar: the machines, then what is running, needs you and is waiting. */}
      {!phone && data && (
        <footer className="shrink-0 h-10 flex items-center gap-1 px-4 border-t border-line bg-page/90 backdrop-blur" aria-label="状态">
          <div className="flex items-center gap-0.5 min-w-0 flex-1 overflow-hidden" aria-label="机器">
            {data.nodes.map((n) => {
              const busy = data.runs.filter((r) => r.node_id === n.id && r.status === 'running').length
              return (
                <button
                  key={n.id}
                  className="shrink-0 inline-flex items-center gap-1.5 h-7 px-2 rounded-[8px] border-0 bg-transparent text-[12.5px] text-muted hover:bg-hover hover:text-ink"
                  data-tip={`${n.name} · ${n.online ? '在线' : '离线'}${busy ? ` · ${busy} 个在跑` : ''}`}
                  aria-label={`${n.name}${n.online ? '' : '（离线）'}${busy ? `，${busy} 个在跑` : ''}`}
                  onClick={openMachines}
                >
                  <span className="relative grid">
                    <Icon name={n.role === 'server' ? 'server' : 'laptop'} size={15} />
                    <span className={`absolute -bottom-0.5 -right-0.5 w-[7px] h-[7px] rounded-full shadow-[0_0_0_1.5px_var(--bg)] ${n.online ? (busy ? 'bg-info live-dot' : 'bg-[var(--online)]') : 'bg-faint'}`}></span>
                  </span>
                  <span className={`max-w-[120px] truncate ${n.online ? '' : 'text-faint'}`}>{n.name}</span>
                </button>
              )
            })}
          </div>
          {data && (
            <div className="flex items-center gap-0.5 shrink-0" aria-label="概况">
              <Stat icon="play" value={running} label="进行中" tone="accent" tip="正在执行、排队或由模型 / agent 验收的任务" />
              {/* Radix places it: above the button, or slid sideways to stay on screen. */}
              <Popover open={attention && pending > 0 && !phone} onOpenChange={(o) => setAttention(o && pending > 0)}>
                <PopoverTrigger asChild>
                  <Stat icon="hand" value={pending} label="要你处理" tone="warning" tip="等你审批、验收，或受阻、失败、等机器的任务" expanded={attention && pending > 0} idle={!pending} />
                </PopoverTrigger>
                <PopoverContent
                  side="top"
                  align="end"
                  sideOffset={8}
                  aria-label="要你处理"
                  className="w-[min(440px,calc(100vw-16px))] max-h-[min(70dvh,var(--radix-popover-content-available-height))] bg-panel rounded-[var(--radius)] p-3 grid gap-2.5 content-start"
                >
                  {attentionBody}
                </PopoverContent>
              </Popover>
              <Stat icon="circle-dashed" value={todo} label="待办" tone="neutral" />
              <Stat icon="laptop" value={`${online}/${data.nodes.length}`} label="机器在线" tone="success" onClick={openMachines} />
            </div>
          )}
        </footer>
      )}
      {phone && attention && pending > 0 && (
        <Dialog open onOpenChange={(open) => { if (!open) setAttention(false) }}>
          <DialogContent className="sheet !gap-2.5" aria-label="要你处理">
            <div className="grid gap-2.5 overflow-y-auto overscroll-contain -mx-1 px-1">{attentionBody}</div>
          </DialogContent>
        </Dialog>
      )}
      {archive && <ArchiveModal cards={allCards} renderCard={renderCard} onClose={closeArchive} />}
      {modalCard && (
        <Suspense fallback={null}>
        <ProjectModal
          key={modalCard.project.id}
          card={modalCard}
          from={open?.from ?? null}
          tab={routeTask ? 'tasks' : tabs[modalCard.project.id] ?? 'tasks'}
          onTab={(t) => {
            const id = modalCard.project.id
            setTabs((m) => ({ ...m, [id]: t }))
            if (routeTask) {
              setOpen({ id, from: open?.from ?? null, file: '' })
              go({ view: 'overview', id: '' })
            }
          }}
          file={open?.file ?? ''}
          onFile={(file) => setOpen({ id: modalCard.project.id, from: open?.from ?? null, file })}
          taskId={routeTask?.id ?? ''}
          onTask={(id) => {
            if (id) return go({ view: 'task', id })
            setOpen({ id: modalCard.project.id, from: open?.from ?? null, file: '' })
            go({ view: 'overview', id: '' })
          }}
          onClose={() => {
            setOpen(null)
            if (routeTask) go({ view: 'overview', id: '' })
          }}
        />
        </Suspense>
      )}
      {setup && <ProjectSetupSheet onClose={() => setSetup(false)} />}
    </div>
  )
}
