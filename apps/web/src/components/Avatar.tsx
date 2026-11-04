import type { PersonaColor } from '../lib/types'
import AgentIcon from './AgentIcon'

/** A persona's round avatar; with agent, the agent's mark sits on its corner. */
export default function Avatar({ glyph, color = 'gray', title = '', size = 26, ring = '', agent = '' }: {
  glyph: string
  color?: PersonaColor
  title?: string
  size?: number
  /** A status ring: accent, warning, success, danger or ''. */
  ring?: string
  /** An agent id whose mark is drawn as a badge. */
  agent?: string
}) {
  const face = (
    <span
      className={`av c-${color} ${ring ? 'ring-' + ring : ''}`}
      style={{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.44)}px` }}
      title={title}
      aria-label={title || glyph}
    >
      {glyph}
    </span>
  )
  if (!agent) return face
  const badge = Math.max(12, Math.round(size * 0.5))
  return (
    <span className="relative inline-flex flex-none" style={{ width: size, height: size }}>
      {face}
      <span className="absolute rounded-[30%] p-px bg-panel" style={{ right: -badge * 0.28, bottom: -badge * 0.22 }}>
        <AgentIcon name={agent} size={badge} />
      </span>
    </span>
  )
}

/**
 * Who works on something: the persona's avatar with its agent's mark, or just
 * the agent's mark when there is no persona. stack draws them side by side.
 */
export function Who({ persona, agent = '', size = 26, stack = false, ring = '' }: {
  persona?: { name: string; glyph?: string; color?: PersonaColor }
  agent?: string
  size?: number
  stack?: boolean
  ring?: string
}) {
  if (!persona) {
    return agent ? <AgentIcon name={agent} size={size} /> : <Avatar glyph="?" color="gray" size={size} title="未分配" ring={ring} />
  }
  const glyph = persona.glyph || persona.name.slice(0, 1)
  if (!agent) return <Avatar glyph={glyph} color={persona.color} title={persona.name} size={size} ring={ring} />
  if (!stack) return <Avatar glyph={glyph} color={persona.color} title={persona.name} size={size} ring={ring} agent={agent} />
  return (
    <span className="inline-flex items-center flex-none">
      <Avatar glyph={glyph} color={persona.color} title={persona.name} size={size} ring={ring} />
      <span className="-ml-1.5 rounded-[32%] p-[1.5px] bg-panel inline-grid">
        <AgentIcon name={agent} size={size - 3} />
      </span>
    </span>
  )
}
