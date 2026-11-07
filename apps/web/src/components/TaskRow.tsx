import { useState } from 'react'
import { wb } from '../lib/app'
import { looks, taskStates } from '../lib/status'
import { personaFor, runMeta, type CardTask } from '../lib/workbench'
import { Who } from './Avatar'
import AgentIcon, { agentTitle } from './AgentIcon'
import { ApprovalStrip } from './ApprovalCard'
import Icon from './Icon'

/**
 * A task on the workbench, one line: title, then the agent, the state (with a
 * small run-status icon when there is a run) and the persona/agent avatar.
 */
/**
 * Who works on a task, and where: the agent of its current run (git
 * operations and health checks run no agent: "sous"), else whoever did its
 * latest work run, else what the task asks for. The persona's agent is only
 * the last resort, in the caller (end-to-end review 4.4).
 */
export function taskWho(task: Pick<CardTask, 'run' | 'task'>): { agent: string; nodeId: string } {
  const run = task.run && task.run.agent !== 'sous' ? task.run : undefined
  if (run) return { agent: run.agent, nodeId: run.node_id }
  return {
    agent: task.task.last_agent || task.task.agent_pref || '',
    nodeId: task.task.last_node || task.task.node_pref || '',
  }
}

export default function TaskRow({ task, onOpen, selected = false }: { task: CardTask; onOpen: () => void; selected?: boolean }) {
  const info = taskStates[task.state]
  const data = wb.state.data
  const persona = data ? personaFor(data, task.task, task.run) : undefined
  const { agent, nodeId } = taskWho(task)
  const shownAgent = agent || persona?.agent
  const node = data?.nodes.find((n) => n.id === nodeId)
  const closed = task.state === 'closed'
  const look = task.runLook ? looks[task.runLook] : undefined
  const stateText = task.detail || info.text
  const meta = task.run && node ? runMeta(task.run, node) : undefined
  // A pending approval that waits for a person turns the row into it (same
  // height); it turns back once answered. The review agent's are not shown.
  const [answered, setAnswered] = useState<string[]>([])
  const asking = wb.state.inbox?.approvals.find((a) => a.review === 'user' && !answered.includes(a.id)
    && (a.task_id === task.task.id || (!!task.run && a.run_id === task.run.id)))
  if (asking) {
    return (
      <ApprovalStrip
        approval={asking}
        size="row"
        onOpen={onOpen}
        onDecided={() => setAnswered((l) => [...l, asking.id])}
        className={`w-full ${selected ? 'shadow-[inset_0_0_0_1.5px_var(--accent)]' : ''}`}
      />
    )
  }
  const label = `${task.task.title}，${stateText}${look ? '，运行' + look.text : ''}，${shownAgent ? agentTitle(shownAgent) : '未分配'}`

  return (
    <button
      className={`flex items-center gap-2.5 w-full text-left border-0 px-2.5 py-3 min-h-[48px] rounded-[12px] min-w-0 transition-colors ${selected ? 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)]' : 'bg-transparent hover:bg-hover active:bg-hover'}`}
      aria-label={label}
      aria-current={selected || undefined}
      title={task.summary}
      onClick={onOpen}
    >
      <span className={`flex-1 min-w-0 text-[13.5px] overflow-hidden text-ellipsis whitespace-nowrap ${selected ? 'text-ink font-semibold' : closed ? 'text-faint' : 'text-muted'}`}>{task.task.title}</span>
      <span className="text-[11.5px] text-faint whitespace-nowrap flex-none max-w-[30%] overflow-hidden text-ellipsis" title={[meta?.model ? `模型 ${meta.model}` : agent ? '执行的 agent' : '预设或默认的 agent', node?.name].filter(Boolean).join(' · ')}>{shownAgent ? agentTitle(shownAgent) : '未分配'}</span>
      <span className={`inline-flex items-center gap-1 text-[11.5px] whitespace-nowrap flex-none opacity-85 ${closed ? 'text-faint' : `tone-${look?.tone ?? info.tone}`}`} title={look ? `运行${look.text}` : undefined}>
        {look && <Icon name={look.icon} size={11} stroke={2.2} className={task.runLook === 'running' ? 'stage-current' : ''} />}{stateText}
      </span>
      {persona ? <Who persona={persona} size={20} /> : shownAgent ? <AgentIcon name={shownAgent} size={18} /> : null}
    </button>
  )
}
