import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { ui } from '../lib/app'
import type { ModelsView } from '../lib/types'
import Icon from './Icon'
import { choiceValue, ModelOptions, parseChoice } from './Models'

const effortText: Record<string, string> = { low: '低', medium: '中', high: '高', xhigh: '很高', max: '最高' }

/**
 * The supervisor's model and thinking effort, switchable from the chat
 * header. The change is kept on the Hub and applies to the next reply.
 */
export default function ModelSwitch() {
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<ModelsView | null>(null)
  const [error, setError] = useState('')

  const load = () => api.models().then(setView).catch((e) => setError((e as Error).message))
  useEffect(() => { void load() }, [])
  useEffect(() => { if (open) void load() }, [open])

  const sup = view?.roles.supervisor
  const label = sup?.model || '未选模型'

  async function set(next: { provider_id?: string; model?: string; effort?: string }) {
    if (!sup && !next.model) return
    setError('')
    try {
      setView(await api.putRoles({ supervisor: { provider_id: sup?.provider_id ?? '', model: sup?.model ?? '', effort: sup?.effort ?? '', ...next } }))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div className="relative">
      <button
        className={`flex items-center gap-1 h-8 px-2 rounded-[9px] border-0 bg-transparent text-[12px] hover:bg-hover max-w-[180px] ${sup?.model ? 'text-muted' : 'text-warm'}`}
        aria-label={`主管的模型：${label}`}
        aria-expanded={open}
        data-tip="主管用的模型" data-tip-pos="bottom"
        onClick={() => setOpen(!open)}
      >
        <Icon name="cpu" size={14} />
        <span className="overflow-hidden text-ellipsis whitespace-nowrap font-mono">{label}</span>
        {sup?.effort && <span className="text-faint">· {effortText[sup.effort] ?? sup.effort}</span>}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-[24]" onClick={() => setOpen(false)}></div>
          <div className="absolute right-0 top-full mt-1 z-[25] w-[300px] bg-raised rounded-[12px] p-3 grid gap-2 shadow-pop rise">
            <strong className="text-[13px]">主管用的模型</strong>
            {view && view.providers.length === 0 ? (
              <p className="m-0 text-[13px] text-muted">还没有模型供应商。</p>
            ) : (
              <>
                <select
                  className="border border-line bg-page text-ink rounded-[10px] px-2.5 py-1.5 text-[13px]"
                  aria-label="模型"
                  value={choiceValue(sup)}
                  onChange={(e) => void set(parseChoice(e.currentTarget.value))}
                >
                  {!sup?.model && <option value="" disabled>选一个模型</option>}
                  {view && <ModelOptions providers={view.providers} />}
                </select>
                <div className="flex gap-1 flex-wrap" role="radiogroup" aria-label="思考强度">
                  {['', ...(view?.efforts ?? [])].map((x) => (
                    <button key={x} role="radio" aria-checked={(sup?.effort ?? '') === x} disabled={!sup?.model}
                      className={`h-7 px-2.5 rounded-[9px] text-[12px] border ${(sup?.effort ?? '') === x ? 'border-accent text-accent bg-transparent' : 'border-line bg-panel text-ink'}`}
                      onClick={() => void set({ effort: x })}>{x ? effortText[x] ?? x : '默认'}</button>
                  ))}
                </div>
              </>
            )}
            {error && <p className="m-0 text-[12px] text-danger">{error}</p>}
            <button className="link justify-self-start text-[12.5px]" onClick={() => { setOpen(false); ui.state.settings = 'models'; ui.emit() }}>
              管理供应商和其他角色的模型
            </button>
          </div>
        </>
      )}
    </div>
  )
}
