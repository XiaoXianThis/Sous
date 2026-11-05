// Light, dark or follow the system. The choice is per browser (localStorage);
// the page reads the result from <html data-theme>. index.html applies the
// saved choice before the app loads, so there is no flash.
import { useSyncExternalStore } from 'react'

export type ThemePref = 'system' | 'light' | 'dark'

const KEY = 'sous.theme'
const listeners = new Set<() => void>()
let pref: ThemePref = read()

function read(): ThemePref {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // Storage may be blocked; follow the system.
  }
  return 'system'
}

const media = typeof matchMedia !== 'undefined' ? matchMedia('(prefers-color-scheme: dark)') : null

/** The theme drawn now, after resolving "system". */
export function resolved(p: ThemePref = pref): 'light' | 'dark' {
  if (p === 'system') return media?.matches ? 'dark' : 'light'
  return p
}

export function applyTheme() {
  if (typeof document === 'undefined') return
  const t = resolved()
  document.documentElement.dataset.theme = t
  for (const m of document.querySelectorAll('meta[name="theme-color"]')) m.setAttribute('content', t === 'dark' ? '#141414' : '#f3f3f3')
  // A home-screen app reads this at launch: white status-bar text over the dark theme.
  document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]')?.setAttribute('content', t === 'dark' ? 'black-translucent' : 'default')
}

export function setTheme(p: ThemePref) {
  pref = p
  try {
    localStorage.setItem(KEY, p)
  } catch {
    // Not saved; it still applies to this page.
  }
  applyTheme()
  for (const l of listeners) l()
}

media?.addEventListener?.('change', () => {
  if (pref === 'system') {
    applyTheme()
    for (const l of listeners) l()
  }
})

export function useTheme(): ThemePref {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => pref,
  )
}
