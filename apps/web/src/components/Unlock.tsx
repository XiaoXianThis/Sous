import { useState } from 'react'
import { Cancelled, passkeysAvailable } from '../trust/webauthn'
import { relayUrl, setRelayUrl, setup, unlock } from '../trust/vault'
import Icon from './Icon'

/**
 * The only door: Face ID, a fingerprint or the Windows PIN unlocks the
 * identity on this device (decision 0030). First setup claims the relay
 * with the token `sous-relay init` printed and creates the passkey.
 */
export default function Unlock() {
  const [mode, setMode] = useState<'unlock' | 'setup'>(relayUrl() ? 'unlock' : 'setup')
  const [relay, setRelay] = useState(relayUrl())
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function run(fn: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await fn()
    } catch (e) {
      setError(e instanceof Cancelled ? '操作已取消或超时' : (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="min-h-full grid place-items-center p-4 bg-[radial-gradient(1200px_600px_at_50%_-10%,color-mix(in_srgb,var(--accent)_16%,transparent),transparent)]">
      <div className="w-[min(380px,100%)] bg-panel shadow-pop rounded-[28px] px-7 pt-10 pb-8 grid justify-items-center gap-3 text-center rise">
        <img src="/icon.svg" alt="" width="72" height="72" className="rounded-[22px] shadow-card" />
        <h1 className="mt-2 mb-0 text-[28px] font-bold tracking-tight">sous</h1>

        {!passkeysAvailable() ? (
          <p className="text-muted text-sm">这个浏览器不支持 Passkey，或者页面不是通过 HTTPS 打开的。</p>
        ) : mode === 'unlock' ? (
          <>
            <p className="mt-0 mb-3 text-muted text-[13px]">已锁定。只有你本人能解锁。</p>
            <button className="btn w-full" disabled={busy} onClick={() => void run(unlock)}>
              <Icon name="fingerprint" size={18} />
              {busy ? '正在解锁…' : '用 Face ID / 指纹 / PIN 解锁'}
            </button>
            <p className="m-0 text-faint text-[12px] [overflow-wrap:anywhere]">中继：{relayUrl()}</p>
            <button className="link text-[13px]" onClick={() => setMode('setup')}>换一个中继或首次设置</button>
          </>
        ) : (
          <form
            className="grid gap-3 w-full text-left"
            onSubmit={(e) => {
              e.preventDefault()
              void run(() => setup(relay, token))
            }}
          >
            <p className="m-0 text-[13px] text-muted text-center">
              首次设置：在服务器上运行 <code className="bg-sunken px-1 rounded">sous-relay init</code> 得到认领码。
              这台设备会创建一个 Passkey，它就是你唯一的钥匙，服务器上不保存任何能解开它的东西。
            </p>
            <label className="grid gap-1 text-[13px]">
              中继地址
              <input className="border border-line bg-page rounded-[10px] px-3 py-2 text-[16px]" placeholder="https://relay.example.com" value={relay}
                onChange={(e) => setRelay(e.currentTarget.value)} required autoComplete="url" />
            </label>
            <label className="grid gap-1 text-[13px]">
              认领码
              <input className="border border-line bg-page rounded-[10px] px-3 py-2 text-[16px] font-mono" value={token}
                onChange={(e) => setToken(e.currentTarget.value)} autoComplete="off" />
            </label>
            <button className="btn w-full" disabled={busy || !relay}>
              {busy ? '正在设置…' : '创建 Passkey 并完成设置'}
            </button>
            <button type="button" className="link text-[13px]" disabled={busy || !relay}
              onClick={() => { setRelayUrl(relay); setMode('unlock') }}>
              这个中继已经设置过：去解锁
            </button>
          </form>
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
