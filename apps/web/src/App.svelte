<script lang="ts">
  import { onMount } from 'svelte'
  import { api } from './lib/api'
  import { startSession, stopSession } from './lib/app.svelte'
  import type { Me } from './lib/types'
  import Login from './components/Login.svelte'
  import Shell from './components/Shell.svelte'

  let me = $state<Me | null>(null)
  let error = $state('')

  async function refresh() {
    try {
      error = ''
      me = await api.me()
      if (me.logged_in) await startSession(loggedOut)
    } catch (e) {
      error = (e as Error).message
    }
  }

  function loggedOut() {
    stopSession()
    refresh()
  }

  async function logout() {
    await api.logout().catch(() => {})
    loggedOut()
  }

  onMount(refresh)
</script>

{#if error}
  <div class="center">
    <p>连接 Hub 失败：{error}</p>
    <button class="btn" onclick={refresh}>重试</button>
  </div>
{:else if !me}
  <div class="center muted">加载中…</div>
{:else if !me.logged_in}
  <Login {me} onLoggedIn={refresh} />
{:else}
  <Shell onLogout={logout} />
{/if}

<style>
  .center {
    height: 100%; display: grid; place-content: center; justify-items: center; gap: 12px; padding: 16px; text-align: center;
  }
  .muted { color: var(--muted); }
</style>
