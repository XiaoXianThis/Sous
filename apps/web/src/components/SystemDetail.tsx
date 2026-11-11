import type { ReactNode } from 'react'
import { useChat, useWb } from '../lib/app'
import { LANES, laneOf, type Lane, type Step } from '../lib/coord'
import { STATE_TEXT } from '../lib/sysmap'
import type { SysGraph, SysModule, SysState } from '../lib/types'
import Icon from './Icon'
import { Dialog, DialogContent, DialogTitle } from './ui/dialog'

/** A module's state as a mark: filled, hollow, accent and live, or a cross. */
export function StateDot({ state }: { state: SysState }) {
  if (state === 'down') return <Icon name="x" size={12} className="text-faint shrink-0" />
  const look =
    state === 'busy' ? 'bg-accent live-dot' : state === 'idle' ? 'border-[1.5px] border-faint' : state === 'degraded' ? 'bg-ink ring-2 ring-ink/25' : 'bg-ink'
  return <span className={`inline-block shrink-0 w-2 h-2 rounded-full ${look}`} />
}

export const clock = (ts: number) => new Date(ts).toLocaleTimeString('zh-CN', { hour12: false })

/** The modal every detail of the system page opens in. */
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

/** Steps per 30 seconds over the last 15 minutes, as small bars. */
function Activity({ list, now }: { list: Step[]; now: number }) {
  const bars = Array.from({ length: 30 }, (_, i) => {
    const end = now - (29 - i) * 30000
    return list.filter((s) => s.until > end - 30000 && s.until <= end).reduce((n, s) => n + s.count, 0)
  })
  const max = Math.max(1, ...bars)
  return (
    <div>
      <div className="flex items-end gap-[3px] h-12">
        {bars.map((v, i) => (
          <span key={i} className={`flex-1 rounded-[2px] ${v ? 'bg-muted' : 'bg-line'}`} style={{ height: `${Math.max(2, (v / max) * 48)}px` }} />
        ))}
      </div>
      <div className="flex justify-between text-[11.5px] text-faint mt-1">
        <span>15 分钟前</span>
        <span>现在</span>
      </div>
    </div>
  )
}

function ModuleBlock({ module, graph }: { module: SysModule; graph: SysGraph }) {
  const chat = useChat()
  const s = graph.status[module.id] ?? { state: 'idle' as const, summary: '' }
  return (
    <div className="rounded-[12px] bg-sunken p-3 grid gap-2">
      <div className="flex items-center gap-2">
        <StateDot state={s.state} />
        <span className="font-semibold text-[14px] min-w-0 truncate">{module.name}</span>
        <span className={`ml-auto text-[12px] ${s.state === 'degraded' ? 'font-semibold' : 'text-muted'}`}>{STATE_TEXT[s.state]}</span>
      </div>
      {s.summary && <div className="text-[13px]">{s.summary}</div>}
      <div className="text-[12px] text-muted">{module.desc}</div>
      {!!s.gauges?.length && (
        <div className="flex flex-wrap gap-2">
          {s.gauges.map((g) => (
            <span key={g.label} className="rounded-[9px] bg-panel px-2.5 py-1 text-[12px]">
              <span className="text-muted">{g.label}</span> <b className="tabular-nums">{g.value}</b>
              {g.unit && <span className="text-muted"> {g.unit}</span>}
            </span>
          ))}
        </div>
      )}
      {!!s.items?.length && (
        <div className="grid">
          {s.items.map((item) => {
            const label = item.label || chat.threads.find((t) => t.id === item.id)?.title || item.id.slice(0, 8)
            const body = (
              <>
                <StateDot state={item.state} />
                <span className="text-[13px] shrink-0">{label}</span>
                <span className="text-[12px] text-muted min-w-0 truncate">{item.detail}</span>
                {item.link && <Icon name="chevron" size={14} className="ml-auto text-faint shrink-0 -rotate-90" />}
              </>
            )
            return item.link ? (
              <button key={item.id} className="flex items-center gap-2 min-h-8 text-left rounded-[8px] px-1 -mx-1 hover:bg-hover" onClick={() => (location.hash = item.link!)}>
                {body}
              </button>
            ) : (
              <div key={item.id} className="flex items-center gap-2 min-h-8">
                {body}
              </div>
            )
          })}
        </div>
      )}
      {module.code && <code className="text-[11.5px] text-faint">{module.code}</code>}
    </div>
  )
}

