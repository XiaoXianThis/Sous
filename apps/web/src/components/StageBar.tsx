import type { Stage } from '../lib/status'
import Icon from './Icon'

const stateText: Record<Stage['state'], string> = { done: '已完成', current: '执行中', todo: '', failed: '失败' }
const stageIcon: Record<string, string> = { plan: 'list', work: 'terminal', review: 'eye', pr: 'pr', deploy: 'rocket', health: 'pulse' }

export default function StageBar({ stages, labels = false, inline = false }: { stages: Stage[]; labels?: boolean; inline?: boolean }) {
  const aria = stages.map((s) => `${s.text}${stateText[s.state]}`).join('，')
  if (inline) {
    // One row: small icon + name per stage, joined by short lines; sized to fit in a line of text.
    return (
      <span className="inline-flex items-center gap-1 flex-none" role="img" aria-label={aria}>
        {stages.map((s, i) => {
          const tone = s.state === 'done' ? 'success' : s.state === 'current' ? 'accent' : s.state === 'failed' ? 'danger' : 'neutral'
          return (
            <span key={s.key} className="inline-flex items-center gap-1">
              <span className={`inline-flex items-center gap-1 whitespace-nowrap text-[12px] tone-${tone} ${s.state === 'todo' ? '!text-faint' : ''} ${s.state === 'current' || s.state === 'failed' ? 'font-semibold' : ''}`}>
                <span className={`grid place-items-center w-4 h-4 rounded-full ${s.state === 'todo' ? 'bg-sunken' : 'bg-[color-mix(in_srgb,currentColor_16%,transparent)]'} ${s.state === 'current' ? 'stage-current' : ''}`}>
                  <Icon name={s.state === 'done' ? 'check' : s.state === 'failed' ? 'x' : stageIcon[s.key] ?? 'dot'} size={12} stroke={2.4} />
                </span>
                {s.text}
              </span>
              {i < stages.length - 1 && <span className={`w-2.5 h-px ${s.state === 'done' ? 'bg-ok' : 'bg-line-strong'}`}></span>}
            </span>
          )
        })}
      </span>
    )
  }
  if (!labels) {
    return (
      <span className="flex gap-[3px]" role="img" aria-label={aria}>
        {stages.map((s) => (
          <span
            key={s.key}
            title={s.text}
            className={`flex-1 h-[4px] rounded-full ${
              s.state === 'done' ? 'bg-ok' : s.state === 'current' ? 'bg-info stage-current' : s.state === 'failed' ? 'bg-danger' : 'bg-line-strong opacity-60'
            }`}
          ></span>
        ))}
      </span>
    )
  }
  // The labelled version: a stepper of icon nodes joined by lines.
  return (
    <div className="flex items-start" role="img" aria-label={aria}>
      {stages.map((s, i) => {
        const tone = s.state === 'done' ? 'success' : s.state === 'current' ? 'accent' : s.state === 'failed' ? 'danger' : 'neutral'
        return (
          <div key={s.key} className="flex-1 grid justify-items-center gap-1 relative min-w-0">
            {i > 0 && (
              <span
                className={`absolute top-[15px] right-[calc(50%+18px)] left-[calc(-50%+18px)] h-[2px] rounded-full ${
                  s.state === 'todo' ? 'bg-line' : s.state === 'failed' ? 'bg-danger' : 'bg-ok'
                }`}
              ></span>
            )}
            <span
              className={`grid place-items-center w-8 h-8 rounded-full tone-${tone} ${
                s.state === 'todo'
                  ? 'bg-sunken text-faint'
                  : s.state === 'current'
                    ? 'bg-[color-mix(in_srgb,currentColor_14%,transparent)] shadow-[0_0_0_2px_currentColor] stage-current'
                    : 'bg-[color-mix(in_srgb,currentColor_16%,transparent)]'
              }`}
            >
              <Icon name={s.state === 'done' ? 'check' : s.state === 'failed' ? 'x' : stageIcon[s.key] ?? 'dot'} size={15} stroke={2.2} />
            </span>
            <span className={`text-[11px] whitespace-nowrap overflow-hidden text-ellipsis max-w-full ${s.state === 'current' ? 'text-info font-semibold' : s.state === 'failed' ? 'text-danger font-semibold' : 'text-muted'}`}>
              {s.text}
            </span>
          </div>
        )
      })}
    </div>
  )
}
