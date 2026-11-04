import Sheet from './Sheet'
import TaskView from './TaskView'

export default function TaskSheet({ id, onClose }: { id: string; onClose: () => void }) {
  return (
    <Sheet title="任务" wide onClose={onClose}>
      <TaskView id={id} embedded />
    </Sheet>
  )
}
