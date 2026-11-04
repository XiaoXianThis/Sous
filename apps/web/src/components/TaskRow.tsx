import { taskStates } from '../lib/status'
import type { CardTask } from '../lib/workbench'
import Icon from './Icon'
import StageBar from './StageBar'

export default function TaskRow({ task, onOpen }: { task: CardTask; onOpen: () => void }) {
  const info = taskStates[task.state]
  const showBar = task.state === 'active' || task.state === 'attention'
  const label = `${task.task.title}，${info.text}${task.detail && task.detail !== info.text ? '，' + task.detail : ''}`

  return (
    <button className="grid gap-[3px] w-full text-left border-0 bg-none px-1.5 py-1.5 rounded-[9px] min-w-0 hover:bg-sunken" aria-label={label} onClick={onOpen}>
      <span className="flex items-center gap-1.5 min-w-0">
        <span className={`tone-${info.tone}`}><Icon name={info.icon} size={13} /></span>
        <span className="flex-1 text-[14px] font-medium overflow-hidden text-ellipsis whitespace-nowrap">{task.task.title}</span>
        <span className={`text-[12px] font-semibold whitespace-nowrap tone-${info.tone}`}>
          {info.text}{task.detail && task.detail !== info.text ? ` · ${task.detail}` : ''}
        </span>
      </span>
      {task.summary && (
        <span className="text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap">{task.summary}</span>
      )}
      {showBar && <StageBar stages={task.stages} />}
    </button>
  )
}
