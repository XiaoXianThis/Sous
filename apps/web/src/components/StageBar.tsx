import type { Stage } from '../lib/status'

const stateText: Record<Stage['state'], string> = { done: '完成', current: '进行中', todo: '', failed: '失败' }

export default function StageBar({ stages, labels = false }: { stages: Stage[]; labels?: boolean }) {
  return (
    <div
      className="flex gap-[3px]"
      role="img"
      aria-label={stages.map((s) => `${s.text}${stateText[s.state]}`).join('，')}
    >
      {stages.map((s) => (
        <div key={s.key} className="flex-1 grid gap-[3px] min-w-0">
          <span
            className={`rounded-[3px] ${labels ? 'h-[6px]' : 'h-[5px]'} ${
              s.state === 'done' ? 'bg-ok' : s.state === 'current' ? 'bg-info stage-current' : s.state === 'failed' ? 'bg-danger' : 'bg-line'
            }`}
          ></span>
          {labels && (
            <span
              className={`text-[11px] text-center whitespace-nowrap overflow-hidden text-ellipsis ${
                s.state === 'current' ? 'text-info font-semibold' : s.state === 'failed' ? 'text-danger font-semibold' : 'text-muted'
              }`}
            >
              {s.text}
            </span>
          )}
        </div>
      ))}
    </div>
  )
}
