// Agent marks (paths and names) live in lib/agents.ts. sous retains its own app mark.
// Unknown agents get their first letter.
import type { ReactNode } from 'react'
import { AGENT_MARKS, agentTitle } from '../lib/agents'

export { agentTitle }

interface Mark {
  bg: string
  fg: string
  glyph: ReactNode
}

const marks: Record<string, Mark> = {
  ...Object.fromEntries(
    Object.entries(AGENT_MARKS).map(([k, m]) => [
      k,
      { bg: m.bg, fg: m.fg, glyph: <path d={m.d} fill="currentColor" {...(m.evenodd && { fillRule: 'evenodd' as const, clipRule: 'evenodd' as const })} /> },
    ]),
  ),
  sous: {
    bg: 'linear-gradient(145deg, #2b2b2b, #141414)',
    fg: '#fff',
    // The cloche from the app icon.
    glyph: (
      <g fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round">
        <path d="M5.5 16a6.5 6.5 0 0 1 13 0M4 16.5h16" />
        <path d="M18.8 16.5l1.9-1.3" stroke="#5fb3f0" />
        <circle cx="12" cy="7.4" r=".9" fill="#5fb3f0" stroke="#5fb3f0" />
      </g>
    ),
  },
}

export default function AgentIcon({ name, size = 22, title, className = '' }: {
  name: string
  size?: number
  title?: string
  className?: string
}) {
  const key = (name || '').toLowerCase()
  const m = marks[key]
  const label = title ?? agentTitle(name || 'agent')
  const radius = Math.round(size * 0.28)
  if (!m) {
    return (
      <span
        className={`inline-grid place-items-center flex-none font-bold text-white ${className}`}
        style={{ width: size, height: size, borderRadius: radius, background: 'linear-gradient(145deg,#9a9c95,#686a64)', fontSize: Math.round(size * 0.5) }}
        title={label}
        role="img"
        aria-label={label}
      >
        {(name || '?').slice(0, 1).toUpperCase()}
      </span>
    )
  }
  return (
    <span
      className={`inline-grid place-items-center flex-none ${className}`}
      style={{
        width: size, height: size, borderRadius: radius, background: m.bg, color: m.fg,
        boxShadow: 'inset 0 1px 0 rgb(255 255 255 / .18), 0 1px 2px rgb(0 0 0 / .18)',
      }}
      title={label}
      role="img"
      aria-label={label}
    >
      <svg width={Math.round(size * 0.68)} height={Math.round(size * 0.68)} viewBox="0 0 24 24" aria-hidden="true">
        {m.glyph}
      </svg>
    </span>
  )
}
