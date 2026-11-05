import { useSyncExternalStore } from 'react'

/** The width where the app switches to its phone layout (tab bar, large titles). Keep in step with app.css. */
export const PHONE_QUERY = '(max-width: 760px)'

const media = typeof matchMedia !== 'undefined' ? matchMedia(PHONE_QUERY) : null

function subscribe(cb: () => void) {
  media?.addEventListener('change', cb)
  return () => media?.removeEventListener('change', cb)
}

/** True while the phone layout is on; re-renders when the window crosses the breakpoint. */
export function usePhone(): boolean {
  return useSyncExternalStore(subscribe, () => media?.matches ?? false, () => false)
}
