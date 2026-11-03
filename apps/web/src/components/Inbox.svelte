<script lang="ts">
  // What needs the user (design §13): approvals, blocked or failed tasks,
  // tasks waiting for a machine, and pull requests to review. Empty when
  // everything runs fine.
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { statusText } from '../lib/tasks'
  import type { Inbox, Task } from '../lib/types'
  import ApprovalCard from './ApprovalCard.svelte'

  let { onOpenTask, focus = false }: { onOpenTask: (id: string) => void; focus?: boolean } = $props()

  let inbox = $state<Inbox>({ approvals: [], tasks: [] })
  let titles = $state<Record<string, string>>({})
  let busy = $state('')
  let error = $state('')
  let el: HTMLElement | undefined = $state()

  async function load() {
    try {
      const [i, all] = await Promise.all([api.inbox(), api.tasks()])
      inbox = i
      titles = Object.fromEntries(all.tasks.map((t) => [t.id, t.title]))
      error = ''
    } catch (e) {
      error = (e as Error).message
    }
  }

  let timer: ReturnType<typeof setTimeout> | undefined
  $effect(() => {
    void chat.inboxRev
    clearTimeout(timer)
    timer = setTimeout(load, 120)
    return () => clearTimeout(timer)
  })

  $effect(() => {
    if (focus && el) el.scrollIntoView({ block: 'start' })
  })

  async function act(key: string, fn: () => Promise<unknown>) {
    busy = key
    error = ''
    try {
      await fn()
      await load()
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = ''
    }
  }

  const count = $derived(inbox.approvals.length + inbox.tasks.length)

  function label(t: Task) {
    if (t.status === 'done') return '待你评审合并'
    if (t.status === 'queued') return '在排队'
    return statusText[t.status]
  }
</script>

{#if count > 0}
  <section class="inbox" bind:this={el} aria-label="需要你处理" data-testid="inbox">
    <h3>需要你处理 <span class="n">{count}</span></h3>
    {#each inbox.approvals as a (a.id)}
      <ApprovalCard approval={a} context={titles[a.task_id] ? `任务：${titles[a.task_id]}` : ''} />
    {/each}
    {#each inbox.tasks as t (t.id)}
      <div class="item {t.status}">
        <div class="line">
          <span class="tag">{label(t)}</span>
          <button class="title" onclick={() => onOpenTask(t.id)}>{t.title}</button>
        </div>
        {#if t.status_reason}<p class="reason">{t.status_reason}</p>{/if}
        <div class="actions">
          {#if t.pr_url}
            <a class="btn small" href={t.pr_url} target="_blank" rel="noreferrer">打开 PR</a>
          {/if}
          {#if t.status === 'blocked' || t.status === 'failed'}
            <button class="btn small" disabled={!!busy} onclick={() => act(t.id, () => api.dispatchTask(t.id))}>重试</button>
          {/if}
          {#if t.status === 'queued'}
            <button class="btn small ghost" disabled={!!busy} onclick={() => act(t.id, () => api.cancelTask(t.id))}>取消任务</button>
          {:else}
            <button class="link" disabled={!!busy} onclick={() => act(t.id, () => api.patchTask(t.id, { dismissed: true }))}>
              {t.status === 'done' ? '已处理' : '知道了'}
            </button>
          {/if}
        </div>
      </div>
    {/each}
    {#if error}<p class="err" role="alert">{error}</p>{/if}
  </section>
{/if}

<style>
  .inbox { display: grid; gap: 8px; border-bottom: 1px dashed var(--line); padding-bottom: 14px; scroll-margin-top: 12px; }
  h3 { margin: 0; font-size: 14px; color: var(--muted); font-weight: 600; }
  .n { display: inline-block; min-width: 20px; text-align: center; border-radius: 999px; background: var(--warm); color: var(--panel); font-size: 12px; padding: 0 6px; }
  .item { border: 1px solid var(--line); border-left: 3px solid var(--muted); border-radius: 12px; background: var(--panel); padding: 10px 12px; display: grid; gap: 6px; }
  .item.blocked, .item.failed { border-left-color: var(--danger); }
  .item.queued { border-left-color: var(--warm); }
  .item.done { border-left-color: #3a9d5d; }
  .line { display: flex; gap: 8px; align-items: baseline; min-width: 0; }
  .tag { font-size: 12px; font-weight: 600; color: var(--warm); white-space: nowrap; }
  .blocked .tag, .failed .tag { color: var(--danger); }
  .done .tag { color: #3a9d5d; }
  .title { border: 0; background: none; padding: 0; text-align: left; font-weight: 600; overflow-wrap: anywhere; }
  .title:hover { text-decoration: underline; }
  .reason { margin: 0; font-size: 13px; color: var(--muted); overflow-wrap: anywhere; }
  .actions { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
  .btn.small { padding: 5px 14px; font-size: 13px; text-decoration: none; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .err { margin: 0; color: var(--danger); font-size: 13px; }
</style>
