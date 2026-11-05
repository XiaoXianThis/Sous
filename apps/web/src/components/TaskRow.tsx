import { wb } from '../lib/app'
import { looks, taskStates } from '../lib/status'
import { personaFor, runMeta, type CardTask } from '../lib/workbench'
import { Who } from './Avatar'
import AgentIcon, { agentTitle } from './AgentIcon'
import Icon from './Icon'

/**
 * A task on the workbench, two lines tall: status icon, title and state; under
 * it the latest run's status and the agent that took the task (icon + name).
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
      className={`flex items-center gap-2.5 w-full text-left border-0 bg-transparent px-2.5 py-2.5 min-h-[64px] rounded-[12px] min-w-0 transition-colors ${selected ? 'bg-[color-mix(in_srgb,var(--accent)_12%,transparent)]' : 'hover:bg-hover active:bg-hover'}`}
      aria-label={label}
      aria-current={selected || undefined}
      title={task.summary}
      onClick={onOpen}
    >
      <span className={`grid place-items-center flex-none w-7 h-7 rounded-full tone-${info.tone} bg-[color-mix(in_srgb,currentColor_12%,transparent)]`}>
        <Icon name={info.icon} size={15} stroke={2.1} className={task.state === 'active' ? 'stage-current' : ''} />
      </span>
      <span className="grid gap-0.5 flex-1 min-w-0 leading-tight">
        <span className="flex items-center gap-2 min-w-0">
          <span className={`flex-1 min-w-0 text-[13.5px] font-medium overflow-hidden text-ellipsis whitespace-nowrap ${closed ? 'text-muted' : 'text-ink'}`}>{task.task.title}</span>
          <span className={`text-[11.5px] font-medium whitespace-nowrap ${closed ? 'text-faint' : `tone-${info.tone}`}`}>{stateText}</span>
        </span>
        <span className="flex items-center gap-2 min-w-0 text-[11.5px] text-muted">
          {look ? (
            <span className={`inline-flex items-center gap-1 whitespace-nowrap tone-${look.tone}`} title="运行状态">
              <Icon name={look.icon} size={12} stroke={2.2} className={task.runLook === 'running' ? 'stage-current' : ''} />运行{look.text}
            </span>
          ) : (
            <span className="inline-flex items-center gap-1 whitespace-nowrap text-faint" title="运行状态"><Icon name="circle-dashed" size={12} />未运行</span>
          )}
          {meta?.model && <span className="overflow-hidden text-ellipsis whitespace-nowrap min-w-0 text-faint" title="模型">{meta.model}</span>}
          <span className="flex-1"></span>
          {agent ? (
            <span className="inline-flex items-center gap-1.5 whitespace-nowrap flex-none" title="接手的 agent">
              <AgentIcon name={agent} size={16} />{agentTitle(agent)}
            </span>
          ) : (
            <span className="text-faint whitespace-nowrap">未分配</span>
          )}
        </span>
      </span>
      {persona && <Who persona={persona} size={26} />}
    </button>
  )
}
