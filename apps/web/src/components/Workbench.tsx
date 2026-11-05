import { useCallback, useEffect, useRef, useState } from 'react'
import { go, ui, useUi, useWb } from '../lib/app'
import { taskState, taskStateDetail } from '../lib/status'
import { latestRun, projectCards, taskSummary, type CardTask, type ProjectCard } from '../lib/workbench'
import ApprovalCard from './ApprovalCard'
import ArchiveModal from './ArchiveModal'
import Icon from './Icon'
import ProjectCardComponent, { type ProjectTab } from './ProjectCard'
import ProjectModal from './ProjectModal'
import ProjectSetupSheet from './ProjectSetupSheet'
import ProjectTile from './ProjectTile'
import PhoneHeader from './PhoneHeader'
import TaskRow from './TaskRow'
import { usePhone } from '../lib/phone'

function Stat({ icon, value, label, tone, onClick, expanded }: {
  icon: string
  value: string | number
  label: string
  tone: string
  onClick?: () => void
  expanded?: boolean
}) {
  return (
    <button
      className={`inline-flex items-center gap-1.5 h-9 px-3 rounded-[12px] border-0 bg-panel shadow-soft text-[13px] font-semibold tabular-nums transition-shadow ${
        onClick ? 'hover:shadow-[0_0_0_1px_var(--line-strong)]' : 'cursor-default'
      }`}
      onClick={onClick}
      aria-label={`${label} ${value}`}
      aria-haspopup={expanded === undefined ? undefined : 'dialog'}
      aria-expanded={expanded}
      data-tip={label}
      data-tip-pos="bottom"
    >
      <span className={`tone-${tone} grid`}><Icon name={icon} size={16} /></span>
      {value}
    </button>
  )
}

/** A summary tile on the phone header: icon, a big number and a label, after iOS Reminders' smart lists. */
function Tile({ icon, value, label, tone, onClick }: { icon: string; value: string | number; label: string; tone: string; onClick?: () => void }) {
  return (
    <button
      className={`grid gap-1 content-start text-left p-2.5 rounded-[14px] border-0 bg-panel shadow-soft min-w-0 transition-transform ${onClick ? 'active:scale-[.97]' : 'cursor-default'}`}
      onClick={onClick}
      aria-label={`${label} ${value}`}
    >
      <span className="flex items-center justify-between gap-1">
        <span className={`grid place-items-center w-7 h-7 rounded-full tone-${tone} bg-[color-mix(in_srgb,currentColor_14%,transparent)]`}><Icon name={icon} size={16} /></span>
        <span className="text-[20px] font-semibold tabular-nums leading-none tracking-tight">{value}</span>
      </span>
      <span className="text-[11.5px] font-medium text-muted truncate">{label}</span>
    </button>
  )
}

