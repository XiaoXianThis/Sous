// A project's app-style tile: its initial on a gradient picked from its name.
export function hueOf(s: string): number {
  let h = 0
  for (const ch of s) h = (h * 31 + ch.codePointAt(0)!) % 360
  return h
}

export default function ProjectTile({ name, id = name, size = 36, misc = false }: {
  name: string
  id?: string
  size?: number
  misc?: boolean
}) {
  const initial = [...(name.trim() || '?')][0].toUpperCase()
  return (
    <span
      className="tile"
      style={{
        width: size, height: size, borderRadius: Math.round(size * 0.3), fontSize: Math.round(size * 0.46),
        ['--h' as string]: misc ? 40 : hueOf(id),
        ...(misc ? { background: 'linear-gradient(140deg,#b9bbb3,#8a8c84)' } : {}),
      }}
      aria-hidden="true"
    >
      {misc ? (
        <svg width={size * 0.52} height={size * 0.52} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 3l9 5l-9 5l-9-5zM3 13l9 5l9-5" />
        </svg>
      ) : initial}
    </span>
  )
}
