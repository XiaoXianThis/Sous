<script lang="ts">
  // Outline icons drawn on a 24px grid, stroke follows currentColor.
  let { name, size = 18, label = '' }: { name: string; size?: number; label?: string } = $props()

  const paths: Record<string, string> = {
    grid: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z',
    folders: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
    inbox: 'M4 13h4l2 3h4l2-3h4M4 13l2-8h12l2 8v6H4z',
    settings: 'M12 9a3 3 0 1 0 0 6a3 3 0 0 0 0-6zM10.3 4.3a1.7 1.7 0 0 1 3.4 0a1.7 1.7 0 0 0 2.6 1.1a1.7 1.7 0 0 1 2.4 2.4a1.7 1.7 0 0 0 1 2.6a1.7 1.7 0 0 1 0 3.4a1.7 1.7 0 0 0-1 2.6a1.7 1.7 0 0 1-2.4 2.4a1.7 1.7 0 0 0-2.6 1a1.7 1.7 0 0 1-3.4 0a1.7 1.7 0 0 0-2.6-1a1.7 1.7 0 0 1-2.4-2.4a1.7 1.7 0 0 0-1-2.6a1.7 1.7 0 0 1 0-3.4a1.7 1.7 0 0 0 1-2.6a1.7 1.7 0 0 1 2.4-2.4a1.7 1.7 0 0 0 2.6-1.1z',
    chat: 'M4 5h16v11H9l-5 4z',
    play: 'M7 4v16l13-8z',
    pause: 'M7 5h3v14H7zM14 5h3v14h-3z',
    hand: 'M8 13V5.5a1.5 1.5 0 0 1 3 0V12M11 11.5v-8a1.5 1.5 0 0 1 3 0V12M14 5.5a1.5 1.5 0 0 1 3 0V12M17 7.5a1.5 1.5 0 0 1 3 0V16a6 6 0 0 1-6 6h-2a6 6 0 0 1-5.2-3L4.2 14.5a1.5 1.5 0 0 1 2.6-1.5L8 15',
    clock: 'M12 3a9 9 0 1 0 0 18a9 9 0 0 0 0-18zM12 7v5l3 3',
    check: 'M5 12l5 5L20 7',
    alert: 'M12 9v4M12 17h.01M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
    x: 'M6 6l12 12M18 6L6 18',
    circle: 'M12 4a8 8 0 1 0 0 16a8 8 0 0 0 0-16z',
    eye: 'M2 12s4-7 10-7s10 7 10 7s-4 7-10 7S2 12 2 12zM12 9a3 3 0 1 0 0 6a3 3 0 0 0 0-6z',
    rocket: 'M5 15c-1.5 1-2 4-2 6c2 0 5-.5 6-2M9 18l-3-3M14.5 4.5C17 2 21 3 21 3s1 4-1.5 6.5L13 16l-5-5zM15 9a1 1 0 1 0 0-2a1 1 0 0 0 0 2z',
    heart: 'M12 20s-7-4.5-9-9a4.5 4.5 0 0 1 9-2a4.5 4.5 0 0 1 9 2c-2 4.5-9 9-9 9z',
    branch: 'M6 3v12M18 9a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM18 9a9 9 0 0 1-9 9',
    laptop: 'M4 5h16v10H4zM2 19h20',
    server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
    plus: 'M12 5v14M5 12h14',
    back: 'M15 6l-6 6l6 6',
    send: 'M12 19V5M5 12l7-7l7 7',
    chevron: 'M6 9l6 6l6-6',
    panel: 'M4 4h16v16H4zM15 4v16',
    message: 'M4 5h16v11H9l-5 4zM8 10h8',
    stop: 'M6 6h12v12H6z',
    refresh: 'M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7',
    link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
    user: 'M12 12a4 4 0 1 0 0-8a4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
    bulb: 'M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9V16h7v-2.1A6 6 0 0 0 12 3z',
    chart: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
    bell: 'M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9M10 21h4',
    calendar: 'M4 6h16v14H4zM4 10h16M8 3v4M16 3v4',
    logout: 'M14 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-3M9 12h12M18 9l3 3l-3 3',
    dot: 'M12 11a1 1 0 1 0 0 2a1 1 0 0 0 0-2z',
    wok: 'M3 11h18a9 9 0 0 1-18 0zM21 11l2-2M9 7c0-2 2-2 2-4M13 7c0-2 2-2 2-4',
  }
  const d = $derived(paths[name] ?? paths.dot)
  const filled = $derived(name === 'play' || name === 'pause' || name === 'stop')
</script>

<svg
  width={size} height={size} viewBox="0 0 24 24" fill={filled ? 'currentColor' : 'none'} stroke="currentColor"
  stroke-width={filled ? 1 : 1.8} stroke-linecap="round" stroke-linejoin="round"
  role={label ? 'img' : undefined} aria-label={label || undefined} aria-hidden={label ? undefined : 'true'}
><path {d} /></svg>

<style>
  svg { flex: none; display: block; }
</style>
