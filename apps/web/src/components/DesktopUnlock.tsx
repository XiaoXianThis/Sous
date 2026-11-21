import { useEffect, useState } from 'react'
import { desktopStatus, showDesktopDebugCode, unlockDesktop } from '../lib/desktop-workspace'

export default function DesktopUnlock({ onUnlocked, error }: { onUnlocked: () => Promise<void>; error?: string }) {
  const [mode, setMode] = useState<'native' | 'debug' | null>(null)
  const [statusError, setStatusError] = useState('')
  const [message, setMessage] = useState('')
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let live = true
    setStatusError('')
    void desktopStatus().then(info => { if (live) setMode(info.debug ? 'debug' : 'native') }).catch((e: Error) => {
      if (live) setStatusError(e.message)
    })
    return () => { live = false }
  }, [attempt])

  async function run(showCode: boolean) {
    if (busy || !mode) return
    setBusy(true)
    setMessage('')
    try {
      if (showCode) {
        setCode('')
        await showDesktopDebugCode()
      } else {
        await unlockDesktop(mode === 'debug' ? code : undefined)
        setCode('')
        await onUnlocked()
      }
    } catch (e) {
      setMessage(e instanceof Error ? e.message : String(e))
    } finally { setBusy(false) }
  }

  return <main className="h-full grid place-items-center px-6 py-8 bg-sunken text-center">
    <div className="w-full max-w-[370px] grid justify-items-center gap-4">
      <img src="/icon.svg" alt="" width={62} height={62} className="rounded-[17px] mb-1" />
      <div>
        <h1 className="m-0 text-3xl font-semibold tracking-tight">Sous</h1>
        <p className="m-0 mt-1 text-muted text-sm">本机工作台</p>
      </div>
      {mode === 'native' && <>
        <p className="text-muted text-sm m-0 leading-relaxed">用系统身份解锁聊天、项目和任务。数据保存在这台电脑上，不需要打开浏览器。</p>
        <button className="btn" disabled={busy} onClick={() => void run(false)}>{busy ? '等待系统验证…' : '验证并解锁'}</button>
      </>}
      {mode === 'debug' && <>
        <p className="m-0 rounded-full bg-surface px-3 py-1 text-xs font-semibold text-muted">调试模式 · 独立工作区</p>
        <p className="text-muted text-sm m-0 leading-relaxed">此模式不读取正式工作区。先显示一次性验证码，再输入它登录，不需要指纹。</p>
        <form className="grid w-full gap-2" onSubmit={e => { e.preventDefault(); if (code.length === 8) void run(false) }}>
          <label htmlFor="desktop-code" className="sr-only">8 位调试验证码</label>
          <input id="desktop-code" className="field w-full text-center tracking-[.25em]" placeholder="8 位验证码" inputMode="numeric" autoComplete="off" maxLength={8} disabled={busy} value={code} onChange={e => setCode(e.target.value.replace(/[^0-9]/g, ''))} />
          <button className="btn" type="submit" disabled={busy || code.length !== 8}>{busy ? '正在验证…' : '登录调试工作区'}</button>
          <button className="text-sm text-accent py-2" type="button" disabled={busy} onClick={() => void run(true)}>显示验证码</button>
        </form>
      </>}
      {!mode && (statusError ? <button className="btn" onClick={() => setAttempt(n => n + 1)}>重新连接桌面程序</button> : <p role="status" className="text-muted text-sm m-0">正在连接桌面程序…</p>)}
      {(message || statusError || error) && <p className="text-danger m-0 text-sm" role="alert">{message || statusError || error}</p>}
    </div>
  </main>
}
