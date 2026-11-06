import * as Menu from '@radix-ui/react-dropdown-menu'
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { ui } from '../lib/app'
import type { ModelsView } from '../lib/types'
import Icon from './Icon'

const effortText: Record<string, string> = { low: '低', medium: '中', high: '高', xhigh: '很高', max: '最高' }

const panel = 'z-[60] bg-raised rounded-[12px] p-1.5 shadow-pop rise outline-none'
const item = 'flex items-center gap-2 h-8 px-2.5 rounded-[9px] text-[13px] text-ink outline-none cursor-default select-none data-[highlighted]:bg-hover data-[state=open]:bg-hover'
const label = 'px-2.5 pt-1.5 pb-0.5 text-[11px] text-faint'

/**
 * The supervisor's model and thinking effort, switchable from the chat
 * composer: a list of models; pointing at (or tapping) one opens a second
 * panel with its thinking effort. Placement, collisions and scroll locking
 * come from Radix. The change is kept on the Hub and applies to the next reply.
 */
export default function ModelSwitch({ placement = 'composer' }: { placement?: 'composer' | 'header' }) {
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<ModelsView | null>(null)
  const [error, setError] = useState('')

  const load = () => api.models().then(setView).catch((e) => setError((e as Error).message))
  useEffect(() => { void load() }, [])
  useEffect(() => { if (open) void load() }, [open])

  const sup = view?.roles.supervisor
  const name = sup?.model || '未选模型'
  const efforts = ['', ...(view?.efforts ?? [])]

  async function save(provider_id: string, model: string, effort: string) {
    setError('')
    try {
      setView(await api.putRoles({ supervisor: { provider_id, model, effort } }))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <Menu.Root open={open} onOpenChange={setOpen}>
      <Menu.Trigger asChild>
        <button
          type="button"
          className={`${placement === 'header' ? 'glass-pill glass max-w-[44vw] min-w-0' : 'flex items-center gap-1 h-8 px-2 rounded-[9px] border-0 bg-transparent text-[12px] hover:bg-hover max-w-[200px] max-[760px]:max-w-[150px] min-w-0'} ${sup?.model ? 'text-muted' : 'text-warm'}`}
          aria-label={`主管的模型：${name}`}
          data-tip={placement === 'header' ? undefined : '主管用的模型'}
        >
          <Icon name="cpu" size={14} />
          <span className="overflow-hidden text-ellipsis whitespace-nowrap font-mono">{name}</span>
          {sup?.effort && <span className="text-faint whitespace-nowrap shrink-0">· {effortText[sup.effort] ?? sup.effort}</span>}
        </button>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Content side={placement === 'header' ? 'bottom' : 'top'} align={placement === 'header' ? 'center' : 'start'} sideOffset={4} collisionPadding={8}
          className={`${panel} w-[260px] max-w-[calc(100vw-16px)] max-h-[min(420px,var(--radix-dropdown-menu-content-available-height))] overflow-y-auto`}>
          {view && view.providers.length === 0 && <p className="m-0 p-2 text-[13px] text-muted">还没有模型供应商。</p>}
          {view?.providers.map((pr) => (
            <Menu.Group key={pr.id}>
              <Menu.Label className={label}>{pr.name}</Menu.Label>
              {pr.models.length === 0 && <span className="block px-2.5 py-1 text-[12px] text-faint">（还没有模型）</span>}
              {pr.models.map((m) => {
                const on = sup?.provider_id === pr.id && sup.model === m
                return (
                  <Menu.Sub key={m}>
                    <Menu.SubTrigger className={item} onClick={() => { if (!on) void save(pr.id, m, sup?.effort ?? '') }}>
                      <span className="flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[12.5px]">{m}</span>
                      {on && sup?.effort && <span className="text-faint text-[12px] shrink-0">{effortText[sup.effort] ?? sup.effort}</span>}
                      {on ? <Icon name="check" size={14} /> : <Icon name="chevron" size={12} className="-rotate-90 text-faint shrink-0" />}
                    </Menu.SubTrigger>
                    <Menu.Portal>
                      <Menu.SubContent sideOffset={4} collisionPadding={8} className={`${panel} w-[150px]`}>
                        <Menu.Label className={label}>思考强度</Menu.Label>
                        <Menu.RadioGroup value={on ? sup?.effort ?? '' : null as unknown as string} onValueChange={(x) => void save(pr.id, m, x)}>
                          {efforts.map((x) => (
                            <Menu.RadioItem key={x} value={x} className={`${item} justify-between`}>
                              <span>{x ? effortText[x] ?? x : '默认'}</span>
                              <Menu.ItemIndicator><Icon name="check" size={14} /></Menu.ItemIndicator>
                            </Menu.RadioItem>
                          ))}
                        </Menu.RadioGroup>
                      </Menu.SubContent>
                    </Menu.Portal>
                  </Menu.Sub>
                )
              })}
            </Menu.Group>
          ))}
          {error && <p className="m-0 px-2.5 py-1 text-[12px] text-danger">{error}</p>}
          <Menu.Item className={`${item} text-[12.5px] text-accent mt-1`} onSelect={() => { ui.state.settings = 'models'; ui.emit() }}>
            管理供应商和其他角色的模型
          </Menu.Item>
        </Menu.Content>
      </Menu.Portal>
    </Menu.Root>
  )
}
