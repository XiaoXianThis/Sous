import { describe, expect, it } from 'vitest'
import { applyEvent, emptyState, isAnswering, mergeHistory } from './reduce'
import type { HubEvent } from './types'

const ev = (seq: number, type: string, payload: any, thread_id = 't1'): HubEvent => ({
  seq, id: `e${seq}-${type}`, ts: seq * 1000, type, actor: 'x', thread_id, payload,
})

describe('applyEvent', () => {
  it('adds threads with the main thread first', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'thread.created', { title: '新对话', kind: 'chat' }, 'c1'))
    applyEvent(s, ev(2, 'thread.created', { title: '主线', kind: 'main' }, 'm1'))
    expect(s.threads.map((t) => t.id)).toEqual(['m1', 'c1'])
    expect(s.lastSeq).toBe(2)
  })

  it('ignores stored events it already applied', () => {
    const s = emptyState()
    expect(applyEvent(s, ev(5, 'message.user', { message_id: 'u1', content: 'a' }))).toBe(true)
    expect(applyEvent(s, ev(5, 'message.user', { message_id: 'u1', content: 'a' }))).toBe(false)
    expect(applyEvent(s, ev(3, 'message.user', { message_id: 'old', content: 'x' }))).toBe(false)
    expect(s.messages.t1).toHaveLength(1)
  })

  it('streams deltas into a draft and replaces it with the final message', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'message.user', { message_id: 'u1', content: '你好' }))
    applyEvent(s, ev(0, 'message.assistant.delta', { message_id: 'a1', delta: '想一下', thinking: true }))
    applyEvent(s, ev(0, 'message.assistant.delta', { message_id: 'a1', delta: '你' }))
    applyEvent(s, ev(0, 'message.assistant.delta', { message_id: 'a1', delta: '好' }))
    let draft = s.messages.t1[1]
    expect(draft).toMatchObject({ content: '你好', thinking: '想一下', streaming: true })
    expect(isAnswering(s, 't1')).toBe(true)

    applyEvent(s, ev(2, 'message.assistant', { message_id: 'a1', content: '你好！', reply_to: 1 }))
    draft = s.messages.t1[1]
    expect(draft).toMatchObject({ content: '你好！', streaming: false, seq: 2, thinking: '想一下' })
    expect(isAnswering(s, 't1')).toBe(false)
    // A late delta must not change a finished message.
    applyEvent(s, ev(0, 'message.assistant.delta', { message_id: 'a1', delta: 'x' }))
    expect(s.messages.t1[1].content).toBe('你好！')
  })

  it('knows a reply is pending before the first delta arrives', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'message.user', { message_id: 'u1', content: 'a' }))
    expect(isAnswering(s, 't1')).toBe(true)
  })

  it('merges REST history without duplicates', () => {
    const s = emptyState()
    applyEvent(s, ev(4, 'message.user', { message_id: 'u2', content: 'b' }))
    mergeHistory(s, 't1', [
      { id: 'u1', thread_id: 't1', role: 'user', content: 'a', seq: 1, created_at: 1 },
      { id: 'u2', thread_id: 't1', role: 'user', content: 'b', seq: 4, created_at: 4 },
    ])
    expect(s.messages.t1.map((m) => m.id)).toEqual(['u1', 'u2'])
  })
})

describe('kinds and memories', () => {
  it('keeps the kind of notices and silent turns', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'message.user', { message_id: 'u1', content: '谢谢' }))
    applyEvent(s, ev(2, 'message.assistant', { message_id: 'a1', content: '用户只是道谢', kind: 'silent', reply_to: 1 }))
    applyEvent(s, ev(3, 'message.assistant', { message_id: 'n1', content: '构建失败', kind: 'notice' }))
    expect(s.messages.t1.map((m) => m.kind)).toEqual([undefined, 'silent', 'notice'])
    expect(isAnswering(s, 't1')).toBe(false)
  })

  it('bumps memoryRev on memory events', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'memory.put', { memory_id: 'm', kind: 'note', content: 'x' }))
    applyEvent(s, ev(2, 'memory.deleted', { memory_id: 'm' }))
    expect(s.memoryRev).toBe(2)
  })
})
