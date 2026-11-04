import { useCallback, useEffect, useRef, useState } from 'react'
import Login from './components/Login'
import Shell from './components/Shell'
import { api } from './lib/api'
import { startSession, stopSession } from './lib/app'
import type { Me } from './lib/types'

export default function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [error, setError] = useState('')

  const refreshRef = useRef<() => Promise<void>>(async () => {})

  const loggedOut = useCallback(() => {
    stopSession()
    void refreshRef.current()
  }, [])

  const refresh = useCallback(async () => {
    try {
      setError('')
      const m = await api.me()
      setMe(m)
      if (m.logged_in) await startSession(loggedOut)
    } catch (e) {
      setError((e as Error).message)
    }
  }, [loggedOut])
  refreshRef.current = refresh

  const logout = useCallback(async () => {
    await api.logout().catch(() => {})
    loggedOut()
  }, [loggedOut])

  useEffect(() => {
    void refresh()
  }, [refresh])

  if (error) {
    return (
      <div className="h-full grid place-content-center justify-items-center gap-3 p-4 text-center">
        <p>连接 Hub 失败：{error}</p>
        <button className="btn" onClick={() => void refresh()}>
          重试
        </button>
      </div>
    )
  }
  if (!me) {
    return (
      <div className="h-full grid place-content-center justify-items-center gap-3 p-4 text-center text-muted">加载中…</div>
    )
  }
  if (!me.logged_in) {
    return <Login me={me} onLoggedIn={refresh} />
  }
  return <Shell onLogout={logout} />
}
