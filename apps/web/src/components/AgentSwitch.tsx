import type { AgentInfo } from '../lib/types'

export default function AgentSwitch({ agent, disabled, onChange }: {
  agent: AgentInfo
  disabled?: boolean
  onChange: (disabled: boolean) => void
}) {
  const enabled = agent.installed && !agent.disabled
  return (
    <button type="button" role="switch" aria-label={`启用 ${agent.title}`} aria-checked={enabled}
      disabled={disabled || !agent.installed} title={!agent.installed ? '请先安装' : enabled ? '停用' : '启用'}
      className={`relative inline-flex items-center w-9 h-5 rounded-full flex-none transition-colors disabled:opacity-40 ${enabled ? 'bg-accent' : 'bg-sunken'}`}
      onClick={() => onChange(enabled)}>
      <span className={`absolute w-4 h-4 rounded-full bg-panel shadow-soft transition-transform ${enabled ? 'translate-x-[18px]' : 'translate-x-0.5'}`} />
    </button>
  )
}
