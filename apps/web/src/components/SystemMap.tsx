import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { go, useChat, useWb } from '../lib/app'
import { activities, laneState, laneSummary, laneX, runTasks, steps, usedLanes, type Lane, type Step } from '../lib/coord'
import { useSystemData } from '../lib/sysmap-data'
import type { SysRecord } from '../lib/types'
import Icon from './Icon'
import { clock, LaneDialog, StateDot, StepDialog } from './SystemDetail'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from './ui/dropdown-menu'

const ROWS = 400 // rows drawn at most; older ones scroll off
const FRESH_MS = 2500 // a row this new is drawn in the accent color
const LANE_MIN = 112 // px; narrower windows scroll sideways

/** Names an activity key ("task:<id>", "thread:<id>", "run:<id>"). */
function useActivityName() {
  const chat = useChat()
  const wb = useWb()
  return (key: string): string => {
    if (!key) return '系统'
    const [kind, id] = key.split(':')
    if (kind === 'task') return wb.data?.tasks.find((t) => t.id === id)?.title ?? `任务 ${id.slice(-6)}`
    if (kind === 'thread') {
      const t = chat.threads.find((x) => x.id === id)
      if (t?.kind === 'main') return '主线'
      return t?.title ?? `对话 ${id.slice(-6)}`
    }
    return `Run ${id.slice(-6)}`
  }
}

/**
 * How the modules work together, live: one column per participant, time going
 * down, each row an arrow from who asked to who answered, grouped by the task
 * or conversation it belongs to (work/specs/system-map).
 */
