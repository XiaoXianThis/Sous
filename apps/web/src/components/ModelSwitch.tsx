import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { ui } from '../lib/app'
import type { ModelsView } from '../lib/types'
import Icon from './Icon'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuItemIndicator, DropdownMenuLabel,
  DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger,
} from './ui/dropdown-menu'

const effortText: Record<string, string> = { low: '低', medium: '中', high: '高', xhigh: '很高', max: '最高' }


/**
 * The supervisor's model and thinking effort, switchable from the chat
 * composer: a list of models; pointing at (or tapping) one opens a second
 * panel with its thinking effort. Placement, collisions and scroll locking
 * come from Radix. The change is kept on the Hub and applies to the next reply.
 */
export default function ModelSwitch({ placement = 'composer', value, onChange }: {
  placement?: 'composer' | 'header'
  /** Controlled use (a meeting's host): shown instead of the supervisor's role. */
  value?: { provider_id?: string; model?: string; effort?: string } | null
  /** Saves a controlled choice; without it the supervisor role is saved. */
  onChange?: (v: { provider_id: string; model: string; effort: string }) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<ModelsView | null>(null)
  const [error, setError] = useState('')

  const load = () => api.models().then(setView).catch((e) => setError((e as Error).message))
  useEffect(() => { void load() }, [])
  useEffect(() => { if (open) void load() }, [open])

  const sup = value ?? view?.roles.supervisor
  const name = sup?.model || '未选模型'
  const efforts = ['', ...(view?.efforts ?? [])]

  async function save(provider_id: string, model: string, effort: string) {
    setError('')
    if (onChange) {
      try {
        await onChange({ provider_id, model, effort })
      } catch (e) {
        setError((e as Error).message)
      }
      return
    }
    try {
      setView(await api.putRoles({ supervisor: { provider_id, model, effort } }))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className={`${placement === 'header' ? 'glass-pill glass max-w-[44vw] min-w-0' : 'flex items-center gap-1 h-8 px-2 rounded-[9px] border-0 bg-transparent text-[12px] hover:bg-hover max-w-[200px] max-[760px]:max-w-[150px] min-w-0'} ${sup?.model ? 'text-muted' : 'text-warm'}`}
          aria-label={`${value !== undefined ? '会议的模型' : '主管的模型'}：${name}`}
          data-tip={placement === 'header' ? undefined : '主管用的模型'}
        >
          <Icon name="cpu" size={14} />
          <span className="overflow-hidden text-ellipsis whitespace-nowrap font-mono">{name}</span>
          {sup?.effort && <span className="text-faint whitespace-nowrap shrink-0">· {effortText[sup.effort] ?? sup.effort}</span>}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side={placement === 'header' ? 'bottom' : 'top'} align={placement === 'header' ? 'center' : 'start'}
          className="w-[260px] max-h-[min(420px,var(--radix-dropdown-menu-content-available-height))]">
          {view && view.providers.length === 0 && <p className="m-0 p-2 text-[13px] text-muted">还没有模型供应商。</p>}
          {view?.providers.map((pr) => (
            <DropdownMenuGroup key={pr.id}>
              <DropdownMenuLabel>{pr.name}</DropdownMenuLabel>
              {pr.models.length === 0 && <span className="block px-2.5 py-1 text-[12px] text-faint">（还没有模型）</span>}
              {pr.models.map((m) => {
                const on = sup?.provider_id === pr.id && sup.model === m
                return (
                  <DropdownMenuSub key={m}>
                    <DropdownMenuSubTrigger onClick={() => { if (!on) void save(pr.id, m, sup?.effort ?? '') }}>
                      <span className="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[12.5px]">{m}</span>
                      {on && sup?.effort && <span className="text-faint text-[12px] shrink-0">{effortText[sup.effort] ?? sup.effort}</span>}
                      {on ? <Icon name="check" size={14} /> : <Icon name="chevron" size={12} className="-rotate-90 text-faint shrink-0" />}
                    </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="w-[150px]">
                        <DropdownMenuLabel>思考强度</DropdownMenuLabel>
                        <DropdownMenuRadioGroup value={on ? sup?.effort ?? '' : null as unknown as string} onValueChange={(x) => void save(pr.id, m, x)}>
                          {efforts.map((x) => (
                            <DropdownMenuRadioItem key={x} value={x} className="justify-between">
                              <span>{x ? effortText[x] ?? x : '默认'}</span>
                              <DropdownMenuItemIndicator><Icon name="check" size={14} /></DropdownMenuItemIndicator>
                            </DropdownMenuRadioItem>
                          ))}
                        </DropdownMenuRadioGroup>
                      </DropdownMenuSubContent>
                  </DropdownMenuSub>
                )
              })}
            </DropdownMenuGroup>
          ))}
          {error && <p className="m-0 px-2.5 py-1 text-[12px] text-danger">{error}</p>}
          <DropdownMenuItem className="text-[12.5px] text-accent mt-1" onSelect={() => { ui.state.settings = 'models'; ui.emit() }}>
            管理供应商和其他角色的模型
          </DropdownMenuItem>
        </DropdownMenuContent>
    </DropdownMenu>
  )
}
