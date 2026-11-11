// A single document-wide tooltip layer drawn in <body>, driven by `data-tip` and
// `data-tip-pos` attributes via event delegation. Keeping it out of the trigger's
// DOM subtree prevents tooltips inside scrollable or clipped containers (composer,
// settings pages, dialogs) from being clipped or stretching scrollbars and causing
// layout-reflow flicker.
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { EDGE_GAP } from './popover'

export { EDGE_GAP }

/** Hover delay before a tooltip appears (ms); hiding is immediate. */
export const TIP_DELAY = 300

/** Maximum tooltip width before text wraps (px). */
export const TIP_MAX_W = 280

export type TipSide = 'right' | 'left' | 'bottom' | 'top'

export interface RectLike {
  left: number
  top: number
  right?: number
  bottom?: number
  width: number
  height: number
}

function canHover(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return true
  if (window.matchMedia('(hover: none)').matches) return false
  return window.matchMedia('(hover: hover)').matches
}

const clamp = (v: number, min: number, max: number) => Math.min(Math.max(min, v), Math.max(min, max))

/**
 * Places a tooltip of size `tip` around `anchor` within a `vw` × `vh` viewport:
 * - default ('right'): vertically centered to the right of `anchor`, flipping left if it overflows;
 * - 'bottom': horizontally centered below `anchor`, flipping above if it overflows;
 * - 'left' / 'top': symmetric counterparts;
 * Finally clamps `left` and `top` at least `EDGE_GAP` (8px) inside the viewport edges.
 */
export function placeTip(
  anchor: RectLike,
  tip: { width: number; height: number },
  pos: string | null | undefined,
  vw: number,
  vh: number,
): { left: number; top: number; side: TipSide } {
  const aRight = anchor.right ?? anchor.left + anchor.width
  const aBottom = anchor.bottom ?? anchor.top + anchor.height
  const w = tip.width
  const h = tip.height
  const gap = pos === 'bottom' || pos === 'top' ? 8 : 10

  let left = 0
  let top = 0
  let side: TipSide = pos === 'bottom' || pos === 'top' || pos === 'left' ? pos : 'right'

  if (side === 'bottom') {
    left = anchor.left + (anchor.width - w) / 2
    top = aBottom + gap
    if (top + h > vh - EDGE_GAP && (anchor.top - gap - h >= EDGE_GAP || anchor.top > vh - aBottom)) {
      top = anchor.top - gap - h
      side = 'top'
    }
  } else if (side === 'top') {
    left = anchor.left + (anchor.width - w) / 2
    top = anchor.top - gap - h
    if (top < EDGE_GAP && (aBottom + gap + h <= vh - EDGE_GAP || vh - aBottom > anchor.top)) {
      top = aBottom + gap
      side = 'bottom'
    }
  } else if (side === 'left') {
    left = anchor.left - gap - w
    top = anchor.top + (anchor.height - h) / 2
    if (left < EDGE_GAP && (aRight + gap + w <= vw - EDGE_GAP || vw - aRight > anchor.left)) {
      left = aRight + gap
      side = 'right'
    }
  } else {
    left = aRight + gap
    top = anchor.top + (anchor.height - h) / 2
    if (left + w > vw - EDGE_GAP && (anchor.left - gap - w >= EDGE_GAP || anchor.left > vw - aRight)) {
      left = anchor.left - gap - w
      side = 'left'
    }
  }

  left = clamp(Math.round(left), EDGE_GAP, vw - EDGE_GAP - w)
  top = clamp(Math.round(top), EDGE_GAP, vh - EDGE_GAP - h)
  return { left, top, side }
}

interface ActiveTip {
  el: HTMLElement
  text: string
  pos: string
}

