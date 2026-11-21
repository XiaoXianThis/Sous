import { useCallback, useEffect, useRef, useState } from 'react'
import Shell from './components/Shell'
import { BrainOffline } from './components/Trust'
import Unlock from './components/Unlock'
import DirectWorkspace from './components/DirectWorkspace'
import { detectDirectWorkspace, expectsHostedWorkspace } from './lib/workspace-transport'
import type { Me } from './lib/types'
import { desktopPage } from './lib/desktop-workspace'
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
  const [mode, setMode] = useState<Me | 'relay' | 'hosted-offline' | 'desktop-offline' | null>(null)
  const open = useCallback(async () => {
    setMode(null)
    const me = await detectDirectWorkspace()
    setMode(me ?? (expectsHostedWorkspace() ? 'hosted-offline' : desktopPage() ? 'desktop-offline' : 'relay'))
  }, [])
  useEffect(() => { void open() }, [open])
  if (mode === 'relay') return <RelayWorkspace />
  if (mode === 'desktop-offline') return <main className="h-full grid place-content-center justify-items-center gap-3 p-6 text-center">
    <h1 className="m-0 text-xl font-semibold">暂时连不上本机工作区</h1>
    <p className="m-0 text-sm text-muted">可从 Sous 菜单重新打开工作台。不会切换到其他工作区。</p>
    <button className="btn" onClick={() => void open()}>重新连接</button>
  </main>
  if (mode === 'hosted-offline') return <main className="h-full grid place-content-center justify-items-center gap-3 p-6 text-center">
    <h1 className="m-0 text-xl font-semibold">暂时连不上云工作区</h1>
    <p className="m-0 text-sm text-muted">确认网络可用后重试。此页面不会切换到其他工作区。</p>
    <button className="btn" onClick={() => void open()}>重新连接</button>
  </main>
  if (mode) return <DirectWorkspace initial={mode} />
  return <div className="h-full grid place-content-center text-muted" role="status">正在打开工作区…</div>
}

function RelayWorkspace() {
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
