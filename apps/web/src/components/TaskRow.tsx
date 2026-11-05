import { wb } from '../lib/app'
import { looks, taskStates } from '../lib/status'
import { personaFor, runMeta, type CardTask } from '../lib/workbench'
import { Who } from './Avatar'
import AgentIcon, { agentTitle } from './AgentIcon'
import Icon from './Icon'

/**
 * A task on the workbench, one line: title, then the agent, the state (with a
 * small run-status icon when there is a run) and the persona/agent avatar.
 */
export default function TaskRow({ task, onOpen, selected = false }: { task: CardTask; onOpen: () => void; selected?: boolean }) {
  const info = taskStates[task.state]
  const data = wb.state.data
  const persona = data ? personaFor(data, task.task, task.run) : undefined
  const agent = task.run?.agent || persona?.agent || task.task.agent_pref
  const node = data?.nodes.find((n) => n.id === (task.run?.node_id || task.task.node_pref))
  const closed = task.state === 'closed'
  const look = task.runLook ? looks[task.runLook] : undefined
  const stateText = task.detail || info.text
  const meta = task.run && node ? runMeta(task.run, node) : undefined
  const label = `${task.task.title}，${stateText}${look ? '，运行' + look.text : ''}，${agent ? agentTitle(agent) : '未分配'}`

  return (
    <button
      className={`flex items-center gap-2.5 w-full text-left border-0 bg-transparent px-2.5 py-3 min-h-[48px] rounded-[12px] min-w-0 transition-colors ${selected ? 'bg-[color-mix(in_srgb,var(--accent)_12%,transparent)]' : 'hover:bg-hover active:bg-hover'}`}
      aria-label={label}
      aria-current={selected || undefined}
      title={task.summary}
      onClick={onOpen}
    >
      <span className={`flex-1 min-w-0 text-[13.5px] overflow-hidden text-ellipsis whitespace-nowrap ${closed ? 'text-faint' : 'text-muted'}`}>{task.task.title}</span>
      <span className="text-[11.5px] text-faint whitespace-nowrap flex-none max-w-[30%] overflow-hidden text-ellipsis" title={meta?.model ? `模型 ${meta.model}` : '接手的 agent'}>{agent ? agentTitle(agent) : '未分配'}</span>
      <span className={`inline-flex items-center gap-1 text-[11.5px] whitespace-nowrap flex-none opacity-85 ${closed ? 'text-faint' : `tone-${look?.tone ?? info.tone}`}`} title={look ? `运行${look.text}` : undefined}>
        {look && <Icon name={look.icon} size={11} stroke={2.2} className={task.runLook === 'running' ? 'stage-current' : ''} />}{stateText}
      </span>
      {persona ? <Who persona={persona} size={20} /> : agent ? <AgentIcon name={agent} size={18} /> : null}
    </button>
  )
}
