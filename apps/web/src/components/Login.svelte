<script lang="ts">
  import { api } from '../lib/api'
  import { loginWithPasskey, passkeySupported, registerPasskey } from '../lib/passkey'
  import type { Me } from '../lib/types'

  let { me, onLoggedIn }: { me: Me; onLoggedIn: () => void } = $props()

  const setupToken = new URLSearchParams(location.search).get('token') ?? ''
  const setup = location.pathname === '/setup' && setupToken !== ''
  let busy = $state(false)
  let error = $state('')

  async function run(fn: () => Promise<unknown>) {
    busy = true
    error = ''
    try {
      await fn()
      if (setup) history.replaceState(null, '', '/')
      onLoggedIn()
    } catch (e) {
      const err = e as Error
      error = err.name === 'NotAllowedError' ? '操作已取消或超时' : err.message
    } finally {
      busy = false
    }
  }
</script>

<main>
  <div class="card">
    <img src="/icon.svg" alt="" width="56" height="56" />
    <h1>sous</h1>
    <p class="tagline">你的副主厨</p>

    {#if !passkeySupported()}
      <p class="note">这个浏览器不支持 Passkey，或者页面不是通过 HTTPS 打开的。</p>
    {:else if setup}
      <p>为这台设备创建一个 Passkey，之后用它登录。</p>
      <button class="btn" disabled={busy} onclick={() => run(() => registerPasskey(setupToken))}>创建 Passkey</button>
    {:else if me.has_passkey}
      <button class="btn" disabled={busy} onclick={() => run(loginWithPasskey)}>用 Passkey 登录</button>
    {:else}
      <p class="note">还没有注册 Passkey。打开 Hub 启动日志里的注册链接，或在服务器上执行<br /><code>sous-hub admin passkey-reset</code></p>
    {/if}

    {#if me.dev_login}
      <button class="btn ghost" disabled={busy} onclick={() => run(api.devLogin)}>开发模式登录</button>
    {/if}

    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </div>
</main>

<style>
  main { min-height: 100%; display: grid; place-items: center; padding: 16px; }
  .card {
    width: min(360px, 100%); background: var(--panel); border: 1px solid var(--line); border-radius: 20px;
    padding: 32px 24px; display: grid; justify-items: center; gap: 12px; text-align: center;
  }
  h1 { margin: 4px 0 0; font-size: 28px; letter-spacing: .02em; }
  .tagline { margin: 0 0 8px; color: var(--muted); }
  .note { color: var(--muted); font-size: 14px; }
  code { background: var(--sunken); padding: 2px 6px; border-radius: 6px; font-size: 13px; }
  .btn { width: 100%; }
  .error { color: var(--danger); margin: 0; font-size: 14px; }
</style>
