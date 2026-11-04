import { useEffect, useRef, type ReactNode } from 'react'

// The sheets currently mounted, oldest first. Escape only closes the top one,
// so a nested sheet does not take its parent down with it.
const sheets: symbol[] = []

export default function Sheet({ title, onClose, wide = false, head, children }: {
  title: string
  onClose: () => void
  wide?: boolean
  /** Drawn instead of the plain title (the title stays the dialog's label). */
  head?: ReactNode
  children: ReactNode
}) {
  const me = useRef(Symbol('sheet')).current

  useEffect(() => {
    const opener = typeof document !== 'undefined' ? (document.activeElement as HTMLElement | null) : null
    sheets.push(me)
    return () => {
      const i = sheets.lastIndexOf(me)
      if (i >= 0) sheets.splice(i, 1)
      if (opener && opener.isConnected && typeof opener.focus === 'function') requestAnimationFrame(() => opener.focus())
    }
  }, [me])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && sheets[sheets.length - 1] === me) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [me, onClose])

  return (
    <>
      <div className="backdrop" role="presentation" onClick={onClose}></div>
      <div className={`sheet ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
        <header className="flex items-center gap-3 mb-3 min-w-0">
          <div className="flex-1 min-w-0">{head ?? <h2 className="m-0 text-[18px] font-bold tracking-tight">{title}</h2>}</div>
          <button className="icon-btn bg-sunken rounded-full w-8 h-8 self-start" aria-label="关闭" onClick={onClose}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </header>
        <div className="overflow-y-auto grid gap-3.5 content-start min-h-0 pb-1 -mx-1 px-1">{children}</div>
      </div>
    </>
  )
}
