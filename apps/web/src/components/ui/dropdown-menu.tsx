// shadcn/ui's dropdown-menu (https://ui.shadcn.com/docs/components/dropdown-menu),
// hand-copied and restyled with the project's tokens. Radix places it like the
// popover (flips, slides, keeps 8px off the edges), adds arrow-key navigation,
// and locks the page's scroll while it is open.
import * as Menu from '@radix-ui/react-dropdown-menu'
import type { ComponentProps } from 'react'
import { cn } from '../../lib/utils'
import { EDGE_GAP, floating } from './popover'

export const DropdownMenu = Menu.Root
export const DropdownMenuTrigger = Menu.Trigger
export const DropdownMenuGroup = Menu.Group
export const DropdownMenuSub = Menu.Sub
export const DropdownMenuRadioGroup = Menu.RadioGroup
export const DropdownMenuItemIndicator = Menu.ItemIndicator

const item = 'flex items-center gap-2 h-8 px-2.5 rounded-[9px] text-[13px] text-ink outline-none cursor-default select-none data-[highlighted]:bg-hover data-[state=open]:bg-hover data-[disabled]:opacity-50'

export function DropdownMenuContent({ className, sideOffset = 4, collisionPadding = EDGE_GAP, ...props }: ComponentProps<typeof Menu.Content>) {
  return (
    <Menu.Portal>
      <Menu.Content
        sideOffset={sideOffset}
        collisionPadding={collisionPadding}
        avoidCollisions
        className={cn(floating, 'p-1.5 max-w-[calc(100vw-16px)] max-h-[var(--radix-dropdown-menu-content-available-height)] overflow-y-auto overscroll-contain', className)}
        {...props}
      />
    </Menu.Portal>
  )
}

export function DropdownMenuSubContent({ className, sideOffset = 4, collisionPadding = EDGE_GAP, ...props }: ComponentProps<typeof Menu.SubContent>) {
  return (
    <Menu.Portal>
      <Menu.SubContent
        sideOffset={sideOffset}
        collisionPadding={collisionPadding}
        avoidCollisions
        className={cn(floating, 'p-1.5 max-w-[calc(100vw-16px)] max-h-[var(--radix-dropdown-menu-content-available-height)] overflow-y-auto overscroll-contain', className)}
        {...props}
      />
    </Menu.Portal>
  )
}

export function DropdownMenuItem({ className, ...props }: ComponentProps<typeof Menu.Item>) {
  return <Menu.Item className={cn(item, className)} {...props} />
}

export function DropdownMenuSubTrigger({ className, ...props }: ComponentProps<typeof Menu.SubTrigger>) {
  return <Menu.SubTrigger className={cn(item, className)} {...props} />
}

export function DropdownMenuRadioItem({ className, ...props }: ComponentProps<typeof Menu.RadioItem>) {
  return <Menu.RadioItem className={cn(item, className)} {...props} />
}

export function DropdownMenuLabel({ className, ...props }: ComponentProps<typeof Menu.Label>) {
  return <Menu.Label className={cn('px-2.5 pt-1.5 pb-0.5 text-[11px] text-faint', className)} {...props} />
}
