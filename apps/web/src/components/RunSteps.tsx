// The plan and tool steps of a Run, drawn as icon lists. Shared by RunCard and RunSheet.
import type { ToolCall } from '../lib/runs'
import Icon from './Icon'

export function PlanList({ plan }: { plan: { content: string; status: string }[] }) {
  return (
    <ul className="m-0 p-0 list-none grid gap-1 text-[13px]">
      {plan.map((e, i) => (
        <li key={i} className="flex gap-2 items-start min-w-0">
          <span className={`mt-[3px] flex-none ${e.status === 'completed' ? 'text-ok' : e.status === 'in_progress' ? 'text-info' : 'text-faint'}`}>
            <Icon name={e.status === 'completed' ? 'check-circle' : e.status === 'in_progress' ? 'circle-dashed' : 'circle'} size={14} stroke={2} className={e.status === 'in_progress' ? 'spin' : ''} />
          </span>
          <span className={`min-w-0 [overflow-wrap:anywhere] ${e.status === 'completed' ? 'text-muted line-through decoration-faint' : ''}`}>{e.content}</span>
        </li>
      ))}
    </ul>
  )
}

const dot: Record<string, string> = { completed: 'bg-ok', failed: 'bg-danger', in_progress: 'bg-info live-dot', pending: 'bg-faint' }

export function ToolList({ tools, live = true, more = 0, onMore }: { tools: ToolCall[]; live?: boolean; more?: number; onMore?: () => void }) {
  return (
    <ol className="m-0 p-0 list-none grid text-[12.5px] relative">
      {more > 0 && (
        <li className="pb-1.5">
          <button className="inline-flex items-center gap-1 border-0 bg-sunken rounded-full px-2 py-0.5 text-[11.5px] font-semibold text-muted hover:text-ink disabled:opacity-100" disabled={!onMore} onClick={onMore} aria-label={`还有 ${more} 个更早的步骤`}>
            <Icon name="chevron" size={11} stroke={2.4} className="rotate-180" />+{more}
          </button>
        </li>
      )}
      {tools.map((t, i) => {
        const done = t.status === 'completed' || t.status === 'failed'
        const state = done || live ? t.status : 'pending'
        return (
          <li key={t.id} className="flex gap-2.5 items-start min-w-0 relative pb-1.5">
            {i < tools.length - 1 && <span className="absolute left-[3.5px] top-[12px] bottom-[-2px] w-px bg-line" aria-hidden="true"></span>}
            <span className={`mt-[6px] w-2 h-2 rounded-full flex-none ${dot[state] ?? 'bg-faint'}`} title={t.status}></span>
            <span className={`min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[12px] ${t.status === 'failed' ? 'text-danger' : done ? 'text-ink' : 'text-muted'}`} title={t.title}>
              {t.title}
            </span>
          </li>
        )
      })}
    </ol>
  )
}
