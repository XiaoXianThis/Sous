import { useEffect, useRef, type ReactNode } from 'react'

// The sheets currently mounted, oldest first. Escape only closes the top one,
// so a nested sheet does not take its parent down with it.
const sheets: symbol[] = []

export default function Sheet({ title, onClose, wide = false, children }: {
  title: string
  onClose: () => void
  wide?: boolean
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
        <header className="flex items-center justify-between mb-2">
          <h2 className="m-0 text-[18px]">{title}</h2>
          <button className="icon-btn" aria-label="关闭" onClick={onClose}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </header>
        <div className="overflow-y-auto grid gap-3 content-start min-h-0 pb-1">{children}</div>
      </div>
    </>
  )
}
