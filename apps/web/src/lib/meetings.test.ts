import { describe, expect, it } from 'vitest'
import { minuteRefs, renderRefs, supersededBy } from './meetings'
import type { Message } from './types'

function minutes(version: number, meeting = 'm1'): Message {
  return { id: `x${version}`, thread_id: 't', role: 'assistant', kind: 'minutes', content: '', seq: version, created_at: 0, source: { run_id: '', agent: '', project_id: '', meeting_id: meeting, version } }
}

describe('meetings', () => {
  it('reads the citations of a minutes text', () => {
    expect(minuteRefs('结论 [#12][#15]\n行动项 [#3]')).toEqual([12, 15, 3])
    expect(minuteRefs('没有出处')).toEqual([])
  })

  it('links citations for the chat to intercept', () => {
    expect(renderRefs('见 [#12] 和 [#7](#seq-7)')).toBe('见 [#12](#seq-12) 和 [#7](#seq-7)')
  })

  it('finds the later version that replaced a minutes message', () => {
    const old = minutes(1)
    expect(supersededBy([old, minutes(2), minutes(3)], old)).toBe(3)
    expect(supersededBy([old], old)).toBeNull()
    expect(supersededBy([old, minutes(2, 'other')], old)).toBeNull()
    expect(supersededBy([old, { ...minutes(2), kind: 'consult' }], old)).toBeNull()
  })
})
