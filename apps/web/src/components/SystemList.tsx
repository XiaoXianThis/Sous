import { useMemo, useState } from 'react'
import { go, ui, useWb } from '../lib/app'
import { laneState, laneSummary, runTasks, steps, visibleLanes, type Lane, type Step } from '../lib/coord'
import { usePhone } from '../lib/phone'
import { STATE_TEXT } from '../lib/sysmap'
import { useSystemData } from '../lib/sysmap-data'
import Icon from './Icon'
import { LaneDialog, StateDot, StepDialog } from './SystemDetail'

/** The system's participants as a list (settings, and the phone): state, summary, details in a modal. */
export default function SystemList() {
  const { graph, records, error, refresh } = useSystemData()
  const wb = useWb()
  const phone = usePhone()
  const [lane, setLane] = useState<Lane | null>(null)
  const [step, setStep] = useState<Step | null>(null)
  const taskOfRun = useMemo(() => {
    const m = runTasks(records)
    for (const r of wb.data?.runs ?? []) if (r.task_id) m.set(r.id, r.task_id)
    return (id: string) => m.get(id) ?? ''
  }, [wb.data?.runs, records])
  const all = useMemo(() => steps(records, { detail: false, taskOfRun }), [records, taskOfRun])

  if (!graph) {
    return (
      <div className="p-4 text-[14px] text-muted">
        {error ? (
          <div className="flex items-center gap-3">
            读不到系统状态：{error}
            <button className="btn small" onClick={() => void refresh()}>重试</button>
          </div>
        ) : (
          '正在读取…'
        )}
      </div>
    )
  }
  return (
    <div className="grid gap-4 p-4 content-start">
      {!phone && (
        <button
          className="btn ghost small justify-self-start"
          onClick={() => {
            ui.state.settings = ''
            ui.emit()
            go({ view: 'system', id: '' })
          }}
        >
          打开系统页，看模块之间怎么配合
        </button>
      )}
      <div className="ios-group">
        {visibleLanes(false).map((l) => {
          const state = laneState(l, graph)
          return (
            <button key={l.id} className="w-full min-h-16 flex items-center gap-3 text-left px-4 border-b border-line last:border-b-0 hover:bg-hover" onClick={() => setLane(l)}>
              <StateDot state={state} />
              <span className="min-w-0 flex-1">
                <span className="block font-semibold text-[14px]">{l.name}</span>
                <span className="block truncate text-[12px] text-muted">{laneSummary(l, graph)}</span>
              </span>
              <span className={`text-[12px] ${state === 'degraded' ? 'font-semibold' : 'text-muted'}`}>{STATE_TEXT[state]}</span>
              <Icon name="chevron" size={14} className="text-faint -rotate-90" />
            </button>
          )
        })}
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
      {step && <StepDialog step={step} label={step.activity || '系统'} onClose={() => setStep(null)} onActivity={() => setStep(null)} />}
    </div>
  )
}
