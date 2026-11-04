import { useEffect } from 'react'
import { closeSheet, followHash, go, loadInbox, loadOverview, openThread, ui, useChat, useUi, useWb } from '../lib/app'
import ChatPanel from './ChatPanel'
import Icon from './Icon'
import ProjectSheet from './ProjectSheet'
import RunSheet from './RunSheet'
import SettingsModal from './SettingsModal'
import TaskDetail from './TaskDetail'
import TaskSheet from './TaskSheet'
import Workbench from './Workbench'

const badge = 'absolute top-0.5 min-w-4 h-4 rounded-[8px] px-1 bg-danger text-page text-[11px] leading-4 text-center font-semibold'

export default function Shell({ onLogout }: { onLogout: () => void }) {
  const chat = useChat()
  const uiState = useUi()
  const wb = useWb()

  useEffect(() => {
    void followHash(location.hash)
    const onHash = () => void followHash(location.hash)
    addEventListener('hashchange', onHash)
    addEventListener('popstate', onHash)
    const onSW = (e: MessageEvent) => {
      if (e.data?.type === 'open' && typeof e.data.url === 'string') void followHash(new URL(e.data.url, location.origin).hash)
    }
    navigator.serviceWorker?.addEventListener('message', onSW)
    return () => {
      removeEventListener('hashchange', onHash)
      removeEventListener('popstate', onHash)
      navigator.serviceWorker?.removeEventListener('message', onSW)
    }
  }, [])

  // The workbench follows tasks, machines, approvals and Run lifecycle events.
  useEffect(() => {
    const timer = setTimeout(() => {
      void loadOverview()
      void loadInbox()
    }, 150)
    return () => clearTimeout(timer)
  }, [chat.tasksRev, chat.nodesRev, chat.inboxRev, chat.runsRev])

  const view = uiState.route.view
  const pending = (wb.inbox?.approvals.filter((a) => a.review === 'user').length ?? 0) + (wb.inbox?.tasks.length ?? 0)
  const openSettings = () => {
    ui.state.settings = 'machines'
    ui.emit()
  }
  const setTab = (tab: 'chat' | 'work') => {
    ui.state.tab = tab
    ui.emit()
  }
  const closeTask = () => {
    ui.state.newTask = ''
    ui.emit()
  }

  return (
    <>
      <div className={`shell ${uiState.chatOpen ? '' : 'chat-closed'}`} data-tab={uiState.tab}>
        <nav className="rail bg-sunken border-r border-line flex flex-col items-center gap-1.5 pt-[calc(12px+env(safe-area-inset-top))] pb-[calc(12px+env(safe-area-inset-bottom))]" aria-label="主导航">
          <span className="text-accent mb-2.5" title="sous"><Icon name="wok" size={22} /></span>
          <button
            className="relative w-10 h-10 rounded-[12px] border-0 bg-panel text-ink grid place-items-center shadow-[0_0_0_1px_var(--line)]"
            title="工作台"
            aria-label="工作台"
            aria-current="page"
            onClick={() => go({ view: 'overview', id: '' })}
          >
            <Icon name="grid" size={20} />
            {pending > 0 && <span className={`${badge} right-0`}>{pending}</span>}
          </button>
          {!uiState.chatOpen && (
            <button
              className="relative w-10 h-10 rounded-[12px] border-0 bg-none text-muted grid place-items-center hover:bg-line hover:text-ink"
              title="打开对话"
              aria-label="打开对话"
              onClick={() => {
                ui.state.chatOpen = true
                ui.emit()
              }}
            >
              <Icon name="chat" size={20} />
            </button>
          )}
          <span className="flex-1"></span>
          <button className="relative w-10 h-10 rounded-[12px] border-0 bg-none text-muted grid place-items-center hover:bg-line hover:text-ink" title="设置" aria-label="设置" onClick={openSettings}>
            <Icon name="settings" size={20} />
          </button>
        </nav>

        <main className="work overflow-y-auto min-w-0 min-h-0 bg-page pt-[env(safe-area-inset-top)]" aria-label="工作台">
          <Workbench />
        </main>

        <aside className="side border-l border-line min-h-0 min-w-0">
          <ChatPanel
            onCollapse={() => {
              ui.state.chatOpen = false
              ui.emit()
            }}
          />
        </aside>

        <nav className="tabs" aria-label="主导航">
          <button
            className={`relative border-0 bg-none grid justify-items-center gap-px pt-[7px] pb-1.5 text-[11px] ${uiState.tab === 'work' ? 'text-accent' : 'text-muted'}`}
            onClick={() => setTab('work')}
          >
            <Icon name="grid" size={20} /><span>工作台</span>{pending > 0 && <span className={`${badge} right-[calc(50%-22px)]`}>{pending}</span>}
          </button>
          <button
            className={`relative border-0 bg-none grid justify-items-center gap-px pt-[7px] pb-1.5 text-[11px] ${uiState.tab === 'chat' ? 'text-accent' : 'text-muted'}`}
            onClick={() => setTab('chat')}
          >
            <Icon name="chat" size={20} /><span>对话</span>
          </button>
          <button className="relative border-0 bg-none grid justify-items-center gap-px pt-[7px] pb-1.5 text-[11px] text-muted" onClick={openSettings}>
            <Icon name="settings" size={20} /><span>设置</span>
          </button>
        </nav>
      </div>

      {view === 'task' ? (
        <TaskSheet id={uiState.route.id} onClose={closeSheet} />
      ) : view === 'project' ? (
        <ProjectSheet id={uiState.route.id} onClose={closeSheet} />
      ) : null}

      {uiState.newTask !== '' && (
        <TaskDetail
          id="new"
          projectId={uiState.newTask}
          onClose={closeTask}
          onOpenTask={(id) => {
            closeTask()
            go({ view: 'task', id })
          }}
          onOpenThread={(tid) => {
            closeTask()
            void openThread(tid)
            ui.state.chatOpen = true
            ui.emit()
          }}
        />
      )}

      {uiState.runSheet !== '' && (
        <RunSheet
          id={uiState.runSheet}
          onClose={() => {
            ui.state.runSheet = ''
            ui.emit()
          }}
        />
      )}
      {uiState.settings !== '' && <SettingsModal onLogout={onLogout} />}
      {wb.error && !wb.data && <p className="fixed bottom-4 left-1/2 -translate-x-1/2 bg-panel border border-danger text-danger px-3.5 py-2 rounded-[10px] m-0 z-40" role="alert">加载工作台失败：{wb.error}</p>}
    </>
  )
}
