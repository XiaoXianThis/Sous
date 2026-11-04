import { wb } from '../lib/app'
import { oneLine, tail } from '../lib/live'
import { looks } from '../lib/status'
import { runMeta, type CardAgent } from '../lib/workbench'
import { Who } from './Avatar'
import Icon from './Icon'

const toolDot: Record<string, string> = { completed: 'bg-ok', failed: 'bg-danger', in_progress: 'bg-info live-dot' }

export default function AgentCard({ agent, onOpen }: { agent: CardAgent; onOpen: () => void }) {
  const run = agent.run
  const node = wb.state.data?.nodes.find((n) => n.id === run.node_id)
  const meta = runMeta(run, node)
  const acts = tail(wb.state.live[run.id] ?? [], 2)
  const look = looks[agent.look]
  const label = `${agent.title}，${look.text}，${agent.persona?.name ?? '未分配'}，${meta.agentTitle}`
  const working = agent.look === 'running' || agent.look === 'review'

  return (
    <button
      className={`relative grid gap-2 w-full text-left border-0 rounded-[16px] p-3 min-w-0 overflow-hidden transition-colors tone-${look.tone} bg-[color-mix(in_srgb,currentColor_6%,var(--panel))] shadow-[inset_0_0_0_1px_color-mix(in_srgb,currentColor_16%,transparent)] hover:bg-[color-mix(in_srgb,currentColor_10%,var(--panel))]`}
      aria-label={label}
      onClick={onOpen}
    >
      {working && <span className="absolute inset-x-0 top-0 h-[2px] shimmer" aria-hidden="true"></span>}
      <span className="flex items-center gap-2.5 min-w-0">
        <Who persona={agent.persona} agent={run.agent || 'sous'} size={32} />
        <span className="grid min-w-0 flex-1 leading-tight">
          <span className="text-[14px] font-semibold text-ink overflow-hidden text-ellipsis whitespace-nowrap">{agent.title}</span>
          <span className="flex items-center gap-2 text-[11.5px] text-muted min-w-0 mt-0.5">
            {meta.model && (
              <span className="inline-flex items-center gap-0.5 min-w-0" title="模型">
                <Icon name="cpu" size={11} /><span className="overflow-hidden text-ellipsis whitespace-nowrap">{meta.model}</span>
              </span>
            )}
            {meta.effort && (
              <span className="inline-flex items-center gap-0.5 whitespace-nowrap" title="思考强度"><Icon name="brain" size={11} />{meta.effort}</span>
            )}
            {node && (
              <span className="inline-flex items-center gap-0.5 whitespace-nowrap" title="机器"><Icon name={node.role === 'server' ? 'server' : 'laptop'} size={11} />{node.name}</span>
            )}
          </span>
        </span>
        <span className="grid place-items-center w-7 h-7 rounded-full flex-none bg-[color-mix(in_srgb,currentColor_14%,transparent)]" title={look.text}>
          <Icon name={look.icon} size={13} stroke={2.2} />
        </span>
      </span>
      <span className="grid auto-rows-[18px] h-[36px] overflow-hidden content-start rounded-[10px] bg-[color-mix(in_srgb,var(--sunken)_70%,transparent)] px-2 py-0 text-[11.5px] font-mono text-muted">
        {acts.length === 0 ? (
          <span className="flex items-center gap-1.5 min-w-0 leading-[36px] h-[36px] text-faint">
            <span className="typing-dot w-1 h-1 rounded-full bg-current"></span>
            <span className="typing-dot w-1 h-1 rounded-full bg-current"></span>
            <span className="typing-dot w-1 h-1 rounded-full bg-current"></span>
          </span>
        ) : (
          acts.map((act, i) => (
            <span
              key={act.id}
              className={`flex items-center gap-1.5 min-w-0 leading-[18px] ${i === 0 && acts.length === 2 ? 'mt-0' : ''} ${
                act.kind === 'thought' ? 'text-faint italic' : act.kind === 'status' || act.kind === 'permission' ? 'text-warm' : 'text-ink/80'
              }`}
            >
              {act.kind === 'tool' ? (
                <span className={`w-1.5 h-1.5 rounded-full flex-none ${toolDot[act.status ?? ''] ?? 'bg-faint'}`}></span>
              ) : act.kind === 'thought' ? (
                <Icon name="brain" size={11} />
              ) : (
                <Icon name="forward" size={11} />
              )}
              <span className="overflow-hidden text-ellipsis whitespace-nowrap">{oneLine(act)}</span>
            </span>
          ))
        )}
      </span>
    </button>
  )
}
