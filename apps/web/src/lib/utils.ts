import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// The project's own shadow names (app.css's @theme), so a caller's `shadow-*` replaces the default one.
const merge = extendTailwindMerge({ extend: { theme: { shadow: ['card', 'soft', 'pop'] } } })

/** Joins class names; a later Tailwind class wins over an earlier one of the same kind (shadcn/ui's cn). */
export function cn(...inputs: ClassValue[]): string {
  return merge(clsx(inputs))
}
