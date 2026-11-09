import { newThread, openThread, send, ui, useChat, useUi, useWb } from '../lib/app'
import ChatView from './ChatView'
import Icon from './Icon'

/** The main chat column: sous with every project in view. */
export default function ChatPanel({ onCollapse }: { onCollapse?: () => void }) {
  const chatState = useChat()
  const uiState = useUi()
  const wbState = useWb()
  const threads = chatState.threads.filter((t) => !t.project_id)
  const thread = threads.find((t) => t.id === uiState.current)

  // What the next message is about: the project or task on the workbench.
  const context = (() => {
    if (uiState.dropContext || !wbState.data) return null
    const r = uiState.route
    if (r.view === 'task') {
      const t = wbState.data.tasks.find((x) => x.id === r.id)
      return { icon: 'target', text: t ? t.title : '这个任务' }
    }
    if (r.view === 'project') {
      const p = wbState.data.projects.find((x) => x.id === r.id)
      return p ? { icon: 'folders', text: p.name } : null
    }
    return null
  })()

  return (
    <ChatView
      label="和 sous 对话"
      threadId={uiState.current}
      threads={threads}
      title={!thread || thread.kind === 'main' ? 'sous' : thread.title}
      avatar={
        <span className="grid place-items-center w-7 h-7 rounded-[9px] bg-brand text-white">
          <Icon name={thread && thread.kind !== 'main' ? 'chat' : 'cloche'} size={15} stroke={2} />
        </span>
      }
      threadLabel={(t) => (t.kind === 'main' ? '主线' : t.title)}
      threadIcon={(t) => (t.kind === 'main' ? 'cloche' : 'chat')}
      onPick={openThread}
      onNew={newThread}
      onSend={send}
      onSendIn={(topic, text) => send(text, [], [], topic)}
      inbox
      context={context}
      onDropContext={() => {
        ui.state.dropContext = true
        ui.emit()
      }}
      placeholder="和 sous 说…"
      empty={
        <>
          <img src="/icon.svg" alt="" width={48} height={48} className="rounded-[14px]" />
          <p className="text-[16px] font-semibold m-0">有什么要交给我的？</p>
          <p className="text-[13px] text-muted m-0 max-w-[260px]">说说你要做的事，sous 会派给合适的 agent，并在这里汇报进度。</p>
        </>
      }
      actions={
        onCollapse && (
          <button className="icon-btn collapse-chat" aria-label="收起对话" data-tip="收起" data-tip-pos="bottom" onClick={onCollapse}><Icon name="panel" size={18} /></button>
        )
      }
    />
  )
}
