import type { ReactNode } from 'react'
import { go, ui } from '../lib/app'
import { agentTitle } from '../lib/agents'
import { ACTOR_NAME, COLUMNS, spotName, type Actor, type Floor, type Move, type Spot } from '../lib/dispatch'
import type { Overview, Run, Task } from '../lib/types'
import AgentIcon from './AgentIcon'
import Icon from './Icon'
import { clock } from './SystemDetail'
import { Dialog, DialogContent, DialogTitle } from './ui/dialog'

/** The modal every detail of the dispatch page opens in. */
function Modal({ title, sub, onClose, children }: { title: ReactNode; sub?: ReactNode; onClose: () => void; children: ReactNode }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sheet !p-0 overflow-hidden" aria-label={typeof title === 'string' ? title : '详情'}>
        <header className="flex items-start gap-3 px-5 pt-4 pb-3 border-b border-line">
          <div className="min-w-0 flex-1">
            <DialogTitle className="text-[17px] font-semibold m-0 flex items-center gap-2">{title}</DialogTitle>
            {sub && <div className="text-[13px] text-muted mt-1">{sub}</div>}
          </div>
          <button className="icon-btn" data-back aria-label="关闭" onClick={onClose}>
            <Icon name="x" size={18} />
          </button>
        </header>
        <div className="overflow-y-auto overscroll-contain px-5 py-4 grid gap-5 content-start min-h-0">{children}</div>
      </DialogContent>
    </Dialog>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="grid gap-2">
      <h3 className="text-[12px] font-semibold text-muted m-0 tracking-wide">{title}</h3>
      {children}
    </section>
  )
}

function Row({ children, onClick }: { children: ReactNode; onClick?: () => void }) {
  return (
    <button className="w-full flex items-center gap-2 px-2 py-1.5 rounded-[9px] text-left hover:bg-hover text-[13px] min-w-0" onClick={onClick} disabled={!onClick}>
      {children}
    </button>
  )
}

const openRun = (id: string) => {
  ui.state.runSheet = id
  ui.emit()
}

/** One line of the feed: time, who to whom, what. */
export function MoveRow({ move, data, onOpen }: { move: Move; data: Overview | null; onOpen: () => void }) {
  return (
    <button className="w-full grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 px-2 py-1.5 rounded-[9px] text-left hover:bg-hover" onClick={onOpen}>
      <span className="text-[11px] text-faint tabular-nums pt-[1px]">{clock(move.ts)}</span>
      <span className="min-w-0">
        <span className={`block text-[12.5px] truncate ${move.ok === false ? 'font-semibold' : ''}`}>{move.text}</span>
        <span className="block text-[11.5px] text-muted truncate">
          {spotName(move.from, data)}
          {JSON.stringify(move.from) !== JSON.stringify(move.to) && <> → {spotName(move.to, data)}</>}
        </span>
      </span>
    </button>
  )
}

function Moves({ list, data, onMove }: { list: Move[]; data: Overview | null; onMove: (m: Move) => void }) {
  if (!list.length) return <div className="text-[13px] text-muted">打开页面以来还没有经手的事</div>
  return (
    <div className="grid">
      {[...list].reverse().slice(0, 30).map((m) => (
        <MoveRow key={m.id} move={m} data={data} onOpen={() => onMove(m)} />
      ))}
    </div>
  )
}

function TaskRow({ task, onClick }: { task: Task; onClick: () => void }) {
  return (
    <Row onClick={onClick}>
      <span className="truncate flex-1">{task.title}</span>
      {task.status_reason && <span className="text-[12px] text-muted truncate max-w-[45%]">{task.status_reason}</span>}
    </Row>
  )
}

function RunRow({ run, data }: { run: Run; data: Overview }) {
  const task = run.task_id ? data.tasks.find((t) => t.id === run.task_id) : undefined
  return (
    <Row onClick={() => openRun(run.id)}>
      <AgentIcon name={run.agent} size={18} />
      <span className="shrink-0">{agentTitle(run.agent)}</span>
      <span className="truncate text-muted">{task?.title || run.title}</span>
    </Row>
  )
}

const ROLE: Record<Actor, string> = {
  you: '留言、审批、收汇报。',
  sup: '接你的话，拆成任务，派给机器上的 agent，追进度，再向你汇报。',
  reviewer: '主管的帮手：检查 agent 交回来的活，通过或退回。',
  approver: '主管的帮手：自动模式下先看 agent 的请示，拿不准的转给你。',
  inspector: '主管的帮手：巡查进行中的活，发现打转、卡住、太久就提醒主管。',
  clock: '定时任务和跟进到点时叫醒主管。',
}

const touches = (s: Spot, a: Actor) => s.kind === 'actor' && s.actor === a

