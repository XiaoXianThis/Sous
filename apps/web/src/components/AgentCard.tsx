import { wb } from '../lib/app'
import { oneLine, tail } from '../lib/live'
import { avatarOf, looks } from '../lib/status'
import { runMeta, type CardAgent } from '../lib/workbench'
import Avatar from './Avatar'
import Icon from './Icon'

export default function AgentCard({ agent, onOpen }: { agent: CardAgent; onOpen: () => void }) {
  const run = agent.run
  const node = wb.state.data?.nodes.find((n) => n.id === run.node_id)
  const meta = runMeta(run, node)
  const metaText = [
    agent.persona?.name ?? '未分配',
    meta.agentTitle,
    meta.model,
    meta.effort ? `思考 ${meta.effort}` : '',
    node ? `@ ${node.name}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
  const acts = tail(wb.state.live[run.id] ?? [], 2)
  const a = avatarOf(agent.persona, run.agent)
  const look = looks[agent.look]
  const label = `${agent.title}，${look.text}，${agent.persona?.name ?? '未分配'}，${meta.agentTitle}`

  return (
    <button
      className="grid gap-[3px] w-full text-left border border-line bg-panel rounded-[11px] px-[9px] py-2 min-w-0 hover:border-accent"
      aria-label={label}
      onClick={onOpen}
    >
      <span className="flex items-center gap-1.5 min-w-0">
        <Avatar glyph={a.glyph} color={a.color} title={a.title} size={22} ring={look.tone === 'neutral' ? '' : look.tone} />
        <span className="flex-1 text-[14px] font-semibold overflow-hidden text-ellipsis whitespace-nowrap">{agent.title}</span>
        <span className={`inline-flex items-center gap-[3px] text-[12px] font-semibold whitespace-nowrap tone-${look.tone}`}>
          <Icon name={look.icon} size={12} />{look.text}
        </span>
      </span>
      <span className="text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap">{metaText}</span>
      <span className="grid auto-rows-[17px] h-[34px] overflow-hidden content-start text-[12px] text-muted mt-px">
        {acts.length === 0 ? (
          <span className="flex items-center gap-1 min-w-0 leading-[17px] text-muted italic">等待输出…</span>
        ) : (
          acts.map((act) => (
            <span key={act.id} className={`flex items-center gap-1 min-w-0 leading-[17px] ${act.kind === 'thought' ? 'text-[color-mix(in_srgb,var(--muted)_80%,transparent)]' : act.kind === 'status' || act.kind === 'permission' ? 'text-warm' : ''}`}>
              {act.kind === 'tool' ? (
                <span className={`w-[5px] h-[5px] rounded-full flex-none ${act.status === 'completed' ? 'bg-ok' : act.status === 'failed' ? 'bg-danger' : act.status === 'in_progress' ? 'bg-info' : 'bg-muted'}`}></span>
              ) : act.kind === 'thought' ? (
                <Icon name="bulb" size={10} />
              ) : null}
              <span className="overflow-hidden text-ellipsis whitespace-nowrap">{oneLine(act)}</span>
            </span>
          ))
        )}
      </span>
    </button>
  )
}
