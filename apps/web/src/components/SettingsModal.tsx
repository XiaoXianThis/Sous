import { useEffect } from 'react'
import { ui, useUi } from '../lib/app'
import { setTheme, useTheme, type ThemePref } from '../lib/theme'
import Icon from './Icon'
import Machines from './Machines'
import Memories from './Memories'
import Models from './Models'
import Personas from './Personas'
import Schedules from './Schedules'
import Settings from './Settings'
import Usage from './Usage'

const tabs = [
  { key: 'machines', text: '机器', icon: 'laptop' },
  { key: 'models', text: '模型', icon: 'cpu' },
  { key: 'personas', text: '岗位', icon: 'users' },
  { key: 'schedules', text: '定时任务', icon: 'calendar' },
  { key: 'memories', text: '记忆', icon: 'brain' },
  { key: 'usage', text: '用量', icon: 'chart' },
  { key: 'settings', text: '通知与默认', icon: 'bell' },
  { key: 'appearance', text: '外观', icon: 'moon' },
  { key: 'account', text: '账号', icon: 'key' },
]
const statusText = { connecting: '连接中', online: '已连接', offline: '离线，正在重连' }

const themes: { key: ThemePref; text: string; icon: string }[] = [
  { key: 'system', text: '跟随系统', icon: 'laptop' },
  { key: 'light', text: '浅色', icon: 'sun' },
  { key: 'dark', text: '深色', icon: 'moon' },
]

