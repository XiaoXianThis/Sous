import { useCallback, useEffect, useRef, useState } from 'react'
import Shell from './Shell'
import DesktopUnlock from './DesktopUnlock'
import { desktopPage } from '../lib/desktop-workspace'
import { ExternalMCPConsent } from './ExternalMCP'
import { api, request } from '../lib/api'
import { startSession, stopSession, useUi } from '../lib/app'
import { workspacePasskey } from '../lib/workspace-passkey'
import { DirectWorkspaceContext, SupervisorBindingContext } from '../lib/direct-workspace'
import type { Me, SupervisorBinding, WorkspaceCapabilities } from '../lib/types'

export default function DirectWorkspace({ initial }: { initial: Me }) {
  const { settings } = useUi()
  const hosted = initial.hosted_workspace === true
  const [me, setMe] = useState(initial)
  const [capabilities, setCapabilities] = useState<WorkspaceCapabilities | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [setupToken, setSetupToken] = useState(() => !initial.has_passkey && location.pathname === '/setup' ? new URLSearchParams(location.hash.slice(1)).get('token') ?? '' : '')
  const lock = useCallback(() => {
    stopSession()
    setCapabilities(null)
    setMe((last) => ({ ...last, logged_in: false }))
  }, [])
  const updateBinding = useCallback((binding: SupervisorBinding) => {
    setCapabilities(last => last && (binding.generation ?? 0) >= (last.supervisor.generation ?? 0) ? { ...last, supervisor: binding } : last)
  }, [])
  const starting = useRef(false)
  const open = useCallback(async () => {
    if (starting.current) return
    starting.current = true
    setError('')
    try {
      const session = await api.me()
      setMe(session)
      if (!session.logged_in) return lock()
      const caps = await api.capabilities()
      await startSession(lock)
      setCapabilities(caps)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      starting.current = false
    }
  }, [lock])

  useEffect(() => {
    if (initial.has_passkey && location.pathname === '/setup') history.replaceState(null, '', '/')
  }, [initial.has_passkey])
  useEffect(() => { if (initial.logged_in) void open() }, [initial.logged_in, open])
  // Pick up model configuration and selections made from another tab.
  const ready = capabilities !== null
  useEffect(() => {
    if (!ready) return
    let active = true
    const refresh = () => {
      if (document.hidden) return
      void api.capabilities().then(caps => { if (active) updateBinding(caps.supervisor) }).catch(() => {})
    }
    refresh()
    document.addEventListener('visibilitychange', refresh)
    return () => { active = false; document.removeEventListener('visibilitychange', refresh) }
  }, [ready, settings, updateBinding])

  async function unlock() {
    setBusy(true)
    setError('')
    try {
      await workspacePasskey(setupToken || undefined)
      if (setupToken) {
        history.replaceState(null, '', '/')
        setSetupToken('')
      }
      await open()
    } catch (e) {
      setError(e instanceof DOMException && e.name === 'NotAllowedError' ? '操作已取消或超时。可以重试。' : (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const [lockError, setLockError] = useState('')
  async function logout() {
    setLockError('')
    try {
      await request('POST', '/api/v1/auth/logout')
      lock()
    } catch {
      setLockError('无法锁定：工作区没有确认退出。请检查运行状态后再试。')
    }
  }

  if (capabilities?.external_mcp && hosted && location.pathname === '/oauth/authorize') {
    return <ExternalMCPConsent authorization={location.search.slice(1)} />
  }

  if (capabilities) return <DirectWorkspaceContext.Provider value={capabilities}>
    {lockError && <p className="bg-sunken m-0 px-4 py-2 text-sm text-danger" role="alert">{lockError}</p>}
    <SupervisorBindingContext.Provider value={updateBinding}>
      <Shell onLogout={() => void logout()} />
    </SupervisorBindingContext.Provider>
  </DirectWorkspaceContext.Provider>

  if (initial.desktop_auth && desktopPage() && !me.logged_in) return <DesktopUnlock onUnlocked={open} error={error} />

  return (
    <main className="h-full grid place-content-center justify-items-center gap-3 p-6 text-center">
      <img src="/icon.svg" alt="" width={54} height={54} className="rounded-[15px] mb-2" />
      <h1 className="m-0 text-2xl font-semibold">Sous {hosted ? '云工作区' : '本机工作区'}</h1>
      <p className="text-muted text-sm max-w-[360px] m-0">{hosted ? '数据保存在此云工作区。个人电脑关机时仍可登录管理。' : '这是此设备上的独立工作区，不连接中继或云端 Hub。'}</p>
      {me.logged_in ? (
        error ? <button className="btn" onClick={() => void open()}>重试连接</button> : <p className="text-muted" role="status">正在读取工作区…</p>
      ) : !window.isSecureContext || !window.PublicKeyCredential ? (
        <p className="text-muted text-sm max-w-[360px]">当前浏览器环境不支持 Passkey。请在系统浏览器打开启动时显示的地址。</p>
      ) : setupToken || me.has_passkey ? (
        <button className="btn" disabled={busy} onClick={() => void unlock()}>
          {busy ? '等待身份验证…' : setupToken ? hosted ? '为云工作区创建 Passkey' : '为本机工作区创建 Passkey' : '用 Passkey 解锁'}
        </button>
      ) : (
        <p className="text-muted text-sm max-w-[360px]">{hosted ? '请使用工作区管理员提供的一次性注册链接。' : '请打开桌面 Sous 提供或启动时在终端显示的一次性注册链接。'} 注册后，这里就可以用 Passkey 解锁。</p>
      )}
      {error && <p className="text-danger m-0 text-sm max-w-[360px]" role="alert">{error}</p>}
    </main>
  )
}
