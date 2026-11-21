import { useContext, useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { SupervisorBindingContext, useDirectWorkspace } from '../lib/direct-workspace'
import type { SupervisorChoice } from '../lib/types'

const choices: Record<SupervisorChoice, { label: string; detail: string }> = {
  none: { label: '不使用主管', detail: '只在工作区内保存对话、记忆、项目和任务，不向任何模型发送消息。' },
  legacy: { label: '兼容主管', detail: '保留旧版模型设置和对话能力。连接本机还是远程模型取决于模型配置。' },
  acp: { label: 'Sous ACP 主管', detail: '独立进程，通过受限工具读取记忆、查询项目和登记任务草稿。不提供宿主文件访问或命令执行工具。' },
}

export default function LocalSupervisor() {
  const binding = useDirectWorkspace()?.supervisor
  const update = useContext(SupervisorBindingContext)
  const [choice, setChoice] = useState<SupervisorChoice>(binding?.selection ?? 'none')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  useEffect(() => { if (binding?.selection) setChoice(binding.selection) }, [binding?.selection])
  if (!binding?.generation || !binding.choices || !binding.selection) return null
  const generation = binding.generation
  const reconnect = choice === 'acp' && binding.selection === 'acp' && !binding.notice
  const activeLabel = binding.module === 'acp' ? choices.acp.label : binding.module === 'legacy' ? choices.legacy.label : choices.none.label

  async function save() {
    setBusy(true)
    setDone(false)
    setError('')
    try {
      update(await api.selectSupervisor(choice, generation, reconnect))
      setDone(true)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'binding_stale') {
        try { update((await api.capabilities()).supervisor) } catch { /* retain the original error */ }
      }
      setError(e instanceof ApiError && e.code === 'binding_unavailable' && choice === 'acp'
        ? 'Sous ACP 未通过连接测试，现有绑定没有改变。请检查本机 supervisor.yaml 和模型服务，再试一次。'
        : e instanceof Error ? e.message : '无法切换主管。')
    } finally { setBusy(false) }
  }

  return <section className="grid gap-3 max-w-[560px] px-3 py-2" aria-label="本机主管绑定">
    <div>
      <h3 className="m-0 text-base font-semibold">本机主管</h3>
      <p className="m-0 mt-1 text-sm text-muted">当前：{activeLabel}{binding.state === 'unconfigured' ? ' · 尚未配置模型' : ''}。切换不会重启工作区。仍未回复的消息会交给新主管。</p>
    </div>
    {binding.notice && <p className="m-0 text-sm text-danger" role="status">{binding.notice}</p>}
    <fieldset disabled={busy} className="m-0 p-0 border-0 min-w-0 grid gap-1.5">
      <legend className="sr-only">选择本机主管</legend>
      {binding.choices.map(id => <label key={id} className={`grid grid-cols-[auto_1fr] gap-2 items-start rounded-xl px-3 py-2 cursor-pointer ${choice === id ? 'bg-sunken' : 'hover:bg-sunken/50'}`}>
        <input className="mt-1" type="radio" name="local-supervisor" checked={choice === id} onChange={() => { setChoice(id); setDone(false); setError('') }} />
        <span className="grid gap-0.5"><strong className="text-sm">{choices[id].label}</strong><span className="text-[13px] leading-relaxed text-muted">{choices[id].detail}</span></span>
      </label>)}
    </fieldset>
    {choice === 'acp' && <p className="m-0 text-[13px] text-muted leading-relaxed">配置来自 Sous 数据目录的 <code>supervisor.yaml</code>（默认 <code>~/.sous/</code>）。连接测试只发送合成文本；选择远程模型后，对话仍会发送给该模型。仅离线使用请选择本机模型。</p>}
    <button type="button" className="btn justify-self-start" disabled={busy || (choice === binding.selection && !binding.notice && !reconnect)} onClick={() => void save()}>
      {busy ? '正在验证…' : reconnect ? '重新连接 ACP' : '应用主管选择'}
    </button>
    {error && <p className="m-0 text-sm text-danger" role="alert">{error}</p>}
    {done && <p className="m-0 text-sm text-muted" role="status">已应用。工作区保留已有任务、记忆和对话。</p>}
  </section>
}
