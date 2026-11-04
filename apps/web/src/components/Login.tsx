import { useState } from 'react'
import { api } from '../lib/api'
import { loginWithPasskey, passkeySupported, registerPasskey } from '../lib/passkey'
import type { Me } from '../lib/types'

export default function Login({ me, onLoggedIn }: { me: Me; onLoggedIn: () => void }) {
  const setupToken = new URLSearchParams(location.search).get('token') ?? ''
  const setup = location.pathname === '/setup' && setupToken !== ''
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function run(fn: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await fn()
      if (setup) history.replaceState(null, '', '/')
      onLoggedIn()
    } catch (e) {
      const err = e as Error
      setError(err.name === 'NotAllowedError' ? '操作已取消或超时' : err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="min-h-full grid place-items-center p-4 bg-[radial-gradient(1200px_600px_at_50%_-10%,color-mix(in_srgb,var(--accent)_16%,transparent),transparent)]">
      <div className="w-[min(360px,100%)] bg-panel shadow-pop rounded-[28px] px-7 pt-10 pb-8 grid justify-items-center gap-3 text-center rise">
        <img src="/icon.svg" alt="" width="72" height="72" className="rounded-[22px] shadow-card" />
        <h1 className="mt-2 mb-0 text-[28px] font-bold tracking-tight">sous</h1>
        <p className="mt-0 mb-3 text-muted text-[13px]">你的副主厨</p>

        {!passkeySupported() ? (
          <p className="text-muted text-sm">这个浏览器不支持 Passkey，或者页面不是通过 HTTPS 打开的。</p>
        ) : setup ? (
          <>
            <p>为这台设备创建一个 Passkey，之后用它登录。</p>
            <button className="btn w-full" disabled={busy} onClick={() => void run(() => registerPasskey(setupToken))}>
              创建 Passkey
            </button>
          </>
        ) : me.has_passkey ? (
          <button className="btn w-full" disabled={busy} onClick={() => void run(loginWithPasskey)}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M15 3a6 6 0 1 0 0 12a6 6 0 0 0 0-12zM10.8 13.2L3 21M6 18l2 2M15 7.5h.01" /></svg>
            用 Passkey 登录
          </button>
        ) : (
          <p className="text-muted text-sm">
            还没有注册 Passkey。打开 Hub 启动日志里的注册链接，或在服务器上执行
            <br />
            <code className="bg-sunken px-1.5 py-0.5 rounded-md text-[13px]">sous-hub admin passkey-reset</code>
          </p>
        )}

        {me.dev_login && (
          <button className="btn ghost w-full" disabled={busy} onClick={() => void run(api.devLogin)}>
            开发模式登录
          </button>
        )}

        {error && (
          <p className="text-danger m-0 text-sm" role="alert">
            {error}
          </p>
        )}
      </div>
    </main>
  )
}