export function TipLayer({ delay = TIP_DELAY }: { delay?: number } = {}) {
  const [active, setActive] = useState<ActiveTip | null>(null)
  const tipRef = useRef<HTMLDivElement | null>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pendingRef = useRef<HTMLElement | null>(null)
  const activeElRef = useRef<HTMLElement | null>(null)
  const pointerDownRef = useRef(false)

  useEffect(() => {
    const clearTimer = () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      pendingRef.current = null
    }

    const hide = () => {
      clearTimer()
      if (activeElRef.current !== null) {
        activeElRef.current = null
        setActive(null)
      }
    }

    const showNow = (el: HTMLElement) => {
      clearTimer()
      const text = el.getAttribute('data-tip')?.trim() ?? ''
      if (!text || !el.isConnected) {
        hide()
        return
      }
      const pos = el.getAttribute('data-tip-pos') ?? 'right'
      activeElRef.current = el
      setActive({ el, text, pos })
    }

    const onPointerOver = (e: PointerEvent) => {
      if (e.pointerType === 'touch' || !canHover()) return
      const el = e.target instanceof Element ? e.target.closest<HTMLElement>('[data-tip]') : null
      if (!el || !el.getAttribute('data-tip')?.trim()) {
        if (activeElRef.current || pendingRef.current) hide()
        return
      }
      if (el === activeElRef.current || el === pendingRef.current) return
      hide()
      if (delay <= 0) {
        showNow(el)
      } else {
        pendingRef.current = el
        timerRef.current = setTimeout(() => showNow(el), delay)
      }
    }

    const onPointerOut = (e: PointerEvent) => {
      const from = e.target instanceof Element ? e.target.closest<HTMLElement>('[data-tip]') : null
      if (!from || (from !== activeElRef.current && from !== pendingRef.current)) return
      const to = e.relatedTarget instanceof Element ? e.relatedTarget.closest<HTMLElement>('[data-tip]') : null
      if (to === from) return
      hide()
    }

    const onPointerDown = (e: PointerEvent) => {
      pointerDownRef.current = true
      setTimeout(() => {
        pointerDownRef.current = false
      }, 0)
      if (e.pointerType === 'touch' || !canHover()) hide()
    }

    const onKeyDown = (e: KeyboardEvent) => {
      pointerDownRef.current = false
      if (e.key === 'Escape') hide()
    }

    const onFocusIn = (e: FocusEvent) => {
      const el = e.target instanceof Element ? e.target.closest<HTMLElement>('[data-tip]') : null
      if (!el || !el.getAttribute('data-tip')?.trim()) {
        if (activeElRef.current || pendingRef.current) hide()
        return
      }
      if (pointerDownRef.current) return
      showNow(el)
    }

    const onFocusOut = (e: FocusEvent) => {
      const from = e.target instanceof Element ? e.target.closest<HTMLElement>('[data-tip]') : null
      if (!from || (from !== activeElRef.current && from !== pendingRef.current)) return
      const to = e.relatedTarget instanceof Element ? e.relatedTarget.closest<HTMLElement>('[data-tip]') : null
      if (to === from) return
      hide()
    }

    const onScroll = () => {
      if (activeElRef.current || pendingRef.current) hide()
    }

    const onClick = () => {
      setTimeout(() => {
        if (activeElRef.current && !activeElRef.current.isConnected) hide()
      }, 0)
    }

    document.addEventListener('pointerover', onPointerOver)
    document.addEventListener('pointerout', onPointerOut)
    document.addEventListener('pointerdown', onPointerDown, true)
    document.addEventListener('keydown', onKeyDown, true)
    document.addEventListener('focusin', onFocusIn)
    document.addEventListener('focusout', onFocusOut)
    document.addEventListener('click', onClick, true)
    window.addEventListener('scroll', onScroll, true)

    return () => {
      clearTimer()
      document.removeEventListener('pointerover', onPointerOver)
      document.removeEventListener('pointerout', onPointerOut)
      document.removeEventListener('pointerdown', onPointerDown, true)
      document.removeEventListener('keydown', onKeyDown, true)
      document.removeEventListener('focusin', onFocusIn)
      document.removeEventListener('focusout', onFocusOut)
      document.removeEventListener('click', onClick, true)
      window.removeEventListener('scroll', onScroll, true)
    }
  }, [delay])

  useLayoutEffect(() => {
    const tipEl = tipRef.current
    if (!active || !tipEl) return
    if (!active.el.isConnected) {
      activeElRef.current = null
      setActive(null)
      return
    }
    const update = () => {
      if (!active.el.isConnected || !tipRef.current) return
      const vw = window.innerWidth || document.documentElement.clientWidth || 1024
      const vh = window.innerHeight || document.documentElement.clientHeight || 768
      const anchorRect = active.el.getBoundingClientRect()
      const tipRect = tipRef.current.getBoundingClientRect()
      const width = tipRect.width || tipRef.current.offsetWidth || 0
      const height = tipRect.height || tipRef.current.offsetHeight || 0
      const { left, top, side } = placeTip(anchorRect, { width, height }, active.pos, vw, vh)
      tipRef.current.style.left = `${left}px`
      tipRef.current.style.top = `${top}px`
      tipRef.current.dataset.side = side
    }
    update()

    const mo = new MutationObserver(() => {
      const text = active.el.getAttribute('data-tip')?.trim() ?? ''
      if (!text) {
        activeElRef.current = null
        setActive(null)
        return
      }
      const pos = active.el.getAttribute('data-tip-pos') ?? 'right'
      setActive((cur) => (cur && cur.el === active.el ? { el: active.el, text, pos } : cur))
    })
    mo.observe(active.el, { attributes: true, attributeFilter: ['data-tip', 'data-tip-pos'] })
    window.addEventListener('resize', update)
    return () => {
      mo.disconnect()
      window.removeEventListener('resize', update)
    }
  }, [active])

  if (!active || typeof document === 'undefined') return null

  return createPortal(
    <div
      ref={tipRef}
      role="tooltip"
      data-tip-layer=""
      className="fixed z-[80] pointer-events-none select-none w-max max-w-[min(280px,calc(100vw-16px))] whitespace-normal break-words [overflow-wrap:anywhere] bg-ink text-page text-[12px] font-semibold leading-[1.4] px-2.5 py-[5px] rounded-[10px] shadow-pop animate-[fade-in_.12s_ease-out] motion-reduce:animate-none"
      style={{ left: 0, top: 0 }}
    >
      {active.text}
    </div>,
    document.body,
  )
}
