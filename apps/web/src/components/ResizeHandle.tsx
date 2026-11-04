import { useState, type PointerEvent as ReactPointerEvent } from 'react'
import { clampWidth, dragWidth } from '../lib/resize'

/**
 * A draggable panel edge. Drag, or focus it and use the arrow keys; a double
 * click restores the default width.
 */
export default function ResizeHandle({ width, min, max, edge = 'left', onWidth, onReset, label, className = '' }: {
  width: number
  min: number
  /** The largest width now (it may depend on the window). */
  max: () => number
  edge?: 'left' | 'right'
  onWidth: (w: number) => void
  onReset: () => void
  label: string
  className?: string
}) {
  const [dragging, setDragging] = useState(false)

  function down(e: ReactPointerEvent<HTMLButtonElement>) {
    if (e.button !== 0) return
    e.preventDefault()
    const el = e.currentTarget
    el.setPointerCapture(e.pointerId)
    const startX = e.clientX
    // Start from the drawn width: CSS may have clamped the stored one.
    const start = el.parentElement?.getBoundingClientRect().width ?? width
    const limit = max()
    setDragging(true)
    document.body.dataset.resizing = ''
    const move = (ev: PointerEvent) => onWidth(clampWidth(dragWidth(start, ev.clientX - startX, edge), min, limit))
    const up = () => {
      setDragging(false)
      delete document.body.dataset.resizing
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerup', up)
      el.removeEventListener('pointercancel', up)
    }
    el.addEventListener('pointermove', move)
    el.addEventListener('pointerup', up)
    el.addEventListener('pointercancel', up)
  }

  return (
    <button
      type="button"
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={Math.round(width)}
      aria-valuemin={min}
      aria-valuemax={Math.round(max())}
      title="拖动调整宽度，双击还原"
      data-dragging={dragging}
      className={`resize-handle ${edge === 'left' ? '-left-1' : '-right-1'} ${className}`}
      onPointerDown={down}
      onDoubleClick={onReset}
      onKeyDown={(e) => {
        const step = e.shiftKey ? 64 : 16
        const grow = edge === 'left' ? 'ArrowLeft' : 'ArrowRight'
        const shrink = edge === 'left' ? 'ArrowRight' : 'ArrowLeft'
        if (e.key === grow || e.key === shrink) {
          e.preventDefault()
          onWidth(clampWidth(width + (e.key === grow ? step : -step), min, max()))
        }
      }}
    ></button>
  )
}
