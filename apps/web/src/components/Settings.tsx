import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { ui } from '../lib/app'
import { disablePush, enablePush, pushState, type PushState } from '../lib/push'
import { agentChoices, modeText, pushKindText } from '../lib/tasks'
import type { Node, PermissionMode, Settings } from '../lib/types'
import Sheet from './Sheet'

const modeHelp: Record<string, string> = {
  default: '读文件、在 worktree 里改代码、跑测试直接放行；其他操作（执行命令、安装、push 等）都问你。',
  auto: '未设置时使用此模式；常规操作自动处理，其余先交审批 agent，只有关键风险或低把握时再问你。',
  open: '全部放行，只记录日志。底线规则仍然生效。',
}
const roleText: Record<string, string> = { supervisor: '主管', reviewer: '验收', approver: '审批', inspector: '巡检' }

export default function SettingsPanel({ onClose }: { onClose: () => void }) {
  const [s, setS] = useState<Settings | null>(null)
  const [nodes, setNodes] = useState<Node[]>([])
  const [push, setPush] = useState<PushState>('off')
  const [error, setError] = useState('')
  const [info, setInfo] = useState('')
  const [busy, setBusy] = useState(false)
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  useEffect(() => {
    void (async () => {
      try {
        const [st, n] = await Promise.all([api.settings(), api.nodes()])
        setS(st)
        setNodes(n.nodes)
        setPush(await pushState())
      } catch (e) {
        setError((e as Error).message)
      }
    })()
  }, [])

  async function patch(p: Partial<Settings>) {
    setError('')
    try {
      setS(await api.patchSettings(p))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function togglePush() {
    setBusy(true)
    setError('')
    setInfo('')
    try {
      const next = push === 'on' ? await disablePush() : await enablePush()
      setPush(next)
      setS(await api.settings())
      if (next === 'denied') setError('浏览器拒绝了通知权限，请在系统或浏览器设置里打开。')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function test() {
    setBusy(true)
    setInfo('')
    try {
      const r = await api.testPush()
      setInfo(r.devices === 0 ? '还没有设备开启推送。' : `已发送到 ${r.sent}/${r.devices} 台设备。${r.errors.length ? r.errors.join('；') : ''}`)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const agents = agentChoices(nodes)

  return (
    <Sheet title="设置" onClose={onClose}>
      {s && (
        <>
          <section className="grid gap-2">
            <h3 className="m-0 text-[14px]">权限模式（全局默认）</h3>
            <div className="grid gap-1.5" role="radiogroup" aria-label="权限模式">
              {Object.entries(modeText).map(([k, v]) => (
                <label
                  key={k}
                  className={`grid grid-cols-[auto_auto_1fr] gap-x-2 gap-y-1 items-baseline border border-line rounded-[12px] px-2.5 py-2 cursor-pointer ${
                    (s.default_mode || 'auto') === k ? 'border-accent bg-[color-mix(in_srgb,var(--accent)_6%,var(--panel))]' : ''
                  }`}
                >
                  <input type="radio" name="mode" value={k} checked={(s.default_mode || 'auto') === k} onChange={() => void patch({ default_mode: k as PermissionMode })} />
                  <strong>{v}</strong><span className="text-muted text-[13px]">{modeHelp[k]}</span>
                </label>
              ))}
            </div>
            <p className="text-muted text-[13px] m-0">项目和单个任务可以覆盖这里的设置。派发时会固定下当时的模式。</p>
          </section>

          <section className="grid gap-2">
            <h3 className="m-0 text-[14px]">默认 agent</h3>
            <select
              className="border border-line bg-page rounded-[10px] px-2 py-[5px] text-ink justify-self-start"
              value={s.default_agent}
              onChange={(e) => void patch({ default_agent: e.currentTarget.value })}
              aria-label="默认 agent"
            >
              <option value="">自动（第一个可用的）</option>
              {agents.map((a) => <option key={a} value={a}>{a}</option>)}
            </select>
          </section>

          <section className="grid gap-2">
            <h3 className="m-0 text-[14px]">推送通知</h3>
            {push === 'unsupported' ? (
              <p className="text-muted text-[13px] m-0">这个浏览器不支持推送。iPhone 和 iPad 上要先把 sous“添加到主屏幕”，再从主屏幕打开。</p>
            ) : (
              <div className="flex gap-2.5 items-center flex-wrap">
                <button className={`btn small px-4 ${push === 'on' ? 'ghost' : ''}`} disabled={busy} onClick={() => void togglePush()}>
                  {push === 'on' ? '在这台设备上关闭' : '在这台设备上开启'}
                </button>
                {s.push_devices > 0 && <button className="link" disabled={busy} onClick={() => void test()}>发一条测试通知</button>}
                <span className="text-muted text-[13px]">已开启的设备：{s.push_devices}</span>
              </div>
            )}
            <div className="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-x-3 gap-y-1">
              {Object.entries(pushKindText).map(([k, v]) => (
                <label key={k} className="flex gap-1.5 items-center text-[14px]">
                  <input type="checkbox" checked={s.notify[k] ?? true} onChange={(e) => void patch({ notify: { [k]: e.currentTarget.checked } })} /> {v}
                </label>
              ))}
            </div>
            <div className="flex gap-2.5 items-center flex-wrap">
              <label className="flex gap-1.5 items-center text-[14px]">
                <input
                  type="checkbox"
                  checked={s.quiet.enabled}
                  onChange={(e) => void patch({ quiet: { ...s.quiet, enabled: e.currentTarget.checked, tz: s.quiet.tz || tz } })}
                />
                免打扰
              </label>
              <input
                type="time"
                className="border border-line bg-page rounded-[10px] px-2 py-[5px] text-ink justify-self-start"
                value={s.quiet.start}
                aria-label="开始"
                onChange={(e) => void patch({ quiet: { ...s.quiet, start: e.currentTarget.value, tz: s.quiet.tz || tz } })}
              />
              <span>到</span>
              <input
                type="time"
                className="border border-line bg-page rounded-[10px] px-2 py-[5px] text-ink justify-self-start"
                value={s.quiet.end}
                aria-label="结束"
                onChange={(e) => void patch({ quiet: { ...s.quiet, end: e.currentTarget.value, tz: s.quiet.tz || tz } })}
              />
              <span className="text-muted text-[13px]">期间只推送审批</span>
            </div>
            <div className="flex gap-2.5 items-center flex-wrap">
              <label className="flex gap-1.5 items-center text-[14px]">
                <input
                  type="checkbox"
                  checked={s.minutes_auto_post}
                  onChange={(e) => void patch({ minutes_auto_post: e.currentTarget.checked })}
                />
                纪要直接发出
              </label>
              <span className="text-muted text-[13px]">散会后不再让你确认，纪要整理好就发回原来的对话</span>
            </div>
            {info && <p className="text-muted text-[13px] m-0">{info}</p>}
          </section>

          <section className="grid gap-2">
            <h3 className="m-0 text-[14px]">模型</h3>
            <table className="border-collapse text-[14px]">
              <tbody>
                {Object.keys(roleText).filter((r) => s.roles[r]).map((role) => (
                  <tr key={role}><td className="py-0.5 pr-4">{roleText[role]}</td><td className="py-0.5 pr-4"><code className="font-mono text-[13px]">{s.roles[role]}</code></td></tr>
                ))}
              </tbody>
            </table>
            <button className="link justify-self-start" onClick={() => { ui.state.settings = 'models'; ui.emit() }}>在“模型”里添加供应商、切换模型和思考强度</button>
          </section>
        </>
      )}
      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
    </Sheet>
  )
}
