// App-style marks for the coding agents sous drives: a rounded tile in the
// agent's color with a simple glyph. Unknown agents get their first letter.
import type { ReactNode } from 'react'

interface Mark {
  bg: string
  fg: string
  glyph: ReactNode
}

const marks: Record<string, Mark> = {
  claude: {
    bg: 'linear-gradient(145deg, #e58a63, #c9643f)',
    fg: '#fff',
    // An eight-ray burst.
    glyph: (
      <g stroke="currentColor" strokeWidth="2.6" strokeLinecap="round">
        <path d="M12 4.5v15M4.5 12h15M6.7 6.7l10.6 10.6M17.3 6.7L6.7 17.3" />
      </g>
    ),
  },
  codex: {
    bg: 'linear-gradient(145deg, #2a2b2f, #0d0d10)',
    fg: '#fff',
    // A terminal prompt in a soft cloud.
    glyph: (
      <g fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round">
        <path d="M7.5 18.5a4 4 0 0 1-.6-7.95A5.5 5.5 0 0 1 17.6 9.2a4.7 4.7 0 0 1-.6 9.3z" />
        <path d="M9.6 12.3l2 1.7l-2 1.7M13.2 15.7h2" />
      </g>
    ),
  },
  cursor: {
    bg: 'linear-gradient(145deg, #3a3a3e, #141416)',
    fg: '#fff',
    // An isometric cube.
    glyph: (
      <g fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinejoin="round">
        <path d="M12 3.8l7.2 4.1v8.2L12 20.2l-7.2-4.1V7.9z" />
        <path d="M4.8 7.9L12 12l7.2-4.1M12 12v8.2" />
        <path d="M12 12l7.2-4.1L12 3.8z" fill="currentColor" fillOpacity=".35" />
      </g>
    ),
  },
  opencode: {
    bg: 'linear-gradient(145deg, #5b6472, #2f353f)',
    fg: '#fff',
    // Code brackets.
    glyph: (
      <g fill="none" stroke="currentColor" strokeWidth="2.3" strokeLinecap="round" strokeLinejoin="round">
        <path d="M8.5 7.5L4 12l4.5 4.5M15.5 7.5L20 12l-4.5 4.5M13.2 5.8l-2.4 12.4" />
      </g>
    ),
  },
  kiro: {
    bg: 'linear-gradient(145deg, #9a6bff, #6a35e0)',
    fg: '#fff',
    // A small ghost.
    glyph: (
      <g>
        <path
          d="M12 3.8a6.2 6.2 0 0 0-6.2 6.2v9.6l2.1-1.6l2 1.6l2.1-1.6l2.1 1.6l2-1.6l2.1 1.6V10A6.2 6.2 0 0 0 12 3.8z"
          fill="currentColor"
        />
        <circle cx="9.7" cy="10.4" r="1.3" fill="#6a35e0" />
        <circle cx="14.3" cy="10.4" r="1.3" fill="#6a35e0" />
      </g>
    ),
  },
  gemini: {
    bg: 'linear-gradient(145deg, #5b8cff, #8a5bff)',
    fg: '#fff',
    glyph: <path d="M12 3c.6 4.8 4.2 8.4 9 9c-4.8.6-8.4 4.2-9 9c-.6-4.8-4.2-8.4-9-9c4.8-.6 8.4-4.2 9-9z" fill="currentColor" />,
  },
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

const titles: Record<string, string> = {
  claude: 'Claude Code', codex: 'Codex', cursor: 'Cursor', opencode: 'OpenCode', kiro: 'Kiro', gemini: 'Gemini', sous: 'sous',
}

/** The display name of an agent id. */
export function agentTitle(name: string): string {
  return titles[name] ?? name
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
