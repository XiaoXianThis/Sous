import { useEffect, useRef, useState, type ReactNode } from 'react'
import Icon from './Icon'

/**
 * The top of a phone page, after iOS 26/27 Liquid Glass: the bar has no
 * background of its own, just glass buttons floating over the content
 * (`leading`, and `actions` grouped in one glass capsule), with a large title
 * under it. Once the large title scrolls away a soft fade appears behind the
 * bar and the title shows small in its middle. Lives inside the page's scroll
 * container, at its very top.
 */
export default function PhoneHeader({ title, leading, actions, fade, children }: {
  title: string
  leading?: ReactNode
  /** Trailing actions, each a `.phone-bar-btn`; drawn in one glass capsule. */
  actions?: ReactNode
  /** The page color the fade blends into (default: the grouped gray). */
  fade?: string
  /** Drawn under the large title (summary tiles, a search field, ...). */
  children?: ReactNode
}) {
  const big = useRef<HTMLHeadingElement>(null)
  const bar = useRef<HTMLDivElement>(null)
  const [collapsed, setCollapsed] = useState(false)

  useEffect(() => {
    const el = big.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    // The title counts as gone once it slides under the bar (44px plus the status bar).
    const top = bar.current?.offsetHeight ?? 44
    const io = new IntersectionObserver(([e]) => setCollapsed(!e.isIntersecting), { rootMargin: `-${top}px 0px 0px 0px`, threshold: 0 })
    io.observe(el)
    return () => io.disconnect()
  }, [])

  return (
    <>
      <div ref={bar} className={`phone-bar ${collapsed ? 'is-collapsed' : ''}`} style={fade ? ({ '--fade': fade } as React.CSSProperties) : undefined}>
        {/* Equal side columns keep the small title centered on the screen. */}
        <div className="grid grid-cols-[minmax(44px,1fr)_minmax(0,auto)_minmax(44px,1fr)] items-center h-[56px] px-3 gap-2">
          <div className="flex items-center min-w-0">{leading}</div>
          <span className="phone-bar-title text-center text-[17px] font-semibold tracking-tight truncate" aria-hidden={!collapsed}>{title}</span>
          <div className="flex items-center justify-end min-w-0">
            {actions && <div className="glass-group glass">{actions}</div>}
          </div>
        </div>
      </div>
      <h1 ref={big} className="m-0 px-5 pt-0.5 pb-3 text-[34px] leading-[1.12] font-bold tracking-[-0.025em]">{title}</h1>
      {children}
    </>
  )
}

/** The back button of a pushed phone page: a glass circle with a chevron. A left-edge swipe presses it. */
export function BackButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button className="glass-btn glass" aria-label={label} data-back onClick={onClick}>
      <Icon name="chevron" size={20} className="rotate-90" stroke={2.4} />
    </button>
  )
}
