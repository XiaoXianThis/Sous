import { useEffect } from 'react'
import { ui, useUi } from '../lib/app'
import Icon from './Icon'
import Machines from './Machines'
import Memories from './Memories'
import Personas from './Personas'
import Schedules from './Schedules'
import Settings from './Settings'
import Usage from './Usage'

const tabs = [
  { key: 'machines', text: '机器', icon: 'laptop' },
  { key: 'personas', text: '岗位', icon: 'user' },
  { key: 'schedules', text: '定时任务', icon: 'calendar' },
  { key: 'memories', text: '记忆', icon: 'bulb' },
  { key: 'usage', text: '用量', icon: 'chart' },
  { key: 'settings', text: '通知与默认', icon: 'bell' },
  { key: 'account', text: '账号', icon: 'user' },
]
const statusText = { connecting: '连接中', online: '已连接', offline: '离线，正在重连' }

export default function SettingsModal({ onLogout }: { onLogout: () => void }) {
  const uiState = useUi()
  const close = () => {
    ui.state.settings = ''
    ui.emit()
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !document.querySelector('.embed > .backdrop:not(:first-child)')) close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  return (
    <div className="fixed inset-0 z-[28] bg-page grid grid-cols-[220px_1fr] max-[760px]:grid-cols-1 max-[760px]:grid-rows-[auto_1fr]" role="dialog" aria-modal="true" aria-label="设置">
      <nav className="bg-sunken border-r border-line px-2.5 pt-[calc(14px+env(safe-area-inset-top))] pb-3.5 grid content-start gap-0.5 max-[760px]:flex max-[760px]:flex-nowrap max-[760px]:overflow-x-auto max-[760px]:gap-1 max-[760px]:px-2.5 max-[760px]:py-2.5 max-[760px]:border-r-0 max-[760px]:border-b max-[760px]:border-line max-[760px]:items-center" aria-label="设置分类">
        <header className="flex items-center justify-between px-1.5 pb-2.5 pl-2.5 max-[760px]:px-2 max-[760px]:pl-1 max-[760px]:pb-0 max-[760px]:flex-none">
          <h2 className="m-0 text-[18px] max-[760px]:hidden">设置</h2>
          <button className="icon-btn hidden max-[760px]:inline-grid" aria-label="关闭设置" onClick={close}><Icon name="x" /></button>
        </header>
        {tabs.map((t) => (
          <button
            key={t.key}
            className={`flex gap-2.5 items-center border-0 bg-none text-left px-3 py-[9px] rounded-[10px] max-[760px]:flex-none max-[760px]:px-2.5 max-[760px]:py-1.5 ${
              uiState.settings === t.key ? 'bg-panel text-ink font-semibold shadow-[0_0_0_1px_var(--line)]' : 'text-muted hover:bg-line hover:text-ink'
            }`}
            aria-current={uiState.settings === t.key ? 'page' : undefined}
            onClick={() => {
              ui.state.settings = t.key
              ui.emit()
            }}
          >
            <Icon name={t.icon} size={16} />{t.text}
          </button>
        ))}
      </nav>
      <div className="relative overflow-y-auto px-7 pt-[calc(18px+env(safe-area-inset-top))] pb-10 max-[760px]:px-4 max-[760px]:pt-3.5 max-[760px]:pb-[30px]">
        <button className="icon-btn absolute top-[calc(12px+env(safe-area-inset-top))] right-3.5 max-[760px]:hidden" aria-label="关闭设置" onClick={close}><Icon name="x" /></button>
        <h2 className="m-0 mb-3.5 text-[20px] max-[760px]:hidden">{tabs.find((t) => t.key === uiState.settings)?.text}</h2>
        <div className="embed max-w-[760px]">
          {uiState.settings === 'machines' ? (
            <Machines onClose={close} />
          ) : uiState.settings === 'personas' ? (
            <Personas onClose={close} />
          ) : uiState.settings === 'schedules' ? (
            <Schedules onClose={close} />
          ) : uiState.settings === 'memories' ? (
            <Memories onClose={close} />
          ) : uiState.settings === 'usage' ? (
            <Usage onClose={close} />
          ) : uiState.settings === 'settings' ? (
            <Settings onClose={close} />
          ) : (
            <div className="grid gap-3 justify-items-start">
              <p className="m-0 flex gap-2 items-center">
                <span className={`w-2 h-2 rounded-full ${uiState.status === 'online' ? 'bg-ok' : uiState.status === 'offline' ? 'bg-danger' : 'bg-muted'}`}></span>
                {statusText[uiState.status]}
              </p>
              <button className="btn ghost inline-flex gap-1.5 items-center" onClick={onLogout}><Icon name="logout" size={16} />退出登录</button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
