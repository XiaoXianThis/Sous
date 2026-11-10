// shadcn/ui's dialog (https://ui.shadcn.com/docs/components/dialog), hand-copied
// and adapted. Radix handles the behavior: a stack of layers (Escape and outside
// clicks only reach the topmost one), the focus trap, scroll locking, and drawing
// in <body> so no clipped or transformed ancestor cuts the dialog off.
//
// The look stays the caller's: the dimmed `.backdrop` and whatever panel classes
// it passes (`.sheet`, the project dialog's own box…). Two differences from
// shadcn's copy:
// - dialogs here are mounted while open and unmounted to close (often after a
//   Motion animation), with no Radix trigger, so the element that had focus when
//   one mounted gets it back when it goes;
// - the backdrop is a sibling right before the panel and closes it on click,
//   which the phone's drag-to-close (lib/ios.ts) relies on.
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { createContext, useContext, useState, type ComponentProps, type CSSProperties, type Ref } from 'react'
import { cn } from '../../lib/utils'

const CloseContext = createContext<() => void>(() => {})

export function Dialog({ onOpenChange, ...props }: ComponentProps<typeof DialogPrimitive.Root>) {
  return (
    <CloseContext.Provider value={() => onOpenChange?.(false)}>
      <DialogPrimitive.Root onOpenChange={onOpenChange} {...props} />
    </CloseContext.Provider>
  )
}

export const DialogTitle = DialogPrimitive.Title
export const DialogDescription = DialogPrimitive.Description
export const DialogClose = DialogPrimitive.Close

/** The element focused before this render, captured once (before Radix moves focus in). */
function useOpener(): HTMLElement | null {
  const [opener] = useState(() => (typeof document === 'undefined' ? null : (document.activeElement as HTMLElement | null)))
  return opener
}

export function DialogContent({
  className,
  children,
  overlay = 'backdrop',
  overlayRef,
  overlayStyle,
  dismissOnOverlay = true,
  portal = true,
  onOpenAutoFocus,
  onCloseAutoFocus,
  onPointerDownOutside,
  ...props
}: ComponentProps<typeof DialogPrimitive.Content> & {
  /** The backdrop's classes; false for none. */
  overlay?: string | false
  overlayRef?: Ref<HTMLDivElement>
  overlayStyle?: CSSProperties
  /** A click on the backdrop closes the dialog (settings turn this off). */
  dismissOnOverlay?: boolean
  /** Draw in <body> (the default). The phone's settings page stays in place, under the tab bar. */
  portal?: boolean
}) {
  const close = useContext(CloseContext)
  const opener = useOpener()
  // Two siblings, not a fragment: Radix's portal draws each child on its own.
  const body = [
    overlay !== false && (
      <DialogPrimitive.Overlay
        key="overlay"
        ref={overlayRef}
        className={overlay}
        style={overlayStyle}
        role="presentation"
        onClick={dismissOnOverlay ? close : undefined}
      />
    ),
    <DialogPrimitive.Content
      key="content"
      aria-modal="true"
      aria-describedby={undefined}
      className={cn('outline-none', className)}
      // Nothing inside is focused for the user (no ring on the first button); the panel takes focus so the trap holds.
      onOpenAutoFocus={(e) => {
        onOpenAutoFocus?.(e)
        if (e.defaultPrevented) return
        e.preventDefault()
        ;(e.currentTarget as HTMLElement | null)?.focus?.({ preventScroll: true })
      }}
      // Back to whatever opened it, if it is still there.
      onCloseAutoFocus={(e) => {
        onCloseAutoFocus?.(e)
        if (e.defaultPrevented) return
        e.preventDefault()
        if (opener?.isConnected && typeof opener.focus === 'function') opener.focus({ preventScroll: true })
      }}
      // Outside clicks land on the backdrop, which closes on click (or not, for settings).
      onPointerDownOutside={(e) => {
        onPointerDownOutside?.(e)
        e.preventDefault()
      }}
      {...props}
    >
      {children}
    </DialogPrimitive.Content>,
  ]
  // An explicit container draws it in the same commit (Radix otherwise waits a render),
  // so a caller's layout effect can measure and animate the panel right away.
  return portal && typeof document !== 'undefined'
    ? <DialogPrimitive.Portal container={document.body}>{body}</DialogPrimitive.Portal>
    : <>{body}</>
}
