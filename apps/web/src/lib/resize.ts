// Panel widths the user drags, remembered per panel in localStorage.
import { useState } from 'react'

/** Keeps a width between min and max (max wins over nothing: min is the floor). */
export function clampWidth(w: number, min: number, max: number): number {
  return Math.round(Math.min(Math.max(w, min), Math.max(min, max)))
}

/** The width after dragging a panel's edge by dx pixels: a left edge grows to the left. */
export function dragWidth(start: number, dx: number, edge: 'left' | 'right'): number {
  return edge === 'left' ? start - dx : start + dx
}

function read(key: string, fallback: number): number {
  try {
    const v = Number(localStorage.getItem(key))
    return Number.isFinite(v) && v > 0 ? v : fallback
  } catch {
    return fallback
  }
}

/** A remembered width: [width, set, reset]. */
export function useStoredWidth(key: string, fallback: number): [number, (w: number) => void, () => void] {
  const [w, setW] = useState(() => read(key, fallback))
  const set = (v: number) => {
    setW(v)
    try {
      localStorage.setItem(key, String(v))
    } catch {
      // Private windows may refuse; the width still applies until reload.
    }
  }
  const reset = () => {
    setW(fallback)
    try {
      localStorage.removeItem(key)
    } catch {
      // ignore
    }
  }
  return [w, set, reset]
}
