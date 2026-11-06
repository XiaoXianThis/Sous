import { lazy, Suspense, useEffect } from 'react'
import { resolved, setTheme, useTheme } from '../lib/theme'
import { closeSheet, followHash, go, loadInbox, loadOverview, openThread, ui, useChat, useUi, useWb } from '../lib/app'
import { useStoredWidth } from '../lib/resize'
import ChatPanel from './ChatPanel'
import Icon from './Icon'
import ProjectSheet from './ProjectSheet'
import ResizeHandle from './ResizeHandle'
import RunSheet from './RunSheet'
const SettingsModal = lazy(() => import('./SettingsModal'))
import TaskDetail from './TaskDetail'
import Workbench from './Workbench'

const badge = 'absolute min-w-[17px] h-[17px] rounded-full px-[4px] bg-warm text-white text-[10.5px] leading-[17px] text-center font-bold shadow-[0_0_0_2px_var(--rail)] tabular-nums'

function RailButton({ tip, active = false, toggle = false, onClick, children, count = 0 }: {
  tip: string
  active?: boolean
  /** A panel toggle: drawn tinted when on, not as the current page. */
  toggle?: boolean
  onClick: () => void
  children: React.ReactNode
  count?: number
}) {
  return (
    <button
      className={`relative w-10 h-10 rounded-[10px] border-0 grid place-items-center transition-colors ${
        active
          ? toggle
            ? 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-accent'
            : 'bg-hover text-ink'
          : 'bg-transparent text-muted hover:bg-hover hover:text-ink'
      }`}
      data-tip={tip}
      aria-label={tip}
      aria-pressed={active}
      onClick={onClick}
    >
      {children}
      {count > 0 && <span className={`${badge} -top-0.5 -right-0.5`}>{count > 99 ? '99+' : count}</span>}
    </button>
  )
}

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
  const [chatWidth, setChatWidth, resetChatWidth] = useStoredWidth('sous.chatWidth', 400)
  useTheme()
  const theme = resolved()
  const pending = (wb.inbox?.approvals.filter((a) => a.review === 'user').length ?? 0) + (wb.inbox?.tasks.length ?? 0)
  const nodes = wb.data?.nodes ?? []
  const openSettings = (tab = 'machines') => {
    ui.state.settings = tab
    ui.emit()
  }
  const setTab = (tab: 'chat' | 'work') => {
    ui.state.tab = tab
    ui.state.settings = ''
    ui.emit()
  }
  const tabs = [
    { key: 'work', icon: 'grid', text: '工作台', count: pending, on: uiState.tab === 'work' && !uiState.settings, act: () => setTab('work') },
    { key: 'chat', icon: 'chat', text: '对话', count: 0, on: uiState.tab === 'chat' && !uiState.settings, act: () => setTab('chat') },
    { key: 'settings', icon: 'settings', text: '设置', count: 0, on: !!uiState.settings, act: () => openSettings('home') },
  ]
  const tabIndex = Math.max(0, tabs.findIndex((t) => t.on))
  const closeTask = () => {
    ui.state.newTask = ''
    ui.emit()
  }

  return (
    <>
      <div
        className={`shell ${uiState.chatOpen ? '' : 'chat-closed'}`}
        data-tab={uiState.tab}
        data-settings={uiState.settings ? '' : undefined}
        style={{ '--chat-w': `${chatWidth}px` } as React.CSSProperties}>
        <nav className="rail flex flex-col items-center gap-2 pt-[calc(14px+var(--safe-t))] pb-[calc(14px+var(--safe-b))]" aria-label="主导航">
          <img src="/icon.svg" alt="sous" width={32} height={32} className="rounded-[10px] mb-3" />
          <RailButton tip="工作台" active count={pending} onClick={() => go({ view: 'overview', id: '' })}>
            <Icon name="grid" size={20} />
          </RailButton>
          <RailButton
            tip={uiState.chatOpen ? '收起对话' : '打开对话'}
            active={uiState.chatOpen}
            toggle
            onClick={() => {
              ui.state.chatOpen = !ui.state.chatOpen
              ui.emit()
            }}
          >
            <Icon name="chat" size={21} />
          </RailButton>

          <span className="flex-1"></span>

          <div className="grid gap-1.5 justify-items-center mb-1" aria-label="机器">
            {nodes.slice(0, 5).map((n) => {
              const busy = wb.data?.runs.filter((r) => r.node_id === n.id && r.status === 'running').length ?? 0
              return (
                <button
                  key={n.id}
                  className="relative w-9 h-9 rounded-[12px] grid place-items-center border-0 bg-transparent text-muted hover:bg-hover hover:text-ink"
                  data-tip={`${n.name} · ${n.online ? '在线' : '离线'}${busy ? ` · ${busy} 个在跑` : ''}`}
                  aria-label={`${n.name}${n.online ? '' : '（离线）'}`}
                  onClick={() => openSettings('machines')}
                >
                  <Icon name={n.role === 'server' ? 'server' : 'laptop'} size={19} />
                  <span className={`absolute bottom-[5px] right-[5px] w-[9px] h-[9px] rounded-full shadow-[0_0_0_2px_var(--rail)] ${n.online ? (busy ? 'bg-info live-dot' : 'bg-[var(--online)]') : 'bg-faint'}`}></span>
                </button>
              )
            })}
          </div>
          <RailButton tip={theme === 'dark' ? '切换到浅色' : '切换到深色'} onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>
            <Icon name={theme === 'dark' ? 'sun' : 'moon'} size={19} />
          </RailButton>
          <RailButton tip="设置" onClick={() => openSettings('machines')}>
            <Icon name="settings" size={21} />
          </RailButton>
        </nav>

        <main className="work overflow-y-auto min-w-0 min-h-0 pt-[var(--safe-t)]" aria-label="工作台">
          <Workbench />
        </main>

        <aside className="side min-h-0 min-w-0">
          <ResizeHandle
            label="调整对话栏宽度"
            width={chatWidth}
            min={300}
            max={() => Math.min(900, window.innerWidth - 488)}
            onWidth={setChatWidth}
            onReset={resetChatWidth}
          />
          <ChatPanel
            onCollapse={() => {
              ui.state.chatOpen = false
              ui.emit()
            }}
          />
        </aside>

        <nav
          className="tabbar glass"
          aria-label="主导航"
          style={{ '--tabs': tabs.length, '--tab-i': tabIndex } as React.CSSProperties}
        >
          <span className="tabbar-lens" aria-hidden="true"></span>
          {tabs.map((t) => (
            <button
              key={t.key}
              className="tabbar-item"
              aria-label={t.text}
              aria-current={t.on ? 'page' : undefined}
              onClick={() => t.act()}
            >
              <Icon name={t.icon} size={24} />
              <span>{t.text}</span>
              {t.count > 0 && <span className={`badge ${badge} top-[5px] left-[calc(50%+6px)] shadow-none`}>{t.count > 99 ? '99+' : t.count}</span>}
            </button>
          ))}
        </nav>
      </div>

      {view === 'project' ? (
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
      {uiState.settings !== '' && <Suspense fallback={null}><SettingsModal onLogout={onLogout} /></Suspense>}
      {wb.error && !wb.data && (
        <p className="fixed bottom-4 left-1/2 -translate-x-1/2 bg-panel shadow-pop text-danger px-4 py-2.5 rounded-[16px] m-0 z-40 flex items-center gap-2" role="alert">
          <Icon name="alert" size={16} />{wb.error}
        </p>
      )}
    </>
  )
}
