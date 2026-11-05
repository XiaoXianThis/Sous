import { useEffect, useRef, useState, type ReactNode } from 'react'

/**
 * The top of a phone page, after iOS: a translucent bar that stays at the top
 * with trailing actions, and a large title under it. Once the large title
 * scrolls away the bar turns solid and shows the title small in its middle.
 * Lives inside the page's scroll container, at its very top.
 */
export default function PhoneHeader({ title, leading, actions, children }: {
  title: string
  leading?: ReactNode
  actions?: ReactNode
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
      <div ref={bar} className={`phone-bar ${collapsed ? 'is-collapsed' : ''}`}>
        <div className="flex items-center h-11 px-2 gap-1">
          <div className="flex items-center min-w-[72px]">{leading}</div>
          <span className="phone-bar-title flex-1 text-center text-[16px] font-semibold tracking-tight truncate" aria-hidden={!collapsed}>{title}</span>
          <div className="flex items-center justify-end min-w-[72px]">{actions}</div>
        </div>
      </div>
      <h1 ref={big} className="m-0 px-4 pt-0.5 pb-2 text-[32px] leading-[1.15] font-bold tracking-[-0.02em]">{title}</h1>
      {children}
    </>
  )
}
