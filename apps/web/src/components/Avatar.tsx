import type { PersonaColor } from '../lib/types'

export default function Avatar({ glyph, color = 'gray', title = '', size = 26, ring = '' }: {
  glyph: string
  color?: PersonaColor
  title?: string
  size?: number
  /** A status ring: accent, warning, success, danger or ''. */
  ring?: string
}) {
  return (
    <span
      className={`av c-${color} ${ring ? 'ring-' + ring : ''}`}
      style={{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.46)}px` }}
      title={title}
      aria-label={title || glyph}
    >
      {glyph}
    </span>
  )
}
