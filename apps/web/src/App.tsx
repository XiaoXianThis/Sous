import { useCallback, useEffect, useRef, useState } from 'react'
import Shell from './components/Shell'
import { BrainOffline } from './components/Trust'
import Unlock from './components/Unlock'
import { api } from './lib/api'
import { startSession, stopSession } from './lib/app'
import { BrainOffline as Offline } from './trust/rpc'
import { isUnlocked, lock, useVault } from './trust/vault'

/**
 * Locked: the unlock screen, nothing else. Unlocked: talk to the brain
 * through the relay; while no brain machine is online, the machine
 * controls still work (decision 0031).
 */
export default function App() {
  useVault()
  const unlocked = isUnlocked()
  const [ready, setReady] = useState(false)
  const [offline, setOffline] = useState('')
  const [error, setError] = useState('')
  const connecting = useRef(false)

  const connect = useCallback(async () => {
    if (connecting.current) return
    connecting.current = true
    try {
      setError('')
      await api.me()
      setOffline('')
      await startSession(lock)
      setReady(true)
    } catch (e) {
      if (!isUnlocked()) return
      if (e instanceof Offline) setOffline((e as Error).message)
      else setError((e as Error).message)
    } finally {
      connecting.current = false
    }
  }, [])

  useEffect(() => {
    if (!unlocked) {
      stopSession()
      setReady(false)
      return
    }
    if (ready) return
    void connect()
    const t = setInterval(() => !ready && void connect(), 8000)
    return () => clearInterval(t)
  }, [unlocked, ready, connect])

  if (!unlocked) return <Unlock />
  if (ready) return <Shell onLogout={lock} />
  if (offline) return <BrainOffline message={offline} />
  if (error) {
    return (
      <div className="h-full grid place-content-center justify-items-center gap-3 p-4 text-center">
        <p>连接大脑失败：{error}</p>
        <button className="btn" onClick={() => void connect()}>重试</button>
      </div>
    )
  }
  return <div className="h-full grid place-content-center justify-items-center gap-3 p-4 text-center text-muted">正在连接大脑…</div>
}