const laneName = (id: string) => LANES.find((l) => l.id === id)?.name ?? id

/** One step's words: "主管 → 模型" or a note on one lane. */
export function stepTitle(s: Step): string {
  return s.from === s.to ? laneName(s.from) : `${laneName(s.from)} → ${laneName(s.to)}`
}

/** Who a lane is and what it has been doing. */
export function LaneDialog({ lane, graph, list, onClose, onStep }: { lane: Lane; graph: SysGraph; list: Step[]; onClose: () => void; onStep: (s: Step) => void }) {
  const mods = graph.modules.filter((m) => laneOf(m.id) === lane.id)
  const mine = list.filter((s) => s.from === lane.id || s.to === lane.id)
  return (
    <Modal title={lane.name} sub={`${mods.length} 个模块 · 最近 15 分钟 ${mine.reduce((n, s) => n + s.count, 0)} 次往来`} onClose={onClose}>
      <Section title="状态">
        {mods.length ? mods.map((m) => <ModuleBlock key={m.id} module={m} graph={graph} />) : <div className="text-[13px] text-muted">现在没有这一类模块</div>}
      </Section>
      <Section title="近 15 分钟">
        <Activity list={mine} now={Date.now()} />
      </Section>
      <Section title="最近的往来">
        {mine.length === 0 && <div className="text-[13px] text-muted">还没有</div>}
        <div className="grid">
          {mine
            .slice(-40)
            .reverse()
            .map((s) => (
              <button key={s.key} className="flex items-center gap-2 min-h-8 text-left rounded-[8px] px-1 -mx-1 hover:bg-hover" onClick={() => onStep(s)}>
                <span className="text-[11.5px] text-faint tabular-nums w-[58px] shrink-0">{clock(s.ts)}</span>
                <span className="text-[12px] text-muted shrink-0">{stepTitle(s)}</span>
                <span className="text-[13px] min-w-0 truncate">{s.text}</span>
                {s.count > 1 && <span className="text-[11.5px] text-faint shrink-0">×{s.count}</span>}
              </button>
            ))}
        </div>
      </Section>
    </Modal>
  )
}

/** What happened in one row, and the raw records behind it. */
export function StepDialog({ step, label, onClose, onActivity }: { step: Step; label: string; onClose: () => void; onActivity: (a: string) => void }) {
  const wb = useWb()
  const [kind, id] = step.activity.split(':')
  const task = kind === 'task' ? wb.data?.tasks.find((t) => t.id === id) : undefined
  return (
    <Modal
      title={step.text}
      sub={
        <>
          {stepTitle(step)} · {clock(step.ts)}
          {step.until !== step.ts && ` – ${clock(step.until)}`}
          {step.count > 1 && ` · ${step.count} 次`}
        </>
      }
      onClose={onClose}
    >
      {step.activity && (
        <Section title="属于">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[14px] font-medium min-w-0 truncate">{label}</span>
            <button className="btn ghost small ml-auto" onClick={() => onActivity(step.activity)}>
              只看这件事
            </button>
            {task && (
              <button className="btn ghost small" onClick={() => (location.hash = `#/task=${encodeURIComponent(task.id)}`)}>
                打开任务
              </button>
            )}
          </div>
        </Section>
      )}
      <Section title={`原始记录（${step.records.length}）`}>
        <div className="grid gap-1 font-mono text-[11.5px]">
          {step.records.slice(-50).map((r, i) => (
            <div key={i} className="flex gap-2 min-w-0">
              <span className="text-faint shrink-0">{clock(r.ts)}</span>
              <span className="text-muted shrink-0">{r.edge}</span>
              <span className="min-w-0 truncate">{r.summary}</span>
            </div>
          ))}
        </div>
      </Section>
    </Modal>
  )
}
