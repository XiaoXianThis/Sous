import { createContext, useContext, type ReactNode } from 'react'
import Icon from './Icon'
import { Dialog, DialogClose, DialogContent, DialogTitle } from './ui/dialog'

/**
 * Whether a Sheet is laid flat into the page instead of opening as a dialog.
 * A settings page (and the task form inside a task view) shows a component that
 * is a dialog elsewhere as its own content: `.embed > .backdrop:first-child + .sheet`
 * in app.css flattens it. Sheets opened from there are dialogs again, drawn in
 * <body> by Radix, so no clipped panel cuts them off and, on a phone, they sit
 * above the tab bar. Components with a root of their own (Machines, Memories)
 * set this back to false for what they open.
 */
export const SheetPageContext = createContext(false)

/**
 * A dialog: a centered card on a desktop, a bottom sheet on a phone (`.sheet`).
 * Mounted while open; Escape and the backdrop close only the topmost one (Radix's
 * layer stack), and focus goes back to what opened it.
 */
export default function Sheet({ title, onClose, wide = false, head, children }: {
  title: string
  onClose: () => void
  wide?: boolean
  /** Drawn instead of the plain title (the title stays the dialog's label). */
  head?: ReactNode
  children: ReactNode
}) {
  const page = useContext(SheetPageContext)
  const body = <div className="overflow-y-auto overscroll-contain grid gap-3.5 content-start min-h-0 pb-1 -mx-1 px-1">{children}</div>

  if (page) {
    return (
      <SheetPageContext.Provider value={false}>
        <div className="backdrop" role="presentation"></div>
        <section className={`sheet ${wide ? 'wide' : ''}`} aria-label={title}>
          <header className="flex items-center gap-3 mb-3 min-w-0">
            <div className="flex-1 min-w-0">{head ?? <h2 className="m-0 text-[18px] font-bold tracking-tight">{title}</h2>}</div>
          </header>
          {body}
        </section>
      </SheetPageContext.Provider>
    )
  }

  return (
    <SheetPageContext.Provider value={false}>
      <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
        <DialogContent className={`sheet ${wide ? 'wide' : ''}`} aria-label={title}>
          <header className="flex items-center gap-3 mb-3 min-w-0">
            <div className="flex-1 min-w-0">{head ?? <DialogTitle className="m-0 text-[18px] font-bold tracking-tight">{title}</DialogTitle>}</div>
            <DialogClose className="icon-btn bg-sunken rounded-full w-8 h-8 self-start" aria-label="关闭">
              <Icon name="x" size={16} />
            </DialogClose>
          </header>
          {body}
        </DialogContent>
      </Dialog>
    </SheetPageContext.Provider>
  )
}