function Appearance() {
  const theme = useTheme()
  return (
    <div className="grid grid-cols-3 gap-3 max-w-[520px]" role="radiogroup" aria-label="主题">
      {themes.map((t) => (
        <button
          key={t.key}
          role="radio"
          aria-checked={theme === t.key}
          className={`grid gap-2 p-2 rounded-[14px] border-0 text-[13px] font-medium text-left transition-shadow ${
            theme === t.key ? 'bg-panel shadow-[0_0_0_2px_var(--accent)]' : 'bg-panel shadow-soft hover:shadow-[0_0_0_1px_var(--line-strong)]'
          }`}
          onClick={() => setTheme(t.key)}
        >
          {/* A tiny window in that theme. */}
          <span className="relative h-[72px] rounded-[10px] overflow-hidden grid grid-cols-[22%_1fr]" aria-hidden="true">
            {(t.key === 'system' ? ['light', 'dark'] : [t.key]).map((m, i, all) => (
              <span
                key={m}
                className="absolute inset-0 grid grid-cols-[22%_1fr]"
                style={all.length === 2 ? { clipPath: i === 0 ? 'polygon(0 0,100% 0,0 100%)' : 'polygon(100% 0,100% 100%,0 100%)' } : undefined}
              >
                <span style={{ background: m === 'dark' ? '#141414' : '#f3f3f3' }}></span>
                <span className="grid content-start gap-1.5 p-2" style={{ background: m === 'dark' ? '#181818' : '#fcfcfc' }}>
                  <span className="h-2 w-3/5 rounded-full" style={{ background: m === 'dark' ? '#79bff0' : '#2f8ad4' }}></span>
                  <span className="h-2 w-4/5 rounded-full" style={{ background: m === 'dark' ? '#2a2a2a' : '#e8e8e8' }}></span>
                  <span className="h-2 w-2/5 rounded-full" style={{ background: m === 'dark' ? '#2a2a2a' : '#e8e8e8' }}></span>
                </span>
              </span>
            ))}
          </span>
          <span className="flex items-center gap-1.5 px-0.5">
            <Icon name={t.icon} size={14} />{t.text}
            {theme === t.key && <Icon name="check" size={14} stroke={2.4} className="ml-auto text-accent" />}
          </span>
        </button>
      ))}
    </div>
  )
}

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

  const current = tabs.find((t) => t.key === uiState.settings) ?? tabs[0]

  return (
    <>
      {/* Clicking outside does not close settings: only the close button and Escape do. */}
      <div className="backdrop z-[28]" role="presentation"></div>
      <div
        className="fixed z-[29] top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[min(980px,calc(100vw-48px))] h-[min(700px,calc(100vh-64px))] bg-page rounded-[16px] shadow-pop overflow-hidden grid grid-cols-[208px_1fr] settings-page rise max-[760px]:inset-0 max-[760px]:translate-x-0 max-[760px]:translate-y-0 max-[760px]:w-full max-[760px]:h-full max-[760px]:rounded-none max-[760px]:grid-cols-1 max-[760px]:grid-rows-[auto_1fr]"
        role="dialog"
        aria-modal="true"
        aria-label="设置"
      >
        <nav
          className="bg-chrome px-2 pt-3 pb-3 grid content-start gap-px shadow-[inset_-1px_0_0_var(--line)] max-[760px]:flex max-[760px]:flex-nowrap max-[760px]:overflow-x-auto max-[760px]:gap-1 max-[760px]:px-2 max-[760px]:pt-[calc(8px+env(safe-area-inset-top))] max-[760px]:pb-2 max-[760px]:shadow-[inset_0_-1px_0_var(--line)] max-[760px]:items-center"
          aria-label="设置分类"
        >
          <h2 className="m-0 px-2.5 pt-1 pb-3 text-[12px] font-semibold text-muted uppercase tracking-wider max-[760px]:hidden">设置</h2>
          <button className="icon-btn hidden max-[760px]:inline-grid flex-none" aria-label="关闭设置" onClick={close}><Icon name="x" size={17} /></button>
          {tabs.map((t) => {
            const on = current.key === t.key
            return (
              <button
                key={t.key}
                className={`flex gap-2.5 items-center border-0 text-left px-2.5 h-8 rounded-[9px] text-[13px] max-[760px]:flex-none ${
                  on ? 'bg-hover text-ink font-semibold' : 'bg-transparent text-muted hover:bg-hover hover:text-ink'
                }`}
                aria-current={on ? 'page' : undefined}
                onClick={() => {
                  ui.state.settings = t.key
                  ui.emit()
                }}
              >
                <Icon name={t.icon} size={16} className={on ? 'text-accent' : ''} />
                <span className="whitespace-nowrap">{t.text}</span>
              </button>
            )
          })}
        </nav>
        <div className="min-h-0 grid grid-rows-[auto_1fr]">
          <header className="flex items-center gap-2.5 h-[52px] px-6 shadow-[inset_0_-1px_0_var(--line)] max-[760px]:px-4">
            <Icon name={current.icon} size={18} className="text-accent" />
            <h2 className="m-0 text-[15px] font-semibold flex-1">{current.text}</h2>
            <button className="icon-btn max-[760px]:hidden" aria-label="关闭设置" data-tip="关闭（Esc）" data-tip-pos="bottom" onClick={close}><Icon name="x" size={17} /></button>
          </header>
          <div className="overflow-y-auto px-6 pt-5 pb-10 max-[760px]:px-4 max-[760px]:pt-4 max-[760px]:pb-[calc(24px+env(safe-area-inset-bottom))]">
            <div className="embed max-w-[760px]">
              {current.key === 'machines' ? (
                <Machines onClose={close} />
              ) : current.key === 'models' ? (
                <Models />
              ) : current.key === 'personas' ? (
                <Personas onClose={close} />
              ) : current.key === 'schedules' ? (
                <Schedules onClose={close} />
              ) : current.key === 'memories' ? (
                <Memories onClose={close} />
              ) : current.key === 'usage' ? (
                <Usage onClose={close} />
              ) : current.key === 'settings' ? (
                <Settings onClose={close} />
              ) : current.key === 'appearance' ? (
                <Appearance />
              ) : (
                <div className="card p-4 grid gap-4 justify-items-start">
                  <div className="flex items-center gap-3">
                    <img src="/icon.svg" alt="" width={40} height={40} className="rounded-[12px]" />
                    <div className="grid leading-tight">
                      <strong className="text-[15px]">sous</strong>
                      <span className="flex gap-1.5 items-center text-[12.5px] text-muted mt-1">
                        <span className={`w-2 h-2 rounded-full ${uiState.status === 'online' ? 'bg-ok' : uiState.status === 'offline' ? 'bg-danger' : 'bg-faint'}`}></span>
                        {statusText[uiState.status]}
                      </span>
                    </div>
                  </div>
                  <button className="btn ghost hover:!text-danger" onClick={onLogout}><Icon name="logout" size={15} />退出登录</button>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </>
  )
}
