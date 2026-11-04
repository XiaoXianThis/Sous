import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { byRole, dailyTotals, roleText, tokens } from '../lib/schedules'
import type { Usage } from '../lib/types'
import Sheet from './Sheet'

export default function UsagePanel({ onClose }: { onClose: () => void }) {
  const [days, setDays] = useState(7)
  const [data, setData] = useState<Usage | null>(null)
  const [error, setError] = useState('')
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  useEffect(() => {
    const d = days
    api.usage(d, tz).then((u) => { setData(u); setError('') }).catch((e) => setError((e as Error).message))
  }, [days])

  const roles = data ? byRole(data.usage) : []
  const daily = data ? dailyTotals(data.daily, data.since, data.days, tz) : []
  const peak = Math.max(1, ...daily.map((d) => d.input + d.output))
  const total = roles.reduce((n, r) => ({ calls: n.calls + r.calls, input: n.input + r.input, output: n.output + r.output, cached: n.cached + r.cached }), { calls: 0, input: 0, output: 0, cached: 0 })

  return (
    <Sheet title="用量" onClose={onClose}>
      <div className="flex gap-2 flex-wrap">
        {[1, 7, 30].map((d) => (
          <button
            key={d}
            className={`border bg-transparent text-muted rounded-full px-3 py-[3px] text-[13px] ${days === d ? 'border-accent text-ink font-semibold' : 'border-line'}`}
            onClick={() => setDays(d)}
          >
            {d === 1 ? '今天' : `最近 ${d} 天`}
          </button>
        ))}
      </div>
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
      {data && (
        <>
          <section className="grid grid-cols-[repeat(auto-fit,minmax(130px,1fr))] gap-2">
            <div className="border border-line rounded-[14px] px-3 py-2.5 grid gap-0.5"><strong className="text-[20px]">{tokens(total.input + total.output)}</strong><span className="text-muted text-[12px]">token</span></div>
            <div className="border border-line rounded-[14px] px-3 py-2.5 grid gap-0.5"><strong className="text-[20px]">{tokens(total.input)}</strong><span className="text-muted text-[12px]">输入（缓存命中 {tokens(total.cached)}）</span></div>
            <div className="border border-line rounded-[14px] px-3 py-2.5 grid gap-0.5"><strong className="text-[20px]">{tokens(total.output)}</strong><span className="text-muted text-[12px]">输出</span></div>
            <div className="border border-line rounded-[14px] px-3 py-2.5 grid gap-0.5"><strong className="text-[20px]">{total.calls}</strong><span className="text-muted text-[12px]">次模型调用</span></div>
          </section>

          {daily.length > 1 && (
            <section className="grid gap-1.5">
              <h3 className="m-0 text-[14px]">每天</h3>
              <ul className="list-none m-0 p-0 grid gap-[3px] text-[13px]">
                {daily.map((d) => (
                  <li key={d.day} className="grid grid-cols-[44px_1fr_70px] gap-2 items-center">
                    <span className="text-muted">{d.day.slice(5)}</span>
                    <span className="h-2.5 bg-sunken rounded-[5px] overflow-hidden">
                      <span className="block h-full bg-accent rounded-[5px]" style={{ width: `${((d.input + d.output) / peak) * 100}%` }}></span>
                    </span>
                    <span className="text-right">{tokens(d.input + d.output)}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}

          <section className="grid gap-1.5">
            <h3 className="m-0 text-[14px]">按角色和模型</h3>
            {data.usage.length === 0 ? (
              <p className="text-muted text-[13px] m-0">这段时间没有模型调用。</p>
            ) : (
              <table className="border-collapse text-[13px] w-full">
                <thead><tr><th className="text-left text-muted font-medium py-[3px] pr-2.5">角色</th><th className="text-left text-muted font-medium py-[3px] pr-2.5">模型</th><th className="text-left text-muted font-medium py-[3px] pr-2.5">调用</th><th className="text-left text-muted font-medium py-[3px] pr-2.5">输入</th><th className="text-left text-muted font-medium py-[3px] pr-2.5">输出</th></tr></thead>
                <tbody>
                  {data.usage.map((u, i) => (
                    <tr key={i}>
                      <td className="py-[3px] pr-2.5">{roleText[u.role] ?? u.role}</td><td className="py-[3px] pr-2.5"><code className="font-mono text-[12px]">{u.provider}/{u.model}</code></td>
                      <td className="py-[3px] pr-2.5">{u.calls}</td><td className="py-[3px] pr-2.5">{tokens(u.input_tokens)}</td><td className="py-[3px] pr-2.5">{tokens(u.output_tokens)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <p className="text-muted text-[13px] m-0">这里只统计 sous 自己的模型调用（主管、验收、审批、巡检）。编程 agent 的用量在各自的账户里。</p>
          </section>
        </>
      )}
    </Sheet>
  )
}
