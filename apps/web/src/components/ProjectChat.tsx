import { useEffect, useState } from 'react'
import { ago } from '../lib/time'
import { loadProjectThreads, newProjectThread, pickProjectThread, sendToProject, useChat, useUi } from '../lib/app'
import type { Project, Task } from '../lib/types'
import ChatView from './ChatView'
import Icon from './Icon'
import ProjectTile from './ProjectTile'

/**
 * sous, scoped to one project: its chats only see and touch this project. The
 * task selected in the project dialog goes along with each message.
 */
export default function ProjectChat({ project, task, onClose }: { project: Project; task?: Task; onClose?: () => void }) {
  const chatState = useChat()
  const uiState = useUi()
  const [dropTask, setDropTask] = useState(false)
  const threads = chatState.threads.filter((t) => t.project_id === project.id)
  const threadId = uiState.projectThread[project.id] ?? ''
  const thread = threads.find((t) => t.id === threadId)

  useEffect(() => {
    void loadProjectThreads(project.id)
  }, [project.id])
  useEffect(() => setDropTask(false), [task?.id])

  const title = `${project.name} 的对话`
  const withTask = task && !dropTask

  return (
    <ChatView
      label={`和 sous 聊 ${project.name}`}
      threadId={threadId}
      threads={threads}
      title={thread && thread.title !== title ? thread.title : 'sous'}
      avatar={<ProjectTile name={project.name} id={project.id} size={28} misc={project.id === 'misc'} />}
      threadLabel={(t) => (t.title === title ? `对话 · ${ago(t.updated_at)}` : t.title)}
      threadIcon={() => 'chat'}
      onPick={(id) => pickProjectThread(project.id, id)}
      onNew={() => newProjectThread(project.id, title)}
      onSend={(text) => sendToProject(project.id, title, text, withTask ? { task_id: task.id } : undefined)}
      context={withTask ? { icon: 'target', text: task.title } : { icon: 'folder', text: project.name }}
      onDropContext={withTask ? () => setDropTask(true) : undefined}
      placeholder={`问问 ${project.name} 的事，或者让 sous 派个任务…`}
      empty={
        <>
          <ProjectTile name={project.name} id={project.id} size={44} misc={project.id === 'misc'} />
          <p className="text-[15px] font-semibold m-0">和 sous 聊 {project.name}</p>
          <p className="text-[13px] text-muted m-0 max-w-[260px]">这里的对话只看得到、也只会动这个项目：建的任务都归到它下面。</p>
        </>
      }
      actions={
        onClose && (
          <button className="icon-btn" data-back aria-label="收起项目对话" data-tip="收起对话" data-tip-pos="bottom" onClick={onClose}><Icon name="panel" size={18} /></button>
        )
      }
    />
  )
}
