// A thread's context window as the chat header shows it (long-main-thread).
import type { ThreadContext } from './types'

export interface ContextLook {
  /** Share of the window the next turn uses, 0..1. */
  share: number
  /** ok below the soft threshold, near until the hard one, full above. */
  level: 'ok' | 'near' | 'full'
  /** Share of recent input served from the prompt cache, or null without calls. */
  cacheRate: number | null
}

export function contextLook(c: ThreadContext): ContextLook {
  const share = c.window > 0 ? Math.min(1, c.used / c.window) : 0
  const level = c.used >= c.hard ? 'full' : c.used >= c.soft ? 'near' : 'ok'
  const cacheRate = c.cache.calls > 0 && c.cache.input_tokens > 0 ? c.cache.cached_tokens / c.cache.input_tokens : null
  return { share, level, cacheRate }
}

/** Tokens as a short number: 950, 12.3k, 128k, 1.2M. */
export function tokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) {
    const k = n / 1000
    return (k < 100 ? k.toFixed(1).replace(/\.0$/, '') : Math.round(k)) + 'k'
  }
  return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
}

export const reasonText: Record<string, string> = {
  start: '开始',
  soft: '自动整理',
  hard: '自动整理（快满了）',
  overflow: '超长后整理',
  manual: '手动整理',
  edit: '手动修改',
}

/**
 * Index of the first timeline item after the folded messages, where the
 * "folded into the summary" divider goes; -1 when no shown item is folded or
 * every one is.
 */
export function dividerAt(seqs: number[], coversUpTo: number): number {
  if (coversUpTo <= 0 || seqs.length === 0 || seqs[0] > coversUpTo) return -1
  const i = seqs.findIndex((s) => s > coversUpTo)
  return i
}
