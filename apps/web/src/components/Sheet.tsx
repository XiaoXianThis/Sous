import { createContext, useContext, useEffect, useRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import Icon from './Icon'

// The sheets currently mounted, oldest first. Escape only closes the top one,
// so a nested sheet does not take its parent down with it.
const sheets: symbol[] = []

/**
 * Where a Sheet is drawn. Settings pages are embedded in a clipped, transformed
 * panel: the page's own root sheet is flattened into it ('root'), but a dialog
 * opened from there ('nested') would be clipped by that panel and sit under the
 * phone tab bar, so it is drawn in <body> instead. Elsewhere it stays in place.
 */
export type SheetPlace = 'inline' | 'root' | 'nested'
export const SheetPlaceContext = createContext<SheetPlace>('inline')

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

  const place = useContext(SheetPlaceContext)

  const body = (
    <>
      <div className="backdrop" role="presentation" onClick={onClose}></div>
      <div className={`sheet ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
        <header className="flex items-center gap-3 mb-3 min-w-0">
          <div className="flex-1 min-w-0">{head ?? <h2 className="m-0 text-[18px] font-bold tracking-tight">{title}</h2>}</div>
          <button className="icon-btn bg-sunken rounded-full w-8 h-8 self-start" aria-label="关闭" onClick={onClose}>
            <Icon name="x" size={16} />
          </button>
        </header>
        <div className="overflow-y-auto grid gap-3.5 content-start min-h-0 pb-1 -mx-1 px-1">{children}</div>
      </div>
    </>
  )

  return (
    <SheetPlaceContext.Provider value={place === 'root' ? 'nested' : place}>
      {place === 'nested' && typeof document !== 'undefined'
        ? createPortal(<div data-nested-sheet style={{ display: 'contents' }}>{body}</div>, document.body)
        : body}
    </SheetPlaceContext.Provider>
  )
}
