<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import '@xterm/xterm/css/xterm.css'
  import { api } from '../lib/api'
  import { chat } from '../lib/app.svelte'
  import { extractLinks, jobTitle, mergeJobTail } from '../lib/jobs'

  let { nodeId, jobId }: { nodeId: string; jobId: string } = $props()

  const job = $derived(chat.jobs[jobId])
  let box: HTMLDivElement
  let term: Terminal | null = null
  let fit: FitAddon | null = null
  let written = 0
  let epoch = -1
  let cols = $state(80)
  let line = $state('')
  let error = $state('')
  let copied = $state('')

  const links = $derived(job ? extractLinks(job.data, cols) : { urls: [], codes: [] })

  function color(name: string, fallback: string) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
  }

  // Keystrokes are sent in small batches.
  let pending = ''
  let timer: ReturnType<typeof setTimeout> | null = null
  function send(data: string) {
    if (!job?.running) return
    pending += data
    if (timer) return
    timer = setTimeout(async () => {
      const d = pending
      pending = ''
      timer = null
      try {
        await api.jobInput(nodeId, jobId, d)
        error = ''
      } catch (e) {
        error = (e as Error).message
      }
    }, 30)
  }

  let resizeTimer: ReturnType<typeof setTimeout> | null = null
  function refit() {
    if (!term || !fit) return
    try {
      fit.fit()
    } catch {
      return
    }
    cols = term.cols
    if (resizeTimer) clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => {
      if (job?.running && term) api.jobResize(nodeId, jobId, term.cols, term.rows).catch(() => {})
    }, 200)
  }

  let observer: ResizeObserver | null = null
  onMount(() => {
    term = new Terminal({
      convertEol: false,
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, Menlo, Consolas, monospace',
      scrollback: 5000,
      theme: { background: color('--sunken', '#efebe3'), foreground: color('--text', '#22201c'), cursor: color('--accent', '#1f3b2d'),
        selectionBackground: color('--line', '#e2ddd2') },
    })
    fit = new FitAddon()
    term.loadAddon(fit)
    term.open(box)
    term.onData(send)
    refit()
    observer = new ResizeObserver(refit)
    observer.observe(box)
    api.jobTail(nodeId, jobId).then((t) => mergeJobTail(chat.jobs, nodeId, t)).catch((e) => (error = (e as Error).message))
  })

  onDestroy(() => {
    observer?.disconnect()
    term?.dispose()
    if (timer) clearTimeout(timer)
    if (resizeTimer) clearTimeout(resizeTimer)
  })

  // Write what arrived since the last time; start over when it was replaced.
  $effect(() => {
    const j = job
    if (!term || !j) return
    if (j.epoch !== epoch) {
      epoch = j.epoch
      term.reset()
      written = 0
    }
    if (j.data.length > written) {
      term.write(j.data.slice(written))
      written = j.data.length
    }
  })

  function submitLine() {
    if (!job?.running) return
    send(line + '\r')
    line = ''
  }

  async function copy(text: string) {
    await navigator.clipboard?.writeText(text)
    copied = text
    setTimeout(() => (copied = ''), 1500)
  }

  async function stop() {
    try {
      await api.jobCancel(nodeId, jobId)
    } catch (e) {
      error = (e as Error).message
    }
  }

  const keys: [string, string][] = [['回车', '\r'], ['↑', '\x1b[A'], ['↓', '\x1b[B'], ['空格', ' '], ['Esc', '\x1b'], ['Ctrl+C', '\x03']]
</script>

<div class="job">
  <div class="bar">
    <span class="title">{jobTitle[job?.kind ?? ''] ?? '作业'}</span>
    {#if job?.command}<code class="cmd" title={job.command}>{job.command}</code>{/if}
    <span class="state" class:bad={job && !job.running && job.exit_code !== 0} class:good={job && !job.running && job.exit_code === 0}>
      {#if !job || job.running}进行中…{:else if job.error === '已停止'}已停止{:else if job.exit_code === 0}已完成{:else}失败（退出码 {job.exit_code}）{/if}
    </span>
    {#if job?.running}<button class="link danger" onclick={stop}>停止</button>{/if}
  </div>

  {#if links.urls.length || links.codes.length}
    <div class="links">
      {#each links.urls as u (u)}
        <div class="link-row">
          <a href={u} target="_blank" rel="noreferrer noopener" title={u}>打开链接</a>
          <span class="url">{u}</span>
          <button class="link" onclick={() => copy(u)}>{copied === u ? '已复制' : '复制'}</button>
        </div>
      {/each}
      {#each links.codes as c (c)}
        <div class="link-row">
          <span>验证码</span><strong class="code">{c}</strong>
          <button class="link" onclick={() => copy(c)}>{copied === c ? '已复制' : '复制'}</button>
        </div>
      {/each}
    </div>
  {/if}

  <div class="term" bind:this={box}></div>

  {#if job?.running}
    <div class="input">
      <input
        bind:value={line}
        placeholder="粘贴验证码或输入后按回车发送"
        aria-label="发送到终端"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        onkeydown={(e) => e.key === 'Enter' && !e.isComposing && (e.preventDefault(), submitLine())}
      />
      <button class="btn small" onclick={submitLine}>发送</button>
    </div>
    <div class="keys">
      {#each keys as [label, seq]}<button class="key" onclick={() => send(seq)}>{label}</button>{/each}
    </div>
  {:else if job?.error && job.error !== '已停止'}
    <p class="sub">{job.error}</p>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</div>

<style>
  .job { display: grid; gap: 8px; min-width: 0; }
  .bar { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; font-size: 13px; }
  .title { font-weight: 600; }
  .cmd { font: 12px ui-monospace, Menlo, Consolas, monospace; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; flex: 1; min-width: 0; }
  .state { color: var(--muted); }
  .state.good { color: #3a9d5d; }
  .state.bad { color: var(--danger); }
  .links { border: 1px solid var(--accent); border-radius: 10px; padding: 8px 10px; display: grid; gap: 6px; font-size: 13px; }
  .link-row { display: flex; gap: 10px; align-items: baseline; min-width: 0; }
  .link-row a { color: var(--accent); font-weight: 600; white-space: nowrap; }
  .url { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; min-width: 0; font: 12px ui-monospace, Menlo, Consolas, monospace; }
  .code { font: 600 15px ui-monospace, Menlo, Consolas, monospace; letter-spacing: 1px; }
  .term { height: 300px; background: var(--sunken); border-radius: 10px; padding: 6px; overflow: hidden; }
  .input { display: flex; gap: 8px; }
  .input input { flex: 1; min-width: 0; border: 1px solid var(--line); background: var(--bg); color: var(--text); border-radius: 8px; padding: 6px 10px; font: 13px ui-monospace, Menlo, Consolas, monospace; }
  .btn.small { padding: 6px 14px; font-size: 13px; }
  .keys { display: flex; gap: 6px; flex-wrap: wrap; }
  .key { border: 1px solid var(--line); background: var(--panel); color: var(--text); border-radius: 6px; padding: 3px 9px; font-size: 12px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .link.danger:hover { color: var(--danger); }
  .sub { color: var(--muted); font-size: 13px; margin: 0; }
  .error { color: var(--danger); font-size: 13px; margin: 0; }
  @media (max-width: 760px) { .term { height: 240px; } }
</style>
