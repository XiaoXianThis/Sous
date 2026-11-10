// shadcn/ui's popover (https://ui.shadcn.com/docs/components/popover), hand-copied and
// restyled with the project's own tokens: the raised panel, its 12px corners and
// shadow-pop. Radix does the placement: below the anchor by default, flipping
// above when there is no room, sliding sideways to stay 8px off the screen edges,
// and never taller than the room it has (the panel scrolls inside instead).
import * as PopoverPrimitive from '@radix-ui/react-popover'
import type { ComponentProps } from 'react'
import { cn } from '../../lib/utils'

export const Popover = PopoverPrimitive.Root
export const PopoverTrigger = PopoverPrimitive.Trigger
export const PopoverAnchor = PopoverPrimitive.Anchor
export const PopoverClose = PopoverPrimitive.Close

/** How far a floating panel stays from the screen edges. */
export const EDGE_GAP = 8

/** The look of every floating panel (popover, menu): raised, rounded, shadow-pop, above dialogs. */
export const floating = 'z-[60] bg-raised rounded-[12px] shadow-pop outline-none rise'

export function PopoverContent({
  className,
  side = 'bottom',
  align = 'start',
  sideOffset = 6,
  collisionPadding = EDGE_GAP,
  portal = true,
  ...props
}: ComponentProps<typeof PopoverPrimitive.Content> & {
  /** Draw in <body> (the default), out of any clipped or transformed ancestor. */
  portal?: boolean
}) {
  const content = (
    <PopoverPrimitive.Content
      side={side}
      align={align}
      sideOffset={sideOffset}
      collisionPadding={collisionPadding}
      avoidCollisions
      className={cn(
        floating,
        // Never wider than the screen, never taller than the room on its side: it scrolls inside.
        'max-w-[calc(100vw-16px)] max-h-[var(--radix-popover-content-available-height)] overflow-y-auto overscroll-contain',
        className,
      )}
      {...props}
    />
  )
  return portal ? <PopoverPrimitive.Portal>{content}</PopoverPrimitive.Portal> : content
}