export default function SystemMap({ trace }: { trace: string }) {
  const { graph, records, connected, error, refresh } = useSystemData()
  const wb = useWb()
  const name = useActivityName()
  const [detail, setDetail] = useState(false)
  const [frozen, setFrozen] = useState<SysRecord[] | null>(null)
  const [activity, setActivity] = useState(trace)
  const [lane, setLane] = useState<Lane | null>(null)
  const [step, setStep] = useState<Step | null>(null)
  const [now, setNow] = useState(Date.now())
  useEffect(() => setActivity(trace), [trace])
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])

  const taskOfRun = useMemo(() => {
    const m = runTasks(records)
    for (const r of wb.data?.runs ?? []) if (r.task_id) m.set(r.id, r.task_id)
    return (id: string) => m.get(id) ?? ''
  }, [wb.data?.runs, records])
  const shown = frozen ?? records
  const all = useMemo(() => steps(shown, { detail, taskOfRun }), [shown, detail, taskOfRun])
  const acts = useMemo(() => activities(all), [all])
  const list = useMemo(() => (activity ? all.filter((s) => s.activity === activity) : all).slice(-ROWS), [all, activity])
  const waiting = frozen ? Math.max(0, records.length - frozen.length) : 0

  // Stay at the bottom while the user is there, like a chat.
  const scroller = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)
  useLayoutEffect(() => {
    const el = scroller.current
    if (el && atBottom.current) el.scrollTop = el.scrollHeight
  }, [list.length, list[list.length - 1]?.count])

  if (!graph) {
    return (
      <div className="h-full grid place-items-center text-[14px] text-muted">
        {error ? (
          <div className="grid gap-3 justify-items-center">
            <Icon name="alert" size={20} />
            <span>读不到系统状态：{error}</span>
            <button className="btn small" onClick={() => void refresh()}>重试</button>
          </div>
        ) : (
          '正在读取…'
        )}
      </div>
    )
  }

  const lanes = usedLanes(detail, all, graph)
  const laneLines: CSSProperties = {
    backgroundImage: 'linear-gradient(to right, var(--line) 1px, transparent 1px)',
    backgroundSize: `${100 / lanes.length}% 100%`,
    backgroundPosition: `${50 / lanes.length}% 0`,
  }
  const cols = 'grid grid-cols-[64px_150px_minmax(0,1fr)]'

  return (
    <div className="h-full grid grid-rows-[auto_minmax(0,1fr)] bg-page">
      <header className="h-[54px] flex items-center gap-3 px-5">
        <h1 className="text-[17px] font-semibold m-0">系统</h1>
        <span className="text-[13px] text-muted max-[1100px]:hidden">模块之间怎么配合，实时</span>
        {!connected && <span className="pill">未连接，正在重试</span>}
        <span className="flex-1" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className="btn ghost small max-w-[260px]" aria-label="只看一件事">
              <span className="truncate">{activity ? name(activity) : '全部'}</span>
              <Icon name="chevron" size={12} className="shrink-0" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-h-[60dvh] overflow-y-auto">
            <DropdownMenuItem onSelect={() => setActivity('')}>全部</DropdownMenuItem>
            {acts.map((a) => (
              <DropdownMenuItem key={a} onSelect={() => setActivity(a)}>
                <span className="truncate max-w-[280px]">{name(a)}</span>
              </DropdownMenuItem>
            ))}
            {activity && !acts.includes(activity) && <DropdownMenuItem disabled>{name(activity)}（最近 15 分钟没有动静）</DropdownMenuItem>}
          </DropdownMenuContent>
        </DropdownMenu>
        <button
          className={`icon-btn ${detail ? 'solid' : ''}`}
          aria-pressed={detail}
          aria-label="显示底层细节"
          data-tip={detail ? '只看模块之间的配合' : '显示底层细节：事件日志、页面推送、轮询'}
          data-tip-pos="bottom"
          onClick={() => setDetail(!detail)}
        >
          <Icon name="layers" size={18} />
        </button>
        <button
          className="icon-btn"
          aria-label={frozen ? '继续' : '暂停'}
          data-tip={frozen ? '回到实时' : '暂停实时更新'}
          data-tip-pos="bottom"
          onClick={() => setFrozen(frozen ? null : records)}
        >
          <Icon name={frozen ? 'play' : 'pause'} size={18} />
        </button>
      </header>

      <div
        ref={scroller}
        className="overflow-auto overscroll-contain px-5 relative"
        onScroll={(e) => {
          const el = e.currentTarget
          atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
      >
        <div style={{ minWidth: 214 + lanes.length * LANE_MIN }}>
        <div className={`${cols} sticky top-0 z-10 bg-page pb-2 border-b border-line`}>
          <span className="text-[11.5px] text-faint self-end pb-1">时间</span>
          <span className="text-[11.5px] text-faint self-end pb-1">哪件事</span>
          <div className="grid" style={{ gridTemplateColumns: `repeat(${lanes.length}, minmax(0, 1fr))` }}>
            {lanes.map((l) => {
              const state = laneState(l, graph)
              const summary = laneSummary(l, graph)
              return (
                <button
                  key={l.id}
                  className="mx-1 rounded-[12px] px-2 py-1.5 text-center hover:bg-hover grid justify-items-center gap-0.5 min-w-0"
                  onClick={() => setLane(l)}
                  data-tip={summary || undefined}
                  data-tip-pos="bottom"
                  aria-label={`${l.name}：${summary}`}
                >
                  <span className="flex items-center gap-1.5 text-[13px] font-semibold min-w-0 max-w-full">
                    <StateDot state={state} />
                    <span className="truncate">{l.name}</span>
                  </span>
                  <span className="text-[11px] text-muted truncate max-w-full">{summary || ' '}</span>
                </button>
              )
            })}
          </div>
        </div>
        {list.length === 0 ? (
          <div className={`${cols} min-h-full`}>
            <span />
            <span />
            <div style={laneLines} className="grid place-items-center text-[13px] text-muted py-24">
              <span className="bg-page px-3 py-1">{activity ? '这件事最近 15 分钟没有动静' : '最近 15 分钟模块之间没有往来'}</span>
            </div>
          </div>
        ) : (
          list.map((s, i) => {
            const prev = list[i - 1]
            const showAct = !prev || prev.activity !== s.activity
            const fresh = !frozen && now - s.until < FRESH_MS
            return (
              <div key={s.key} className={`${cols} h-[30px] items-center group`}>
                <span className="text-[11.5px] text-faint tabular-nums">{!prev || clock(prev.ts) !== clock(s.ts) ? clock(s.ts) : ''}</span>
                <span className="min-w-0 pr-2">
                  {showAct && (
                    <button
                      className="text-[12px] text-muted hover:text-ink truncate max-w-full text-left"
                      onClick={() => setActivity(s.activity)}
                      data-tip="只看这件事"
                    >
                      {name(s.activity)}
                    </button>
                  )}
                </span>
                <button
                  className={`relative h-full w-full rounded-[8px] hover:bg-hover ${fresh ? 'text-accent' : 'text-muted'}`}
                  style={laneLines}
                  onClick={() => setStep(s)}
                  aria-label={`${s.text}，${clock(s.ts)}`}
                >
                  <Arrow step={s} lanes={lanes} fresh={fresh} />
                </button>
              </div>
            )
          })
        )}
        </div>
        {waiting > 0 && (
          <button className="sticky bottom-3 left-1/2 -translate-x-1/2 pill shadow-soft" onClick={() => setFrozen(null)}>
            已暂停 · 有 {waiting} 条新的，回到实时
          </button>
        )}
      </div>

      {lane && (
        <LaneDialog
          lane={lane}
          graph={graph}
          list={all}
          onClose={() => setLane(null)}
          onStep={(s) => {
            setLane(null)
            setStep(s)
          }}
        />
      )}
      {step && (
        <StepDialog
          step={step}
          label={name(step.activity)}
          onClose={() => setStep(null)}
          onActivity={(a) => {
            setStep(null)
            setActivity(a)
            if (trace) go({ view: 'system', id: '' })
          }}
        />
      )}
    </div>
  )
}

/** One row's mark: an arrow between two lanes, or a dot with words on one. */
function Arrow({ step, lanes, fresh }: { step: Step; lanes: Lane[]; fresh: boolean }) {
  const a = laneX(lanes, step.from)
  const b = laneX(lanes, step.to)
  if (a < 0 || b < 0) return null
  const label = (
    <>
      {step.text}
      {step.count > 1 && <span className="opacity-70"> ×{step.count}</span>}
    </>
  )
  const anim = fresh ? 'coord-in' : ''
  if (a === b) {
    const right = a < 70
    return (
      <span className={`absolute inset-y-0 flex items-center ${anim}`} style={right ? { left: `${a}%` } : { right: `${100 - a}%` }}>
        <span className={`w-[7px] h-[7px] rounded-full bg-current shrink-0 ${right ? '-ml-[3.5px]' : 'order-2 -mr-[3.5px]'}`} />
        <span className={`text-[12px] whitespace-nowrap ${right ? 'ml-2' : 'mr-2'} ${fresh ? '' : 'text-ink/80'}`}>{label}</span>
      </span>
    )
  }
  const left = Math.min(a, b)
  const width = Math.abs(a - b)
  const toRight = b > a
  return (
    <span className={`absolute inset-y-0 ${anim}`} style={{ left: `${left}%`, width: `${width}%` }}>
      <span className="absolute left-0 right-0 top-1/2 h-[1.5px] bg-current" />
      <span
        className="absolute top-1/2 -translate-y-1/2 w-0 h-0 border-y-[4px] border-y-transparent"
        style={toRight ? { right: -1, borderLeft: '7px solid currentColor' } : { left: -1, borderRight: '7px solid currentColor' }}
      />
      <span className={`absolute left-2 bottom-[calc(50%+2px)] text-[12px] leading-[15px] whitespace-nowrap ${fresh ? '' : 'text-ink/80'}`}>{label}</span>
    </span>
  )
}
