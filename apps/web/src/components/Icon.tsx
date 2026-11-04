// Outline icons drawn on a 24px grid, stroke follows currentColor.
const paths: Record<string, string> = {
  grid: 'M4 5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1zM14 5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1h-4a1 1 0 0 1-1-1zM4 15a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1zM14 15a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1h-4a1 1 0 0 1-1-1z',
  folders: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
  folder: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zM3 10h18',
  inbox: 'M4 13h4l2 3h4l2-3h4M4 13l2-8h12l2 8v6H4z',
  settings: 'M12 9a3 3 0 1 0 0 6a3 3 0 0 0 0-6zM10.3 4.3a1.7 1.7 0 0 1 3.4 0a1.7 1.7 0 0 0 2.6 1.1a1.7 1.7 0 0 1 2.4 2.4a1.7 1.7 0 0 0 1 2.6a1.7 1.7 0 0 1 0 3.4a1.7 1.7 0 0 0-1 2.6a1.7 1.7 0 0 1-2.4 2.4a1.7 1.7 0 0 0-2.6 1a1.7 1.7 0 0 1-3.4 0a1.7 1.7 0 0 0-2.6-1a1.7 1.7 0 0 1-2.4-2.4a1.7 1.7 0 0 0-1-2.6a1.7 1.7 0 0 1 0-3.4a1.7 1.7 0 0 0 1-2.6a1.7 1.7 0 0 1 2.4-2.4a1.7 1.7 0 0 0 2.6-1.1z',
  chat: 'M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H9l-5 4z',
  play: 'M7 4.8v14.4a1 1 0 0 0 1.5.86l11.6-7.2a1 1 0 0 0 0-1.72L8.5 3.94A1 1 0 0 0 7 4.8z',
  pause: 'M7 5h3v14H7zM14 5h3v14h-3z',
  hand: 'M8 13V5.5a1.5 1.5 0 0 1 3 0V12M11 11.5v-8a1.5 1.5 0 0 1 3 0V12M14 5.5a1.5 1.5 0 0 1 3 0V12M17 7.5a1.5 1.5 0 0 1 3 0V16a6 6 0 0 1-6 6h-2a6 6 0 0 1-5.2-3L4.2 14.5a1.5 1.5 0 0 1 2.6-1.5L8 15',
  clock: 'M12 3a9 9 0 1 0 0 18a9 9 0 0 0 0-18zM12 7v5l3 3',
  check: 'M5 12l5 5L20 7',
  'check-circle': 'M12 3a9 9 0 1 0 0 18a9 9 0 0 0 0-18zM8 12l3 3l5-6',
  alert: 'M12 9v4M12 17h.01M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
  x: 'M6 6l12 12M18 6L6 18',
  circle: 'M12 4a8 8 0 1 0 0 16a8 8 0 0 0 0-16z',
  'circle-dashed': 'M8.6 4.7a8 8 0 0 1 6.8 0M19.3 8.6a8 8 0 0 1 0 6.8M15.4 19.3a8 8 0 0 1-6.8 0M4.7 15.4a8 8 0 0 1 0-6.8',
  eye: 'M2 12s4-7 10-7s10 7 10 7s-4 7-10 7S2 12 2 12zM12 9a3 3 0 1 0 0 6a3 3 0 0 0 0-6z',
  rocket: 'M5 15c-1.5 1-2 4-2 6c2 0 5-.5 6-2M9 18l-3-3M14.5 4.5C17 2 21 3 21 3s1 4-1.5 6.5L13 16l-5-5zM15 9a1 1 0 1 0 0-2a1 1 0 0 0 0 2z',
  heart: 'M12 20s-7-4.5-9-9a4.5 4.5 0 0 1 9-2a4.5 4.5 0 0 1 9 2c-2 4.5-9 9-9 9z',
  branch: 'M6 3v12M18 9a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM18 9a9 9 0 0 1-9 9',
  pr: 'M6 3a3 3 0 1 0 0 6a3 3 0 0 0 0-6zM6 9v12M18 15a3 3 0 1 0 0 6a3 3 0 0 0 0-6zM18 15V8a2 2 0 0 0-2-2h-4M14 3l-3 3l3 3',
  laptop: 'M5 6a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v9H5zM2 18h20',
  server: 'M4 5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1zM4 15a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1zM8 7h.01M8 17h.01',
  plus: 'M12 5v14M5 12h14',
  back: 'M15 6l-6 6l6 6',
  forward: 'M9 6l6 6l-6 6',
  send: 'M12 19V5M5 12l7-7l7 7',
  chevron: 'M6 9l6 6l6-6',
  panel: 'M5 4h14a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM15 4v16',
  message: 'M4 5h16v11H9l-5 4zM8 10h8',
  stop: 'M7 6h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1z',
  refresh: 'M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7',
  link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
  external: 'M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5',
  user: 'M12 12a4 4 0 1 0 0-8a4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
  users: 'M9 11a4 4 0 1 0 0-8a4 4 0 0 0 0 8zM2 21a7 7 0 0 1 14 0M16 3.5a4 4 0 0 1 0 7M18 14a7 7 0 0 1 4 7',
  bulb: 'M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9V16h7v-2.1A6 6 0 0 0 12 3z',
  brain: 'M12 5a3 3 0 0 0-5.8-1A3 3 0 0 0 4 8a3 3 0 0 0 0 5a3 3 0 0 0 2 5a3 3 0 0 0 6 1zM12 5a3 3 0 0 1 5.8-1A3 3 0 0 1 20 8a3 3 0 0 1 0 5a3 3 0 0 1-2 5a3 3 0 0 1-6 1zM12 5v14',
  chart: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
  bell: 'M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9M10 21h4',
  calendar: 'M5 5h14a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM4 10h16M8 3v4M16 3v4',
  logout: 'M14 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-3M9 12h12M18 9l3 3l-3 3',
  dot: 'M12 11a1 1 0 1 0 0 2a1 1 0 0 0 0-2z',
  wok: 'M3 11h18a9 9 0 0 1-18 0zM21 11l2-2M9 7c0-2 2-2 2-4M13 7c0-2 2-2 2-4',
  cloche: 'M4 17a8 8 0 0 1 16 0M2 17h20M12 7V5M10 5h4',
  terminal: 'M4 5h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM7 10l3 2.5L7 15M12 15h5',
  bolt: 'M13 3L4 14h7l-1 7l9-11h-7z',
  sparkles: 'M12 3l1.8 4.7L18.5 9.5l-4.7 1.8L12 16l-1.8-4.7L5.5 9.5l4.7-1.8zM19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z',
  more: 'M5 12h.01M12 12h.01M19 12h.01',
  edit: 'M4 20h4L19 9l-4-4L4 16zM14 6l4 4',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
  copy: 'M9 9h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V10a1 1 0 0 1 1-1zM5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1',
  file: 'M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8zM14 3v5h5',
  diff: 'M12 3v8M8 7h8M8 17h8M5 21h14',
  log: 'M4 6h16M4 10h16M4 14h10M4 18h7',
  github: 'M9 19c-4.3 1.4-4.3-2.5-6-3m12 5v-3.5c0-1 .1-1.4-.5-2c2.8-.3 5.5-1.4 5.5-6a4.6 4.6 0 0 0-1.3-3.2a4.2 4.2 0 0 0-.1-3.2s-1.1-.3-3.5 1.3a12.3 12.3 0 0 0-6.2 0C6.5 2.8 5.4 3.1 5.4 3.1a4.2 4.2 0 0 0-.1 3.2A4.6 4.6 0 0 0 4 9.5c0 4.6 2.7 5.7 5.5 6c-.6.6-.6 1.2-.5 2V21',
  globe: 'M12 3a9 9 0 1 0 0 18a9 9 0 0 0 0-18zM3 12h18M12 3c2.5 2.7 3.8 5.7 3.8 9s-1.3 6.3-3.8 9c-2.5-2.7-3.8-5.7-3.8-9S9.5 5.7 12 3z',
  filter: 'M4 5h16l-6 8v5l-4 2v-7z',
  archive: 'M3 6a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1zM5 9v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9M10 13h4',
  unarchive: 'M3 6a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1zM5 9v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9M12 17v-5M9.5 14.5L12 12l2.5 2.5',
  'folder-open': 'M3 18V7a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v1M3 18l2.4-6.6A2 2 0 0 1 7.3 10H20a1 1 0 0 1 .95 1.3l-2 6.3A2 2 0 0 1 17 19H4a1 1 0 0 1-1-1z',
  search: 'M11 4a7 7 0 1 0 0 14a7 7 0 0 0 0-14zM20 20l-4-4',
  layers: 'M12 3l9 5l-9 5l-9-5zM3 13l9 5l9-5',
  list: 'M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01',
  target: 'M12 3a9 9 0 1 0 0 18a9 9 0 0 0 0-18zM12 7a5 5 0 1 0 0 10a5 5 0 0 0 0-10zM12 11a1 1 0 1 0 0 2a1 1 0 0 0 0-2z',
  shield: 'M12 3l8 3v6c0 5-3.5 8-8 9c-4.5-1-8-4-8-9V6z',
  key: 'M15 3a6 6 0 1 0 0 12a6 6 0 0 0 0-12zM10.8 13.2L3 21M6 18l2 2M15 7.5h.01',
  download: 'M12 4v11M7 10l5 5l5-5M5 20h14',
  pulse: 'M3 12h4l3-7l4 14l3-7h4',
  sun: 'M12 8a4 4 0 1 0 0 8a4 4 0 0 0 0-8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
  moon: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z',
  expand: 'M14 4h6v6M10 20H4v-6M20 4l-7 7M4 20l7-7',
  cpu: 'M7 7h10v10H7zM10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4',
}

// Icons snap to one scale so neighbours look alike; thin strokes at small sizes.
const scale = [12, 14, 16, 18, 20, 24, 28]
const snap = (n: number) => scale.reduce((a, b) => (Math.abs(b - n) < Math.abs(a - n) ? b : a))

const filledIcons = new Set(['play', 'pause', 'stop', 'bolt'])

export default function Icon({ name, size = 18, label = '', stroke = 1.8, className = '' }: {
  name: string
  size?: number
  label?: string
  stroke?: number
  className?: string
}) {
  const d = paths[name] ?? paths.dot
  size = size > 28 ? size : snap(size)
  stroke = size <= 14 ? 2.1 : size >= 20 ? 1.7 : 1.9
  const filled = filledIcons.has(name)
  return (
    <svg
      width={size} height={size} viewBox="0 0 24 24" fill={filled ? 'currentColor' : 'none'} stroke="currentColor"
      strokeWidth={filled ? 1 : stroke} strokeLinecap="round" strokeLinejoin="round"
      role={label ? 'img' : undefined} aria-label={label || undefined} aria-hidden={label ? undefined : true}
      className={`flex-none block ${className}`}
    >
      <path d={d} />
    </svg>
  )
}
