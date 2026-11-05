import { describe, expect, it } from 'vitest'
import { contextLook, dividerAt, tokens } from './context'
import { applyEvent, emptyState } from './reduce'
import type { ThreadContext } from './types'

const base: ThreadContext = {
  segment: 2, covers_up_to: 40, summary: '', profile: [], used: 50_000, window: 100_000, soft: 60_000, hard: 85_000,
  cache: { calls: 4, input_tokens: 200_000, cached_tokens: 150_000, last_input: 50_000 },
}

describe('context', () => {
  it('rates usage against the thresholds', () => {
    expect(contextLook(base)).toEqual({ share: 0.5, level: 'ok', cacheRate: 0.75 })
    expect(contextLook({ ...base, used: 70_000 }).level).toBe('near')
    expect(contextLook({ ...base, used: 90_000 }).level).toBe('full')
    expect(contextLook({ ...base, cache: { calls: 0, input_tokens: 0, cached_tokens: 0, last_input: 0 } }).cacheRate).toBeNull()
  })

  it('formats tokens', () => {
    expect(tokens(950)).toBe('950')
    expect(tokens(12_345)).toBe('12.3k')
    expect(tokens(128_000)).toBe('128k')
    expect(tokens(1_200_000)).toBe('1.2M')
  })

  it('places the divider after the folded messages', () => {
    expect(dividerAt([10, 20, 41, 50], 40)).toBe(2)
    expect(dividerAt([41, 50], 40)).toBe(-1) // nothing folded is shown
    expect(dividerAt([10, 20], 40)).toBe(-1) // everything shown is folded
    expect(dividerAt([10, 20], 0)).toBe(-1)
  })

  it('bumps contextRev on thread.compacted', () => {
    const s = emptyState()
    applyEvent(s, { seq: 1, type: 'thread.compacted', thread_id: 't', ts: 1, payload: { segment: 2 } } as never)
    expect(s.contextRev).toBe(1)
  })
})