/** A person in the dispatch room or your office: what they do, what is on their desk, what went through their hands. */
export function StaffDialog({ actor, floor, data, feed, onClose, onMove }: { actor: Actor; floor: Floor; data: Overview; feed: Move[]; onClose: () => void; onMove: (m: Move) => void }) {
  const desk = actor === 'you' ? floor.mine : actor === 'approver' ? floor.theirs : []
  const tasks = (id: string) => data.tasks.find((t) => t.id === id)
  return (
    <Modal title={ACTOR_NAME[actor]} sub={ROLE[actor]} onClose={onClose}>
      {(actor === 'you' || actor === 'approver') && (
        <Section title={actor === 'you' ? `等你批（${desk.length}）` : `正在看（${desk.length}）`}>
          {desk.length ? (
            desk.map((a) => (
              <Row key={a.id} onClick={() => go({ view: 'task', id: a.task_id })}>
                <Icon name="hand" size={14} className="text-muted shrink-0" />
                <span className="truncate flex-1">{a.title}</span>
                <span className="text-[12px] text-muted truncate max-w-[40%]">{tasks(a.task_id)?.title}</span>
              </Row>
            ))
          ) : (
            <div className="text-[13px] text-muted">没有</div>
          )}
        </Section>
      )}
      <Section title="最近经手">
        <Moves list={feed.filter((m) => touches(m.from, actor) || touches(m.to, actor))} data={data} onMove={onMove} />
      </Section>
    </Modal>
  )
}

/** One column of the task board. */
export function ColumnDialog({ col, floor, onClose }: { col: number; floor: Floor; onClose: () => void }) {
  const c = COLUMNS[col]
  const list = floor.board[c.id]
  return (
    <Modal title={`看板 · ${c.name}`} sub={`${list.length} 个任务${c.id === 'done' ? '（最近 24 小时）' : ''}`} onClose={onClose}>
      <div className="grid">
        {list.length ? list.map((t) => <TaskRow key={t.id} task={t} onClick={() => go({ view: 'task', id: t.id })} />) : <div className="text-[13px] text-muted">没有</div>}
      </div>
    </Modal>
  )
}

/** A machine's workshop: who is at which seat, who is waiting, what came and went. */
export function MachineDialog({ node, floor, data, feed, onClose, onMove }: { node: string; floor: Floor; data: Overview; feed: Move[]; onClose: () => void; onMove: (m: Move) => void }) {
  const shop = floor.shops.find((s) => s.node.id === node)
  if (!shop) return null
  const runs = new Set(data.runs.filter((r) => r.node_id === node).map((r) => r.id))
  const here = (s: Spot) => (s.kind === 'machine' && s.node === node) || (s.kind === 'run' && runs.has(s.run))
  const busy = shop.seats.filter((r): r is Run => !!r)
  return (
    <Modal title={shop.node.name} sub={shop.node.online ? `在线 · ${busy.length}/${shop.seats.length} 个工位在干活` : '离线'} onClose={onClose}>
      <Section title="在干活">
        {busy.length ? busy.map((r) => <RunRow key={r.id} run={r} data={data} />) : <div className="text-[13px] text-muted">工位都空着</div>}
      </Section>
      {shop.queue.length > 0 && (
        <Section title="排队">
          {shop.queue.map((r) => (
            <RunRow key={r.id} run={r} data={data} />
          ))}
        </Section>
      )}
      <Section title="最近进出">
        <Moves list={feed.filter((m) => here(m.from) || here(m.to))} data={data} onMove={onMove} />
      </Section>
    </Modal>
  )
}

/** One thing that moved: what, from whom to whom, and where to look. */
export function MoveDialog({ move, data, onClose, onFocus }: { move: Move; data: Overview | null; onClose: () => void; onFocus: (key: string) => void }) {
  const task = move.task_id ? data?.tasks.find((t) => t.id === move.task_id) : undefined
  const key = move.task_id ? `task:${move.task_id}` : move.run_id ? `run:${move.run_id}` : move.thread_id ? `thread:${move.thread_id}` : ''
  return (
    <Modal title={move.text} sub={`${spotName(move.from, data)} → ${spotName(move.to, data)} · ${clock(move.ts)}`} onClose={onClose}>
      {task && (
        <Section title="任务">
          <TaskRow task={task} onClick={() => go({ view: 'task', id: task.id })} />
        </Section>
      )}
      <div className="flex flex-wrap gap-2">
        {move.run_id && (
          <button className="btn small" onClick={() => openRun(move.run_id!)}>
            打开 Run
          </button>
        )}
        {key && (
          <button className="btn small" onClick={() => onFocus(key)}>
            只看这件事
          </button>
        )}
      </div>
    </Modal>
  )
}

/** Every hand-off since the page opened (and just before), newest first. */
export function FeedDialog({ list, data, sub, onClose, onMove }: { list: Move[]; data: Overview | null; sub: string; onClose: () => void; onMove: (m: Move) => void }) {
  return (
    <Modal title="动态" sub={sub} onClose={onClose}>
      {list.length ? (
        <div className="grid">
          {[...list].reverse().map((m) => (
            <MoveRow key={m.id} move={m} data={data} onOpen={() => onMove(m)} />
          ))}
        </div>
      ) : (
        <div className="text-[13px] text-muted">还没有交接</div>
      )}
    </Modal>
  )
}
