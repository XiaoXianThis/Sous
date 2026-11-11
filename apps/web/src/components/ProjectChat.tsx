import { useEffect, useState } from 'react'
import { ago } from '../lib/time'
import { api } from '../lib/api'
import { loadProjectThreads, newProjectThread, pickProjectThread, sendToProject, ui, useChat, useUi, useWb } from '../lib/app'
import { dmTargets, dmTo } from '../lib/dm'
import type { Project, Task } from '../lib/types'
import ChatView from './ChatView'
import Icon from './Icon'
import MeetingList from './MeetingList'
import MeetingView from './MeetingView'
import { BackButton } from './PhoneHeader'
import { usePhone } from '../lib/phone'

/**
 * sous, scoped to one project: its chats only see and touch this project. The
 * task selected in the project dialog goes along with each message.
 */
export default function ProjectChat({ project, task, onClose }: { project: Project; task?: Task; onClose?: () => void }) {
  const chatState = useChat()
  const phone = usePhone()
  const uiState = useUi()
  const wbState = useWb()
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
  // A meeting opened from one of this project's topics takes over the panel.
  if (uiState.meeting) return <MeetingView />
  // Who the user may talk to directly: running runs, idle tasks, the consultant.
  const runs = [...(wbState.data?.runs ?? []), ...Object.values(chatState.runs).map((r) => r.run)]
  const targets = dmTargets(project, wbState.data?.tasks ?? [], runs)

  async function sendDm(target: (typeof targets)[number], text: string) {
    let id = threadId
    if (!id) id = (await newProjectThread(project.id, title)).id
    await api.dm(id, text, dmTo(target), uiState.openTopic || undefined)
  }

  return (
    <ChatView
      label={`和 sous 聊 ${project.name}`}
      threadId={threadId}
      threads={threads}
      title={thread && thread.title !== title ? thread.title : 'sous'}
      avatar={null}
      threadLabel={(t) => (t.title === title ? `对话 · ${ago(t.updated_at)}` : t.title)}
      threadIcon={() => 'chat'}
      onPick={(id) => pickProjectThread(project.id, id)}
      onNew={() => newProjectThread(project.id, title)}
      onSend={(text, attachments, mentions) => sendToProject(project.id, title, text, withTask ? { task_id: task.id } : undefined, attachments, mentions)}
      onSendIn={(topic, text) => sendToProject(project.id, title, text, withTask ? { task_id: task.id } : undefined, [], [], topic)}
      context={withTask ? { icon: 'target', text: task.title } : { icon: 'folder', text: project.name }}
      onDropContext={withTask ? () => setDropTask(true) : undefined}
      targets={targets}
      dmTarget={uiState.dmTarget[project.id] ?? ''}
      onDmTarget={(key) => { ui.state.dmTarget[project.id] = key; ui.emit() }}
      onDm={(target, text) => sendDm(target, text)}
      placeholder={`问问 ${project.name} 的事，或者让 sous 派个任务…`}
      empty={
        <>
          <p className="text-[15px] font-semibold m-0">和 sous 聊 {project.name}</p>
          <p className="text-[13px] text-muted m-0 max-w-[260px]">这里的对话只看得到、也只会动这个项目：建的任务都归到它下面。</p>
        </>
      }
      // On a phone the chat covers the project page, so it leads with a back button.
      leading={phone && onClose ? <BackButton label="返回项目" onClick={onClose} /> : undefined}
      actions={
        <>
          {threadId && <MeetingList parentThreadId={threadId} />}
          {onClose && !phone && (
            <button className="icon-btn" data-back aria-label="收起项目对话" data-tip="收起对话" data-tip-pos="bottom" onClick={onClose}><Icon name="panel" size={18} /></button>
          )}
        </>
      }
    />
  )
}