export default function Workbench() {
  const phone = usePhone()
  const wbState = useWb()
  const uiState = useUi()
  const [setup, setSetup] = useState(false)
  const [attention, setAttention] = useState(false)
  const popRef = useRef<HTMLDivElement>(null)
  const [tabs, setTabs] = useState<Record<string, ProjectTab>>({})
  const [open, setOpen] = useState<{ id: string; from: DOMRect | null; file: string } | null>(null)
  const [archive, setArchive] = useState(false)
  const closeArchive = useCallback(() => setArchive(false), [])
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
  const running = data?.runs.filter((r) => r.status === 'running').length ?? 0
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
    ? inboxTasks.map((t) => ({ task: t, state: taskState(t), detail: taskStateDetail(t), summary: taskSummary(data, t), stages: [], ...latestRun(data, t.id) }))
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

  useEffect(() => {
    if (!attention) return
    // On a phone the list is a sheet with its own backdrop.
    const down = (e: MouseEvent) => { if (!phone && !popRef.current?.contains(e.target as Node)) setAttention(false) }
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') setAttention(false) }
    document.addEventListener('mousedown', down)
    window.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', down); window.removeEventListener('keydown', key) }
  }, [attention, phone])
  useEffect(() => { if (pending === 0) setAttention(false) }, [pending])

  const attentionBody = (
    <>
    <h2 className="m-0 flex items-center gap-1.5 text-[12px] font-semibold text-muted">
      <Icon name="hand" size={14} className="text-warm" />要你处理<span className="tabular-nums text-faint">{pending}</span>
    </h2>
    {approvals.map((a) => <ApprovalCard key={a.id} approval={a} context={taskTitle(a.task_id)} />)}
    {attentionRows.length > 0 && (
      <div className="grid gap-0.5">
        {attentionRows.map((t) => {
          const p = projectOf(t.task.project_id)
          return (
            <div key={t.task.id} className="flex items-center gap-1 pl-1 min-w-0">
              {p && <span title={p.name}><ProjectTile name={p.name} id={p.id} size={22} misc={p.id === 'misc'} /></span>}
              <div className="flex-1 min-w-0">
                <TaskRow task={t} onOpen={() => { setAttention(false); go({ view: 'task', id: t.task.id }) }} />
              </div>
            </div>
          )
        })}
      </div>
    )}
    </>
  )
  const openMachines = () => { ui.state.settings = 'machines'; ui.emit() }

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

  return (
    <>
      {phone && (
        <PhoneHeader
          title="工作台"
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
          {data && (
            <div className="grid grid-cols-4 gap-2 px-4 pb-1" aria-label="概况">
              <Tile icon="play" value={running} label="执行中" tone="accent" />
              <Tile icon="hand" value={pending} label="要你处理" tone="warning" onClick={pending ? () => setAttention(true) : undefined} />
              <Tile icon="circle-dashed" value={todo} label="待办" tone="neutral" />
              <Tile icon="laptop" value={`${online}/${data.nodes.length}`} label="机器在线" tone="success" onClick={openMachines} />
            </div>
          )}
        </PhoneHeader>
      )}
      <div className="px-7 pt-5 pb-10 grid gap-6 content-start max-w-[1480px] min-w-0 max-[760px]:px-4 max-[760px]:pt-4 max-[760px]:pb-8 max-[760px]:gap-4">
        <header className="flex items-center flex-wrap gap-2.5 min-h-9 max-[760px]:hidden">
          <h1 className="text-[18px] font-semibold tracking-tight m-0 whitespace-nowrap">工作台</h1>
          <span className="flex-1"></span>
          {data && (
            <div className="flex items-center flex-wrap gap-2" aria-label="概况">
              <Stat icon="play" value={running} label="正在执行" tone="accent" />
              <div className="relative" ref={popRef}>
                <Stat icon="hand" value={pending} label="要你处理" tone="warning" expanded={attention} onClick={pending ? () => setAttention((v) => !v) : undefined} />
                {attention && pending > 0 && !phone && (
                  <div
                    id="attention"
                    role="dialog"
                    aria-label="要你处理"
                    className="absolute right-0 top-full mt-2 z-20 w-[min(440px,calc(100vw-28px))] max-h-[70dvh] overflow-y-auto card shadow-pop p-3 grid gap-2.5 rise"
                  >
                    {attentionBody}
                  </div>
                )}
              </div>
              <Stat icon="circle-dashed" value={todo} label="待办" tone="neutral" />
              <Stat icon="laptop" value={`${online}/${data.nodes.length}`} label="机器在线" tone="success" onClick={openMachines} />
            </div>
          )}
          <button className="btn small ghost" aria-label="打开已归档的项目" data-tip={`已归档 ${archivedCount} 个`} data-tip-pos="bottom" onClick={() => setArchive(true)}>
            <Icon name="folder-open" size={16} />打开
          </button>
          <button className="btn small" aria-label="新建或登记项目" onClick={() => setSetup(true)}>
            <Icon name="plus" size={16} />项目
          </button>
        </header>

        {wbState.error && !data ? (
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
      {phone && attention && pending > 0 && (
        <>
          <div className="backdrop" onClick={() => setAttention(false)}></div>
          <div className="sheet !gap-2.5" role="dialog" aria-label="要你处理">
            <div className="grid gap-2.5 overflow-y-auto -mx-1 px-1">{attentionBody}</div>
          </div>
        </>
      )}
      {archive && <ArchiveModal cards={allCards} renderCard={renderCard} onClose={closeArchive} />}
      {modalCard && (
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
      )}
      {setup && <ProjectSetupSheet onClose={() => setSetup(false)} />}
    </>
  )
}
