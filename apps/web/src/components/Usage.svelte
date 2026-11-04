<script lang="ts">
  import { api } from '../lib/api'
  import { byRole, dailyTotals, roleText, tokens } from '../lib/schedules'
  import type { Usage } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let days = $state(7)
  let data = $state<Usage | null>(null)
  let error = $state('')
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  $effect(() => {
    const d = days
    api.usage(d, tz).then((u) => { data = u; error = '' }).catch((e) => (error = (e as Error).message))
  })

  const roles = $derived(data ? byRole(data.usage) : [])
  const daily = $derived(data ? dailyTotals(data.daily, data.since, data.days, tz) : [])
  const peak = $derived(Math.max(1, ...daily.map((d) => d.input + d.output)))
  const total = $derived(roles.reduce((n, r) => ({ calls: n.calls + r.calls, input: n.input + r.input, output: n.output + r.output, cached: n.cached + r.cached }), { calls: 0, input: 0, output: 0, cached: 0 }))
</script>

<Sheet title="用量" {onClose}>
  <div class="row">
    {#each [1, 7, 30] as d}
      <button class="chip" class:on={days === d} onclick={() => (days = d)}>{d === 1 ? '今天' : `最近 ${d} 天`}</button>
    {/each}
  </div>
  {#if error}<p class="err" role="alert">{error}</p>{/if}
  {#if data}
    <section class="totals">
      <div><strong>{tokens(total.input + total.output)}</strong><span>token</span></div>
      <div><strong>{tokens(total.input)}</strong><span>输入（缓存命中 {tokens(total.cached)}）</span></div>
      <div><strong>{tokens(total.output)}</strong><span>输出</span></div>
      <div><strong>{total.calls}</strong><span>次模型调用</span></div>
    </section>

    {#if daily.length > 1}
      <section>
        <h3>每天</h3>
        <ul class="bars">
          {#each daily as d (d.day)}
            <li>
              <span class="day">{d.day.slice(5)}</span>
              <span class="bar"><span style="width: {((d.input + d.output) / peak) * 100}%"></span></span>
              <span class="num">{tokens(d.input + d.output)}</span>
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    <section>
      <h3>按角色和模型</h3>
      {#if data.usage.length === 0}
        <p class="muted">这段时间没有模型调用。</p>
      {:else}
        <table>
          <thead><tr><th>角色</th><th>模型</th><th>调用</th><th>输入</th><th>输出</th></tr></thead>
          <tbody>
            {#each data.usage as u}
              <tr>
                <td>{roleText[u.role] ?? u.role}</td><td><code>{u.provider}/{u.model}</code></td>
                <td>{u.calls}</td><td>{tokens(u.input_tokens)}</td><td>{tokens(u.output_tokens)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
      <p class="muted">这里只统计 sous 自己的模型调用（主管、验收、审批、巡检）。编程 agent 的用量在各自的账户里。</p>
    </section>
  {/if}
</Sheet>

<style>
  .row { display: flex; gap: 8px; flex-wrap: wrap; }
  .chip { border: 1px solid var(--line); background: transparent; color: var(--muted); border-radius: 999px; padding: 3px 12px; font-size: 13px; }
  .chip.on { border-color: var(--accent); color: var(--text); font-weight: 600; }
  .totals { display: grid; grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); gap: 8px; }
  .totals div { border: 1px solid var(--line); border-radius: 12px; padding: 10px 12px; display: grid; gap: 2px; }
  .totals strong { font-size: 20px; }
  .totals span { color: var(--muted); font-size: 12px; }
  section { display: grid; gap: 6px; }
  h3 { margin: 0; font-size: 14px; }
  .bars { list-style: none; margin: 0; padding: 0; display: grid; gap: 3px; font-size: 13px; }
  .bars li { display: grid; grid-template-columns: 44px 1fr 70px; gap: 8px; align-items: center; }
  .day { color: var(--muted); }
  .bar { height: 10px; background: var(--sunken); border-radius: 5px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); border-radius: 5px; }
  .num { text-align: right; }
  table { border-collapse: collapse; font-size: 13px; width: 100%; }
  th { text-align: left; color: var(--muted); font-weight: 500; }
  td, th { padding: 3px 10px 3px 0; }
  code { font: 12px ui-monospace, Menlo, Consolas, monospace; }
  .muted { color: var(--muted); font-size: 13px; margin: 0; }
  .err { color: var(--danger); font-size: 13px; margin: 0; }
</style>
