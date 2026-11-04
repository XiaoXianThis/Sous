import { ui } from '../lib/app'
import type { Location } from '../lib/workbench'
import Icon from './Icon'

export default function Locations({ locations }: { locations: Location[] }) {
  return (
    <div className="flex flex-wrap gap-[5px] items-center" aria-label="项目位置">
      {locations.length === 0 ? (
        <span className="text-[12px] text-muted">不在任何机器上</span>
      ) : (
        locations.map((l) =>
          l.kind === 'remote' ? (
            l.href ? (
              <a
                key={l.kind + l.id}
                className="inline-flex items-center gap-1 border border-[color-mix(in_srgb,var(--accent)_35%,var(--line))] bg-page rounded-full py-px pr-2 pl-1.5 text-[12px] text-accent no-underline max-w-full min-w-0 overflow-hidden text-ellipsis whitespace-nowrap hover:text-ink hover:border-accent"
                href={l.href}
                target="_blank"
                rel="noreferrer"
                title={l.title}
              >
                <Icon name={l.icon} size={12} />{l.name}
              </a>
            ) : (
              <span
                key={l.kind + l.id}
                className="inline-flex items-center gap-1 border border-[color-mix(in_srgb,var(--accent)_35%,var(--line))] bg-page rounded-full py-px pr-2 pl-1.5 text-[12px] text-accent no-underline max-w-full min-w-0 overflow-hidden text-ellipsis whitespace-nowrap hover:text-ink hover:border-accent"
                title={l.title}
              >
                <Icon name={l.icon} size={12} />{l.name}
              </span>
            )
          ) : (
            <button
              key={l.kind + l.id}
              className={`inline-flex items-center gap-1 border border-line bg-page rounded-full py-px pr-2 pl-1.5 text-[12px] text-muted no-underline max-w-full min-w-0 overflow-hidden text-ellipsis whitespace-nowrap hover:text-ink hover:border-accent ${
                l.online === false ? 'opacity-50' : ''
              } ${l.kind === 'deploy' ? 'border-dashed' : ''}`}
              title={l.title}
              onClick={() => {
                ui.state.settings = 'machines'
                ui.emit()
              }}
            >
              <Icon name={l.icon} size={12} />{l.name}
            </button>
          ),
        )
      )}
    </div>
  )
}
