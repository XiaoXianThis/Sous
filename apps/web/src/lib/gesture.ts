// Pure math behind the touch gestures in ios.ts: sheet drag-to-dismiss, the
// edge swipe back, timeline resizing, and the keyboard
// insets. Kept free of the DOM so it can be unit tested.

/** How far a touch must move before it counts as a drag rather than a tap. */
export const SLOP = 8
/** A swipe back only starts this close to the left edge. */
export const EDGE = 24

export type Axis = 'x' | 'y' | null

/** Which way a touch is going once it has moved past the slop; null while undecided. */
export function axisOf(dx: number, dy: number, slop = SLOP): Axis {
  if (Math.hypot(dx, dy) < slop) return null
  return Math.abs(dx) > Math.abs(dy) ? 'x' : 'y'
}

/**
 * Resistance past a limit, like iOS pulling a sheet the wrong way: the
 * further you pull, the less it moves. Never exceeds `max`.
 */
export function rubber(over: number, max = 60): number {
  if (over <= 0) return 0
  return max * (1 - 1 / (over / max + 1))
}

/**
 * Whether a drag that ends `distance` px along its dismiss direction, at
 * `velocity` px/ms, dismisses a layer that is `size` px long: a long enough
 * pull or a quick flick, never a flick back the other way.
 */
export function shouldDismiss(distance: number, velocity: number, size: number): boolean {
  if (distance <= 0 || velocity < -0.1) return false
  return distance > Math.min(size * 0.35, 160) || (velocity > 0.5 && distance > 24)
}

/**
 * Velocity in px/ms from the last few samples, newest last. A finger that has
 * rested for over 100ms before `now` has no velocity left.
 */
export function velocityOf(samples: { t: number; v: number }[], now?: number): number {
  if (samples.length < 2) return 0
  const a = samples[Math.max(0, samples.length - 4)]
  const b = samples[samples.length - 1]
  if (now !== undefined && now - b.t > 100) return 0
  const dt = b.t - a.t
  return dt > 0 ? (b.v - a.v) / dt : 0
}

/** Keep the viewport's bottom content in place when its height changes, or stick to the end. */
export function scrollAfterResize(o: { prevHeight: number; height: number; scrollTop: number; scrollHeight: number; stick: boolean }): number {
  const max = Math.max(0, o.scrollHeight - o.height)
  const top = o.stick ? max : o.scrollTop + (o.prevHeight - o.height)
  return Math.min(max, Math.max(0, top))
}

export interface ViewportBox {
  /** window.innerHeight: the layout viewport. */
  inner: number
  /** visualViewport.height: what is visible above the keyboard. */
  height: number
  /** visualViewport.offsetTop: how far iOS has panned the visible part down. */
  offsetTop: number
}

/**
 * CSS variables for the visible part of the screen. iOS keeps the layout
 * viewport when the keyboard opens and covers its bottom instead, so the app
 * sizes itself to the visual viewport: `--vv-h` (height), `--vv-top` (where it
 * starts) and `--kb` (how much the keyboard covers, for fixed bottom sheets).
 * The keyboard counts as open when it hides more than 120px.
 */
export function viewportVars(v: ViewportBox): { h: number; top: number; kb: number; open: boolean } {
  const h = Math.max(0, Math.round(v.height))
  const top = Math.max(0, Math.round(v.offsetTop))
  const kb = Math.max(0, Math.round(v.inner - v.height - v.offsetTop))
  return { h, top, kb, open: kb > 120 }
}
