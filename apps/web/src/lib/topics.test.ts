import { describe, expect, it } from 'vitest'
import { applyEvent, emptyState } from './reduce'
import { messagesOf, taskSummary, topicCards, topicOfEvent, unread, waitingIn } from './topics'
import type { Approval, HubEvent, Message, Task, Topic } from './types'

const ev = (seq: number, type: string, payload: any, extra: Partial<HubEvent> = {}): HubEvent => ({
  seq, id: `e${seq}-${type}`, ts: seq * 1000, type, actor: 'x', thread_id: 't1', payload, ...extra,
})

const topic = (id: string, extra: Partial<Topic> = {}): Topic => ({
  id, thread_id: 't1', title: id, summary: '', status: 'open', first_seq: 1, created_at: 1, updated_at: 1, count: 1, tasks: [], ...extra,
})

const task = (id: string, status: Task['status'], status_reason = ''): Task => ({ id, title: id, status, status_reason } as Task)

describe('topics in the reducer', () => {
  it('puts live messages into topics as the Hub does', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'message.user', { message_id: 'a', content: '修登录' }))
    applyEvent(s, ev(2, 'message.assistant', { message_id: 'r', content: '好', reply_to: 1, topic_id: 'a' }))
    applyEvent(s, ev(3, 'message.assistant', { message_id: 'plain', content: '旧的回复' }))
    applyEvent(s, ev(4, 'message.assistant', { message_id: 'rep', content: '汇报', kind: 'report' }, { task_id: 'k' }))
    applyEvent(s, ev(5, 'trigger.fired', { message_id: 'tr', content: '【任务进展】', topic_id: 'rep' }, { task_id: 'k' }))
    const byId = Object.fromEntries(s.messages.t1.map((m) => [m.id, m]))
    expect(byId.a.topic_id).toBe('a')
    expect(byId.r.topic_id).toBe('a')
    expect(byId.plain.topic_id).toBeUndefined()
    expect(byId.rep.topic_id).toBe('rep')
    expect(byId.rep.task_id).toBe('k')
    expect(byId.tr.topic_id).toBe('rep')
    expect(s.topicsRev).toBeGreaterThan(0)
  })

  it('tracks the topic being answered, seen messages and moves', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'message.user', { message_id: 'a', content: 'x' }))
    applyEvent(s, ev(2, 'message.user', { message_id: 'b', content: 'y' }))
    applyEvent(s, ev(0, 'message.seen', { message_ids: ['a'] }))
    expect(s.answering.t1).toBe('a')
    expect(s.messages.t1[0].seen).toBe(true)
    applyEvent(s, ev(3, 'message.moved', { message_id: 'b', topic_id: 'a', from: 'b' }))
    expect(s.messages.t1[1].topic_id).toBe('a')
    expect(s.moved.b).toBe('b')
    applyEvent(s, ev(4, 'message.assistant', { message_id: 'r', content: '都好了', reply_to: 2, topic_id: 'a' }))
    expect(s.answering.t1).toBeUndefined()
    applyEvent(s, ev(5, 'message.moved', { message_id: 'b', topic_id: 'b', from: 'a' }))
    expect(s.moved.b).toBeUndefined()
  })
})

describe('topic helpers', () => {
  const msgs: Message[] = [
    { id: 'a', thread_id: 't1', role: 'user', content: 'x', seq: 1, created_at: 1, topic_id: 'a' },
    { id: 'r', thread_id: 't1', role: 'assistant', content: '好', seq: 2, created_at: 2, topic_id: 'a', reply_to: 1 },
    { id: 'c', thread_id: 't1', role: 'user', content: '还有', seq: 3, created_at: 3, topic_id: 'a' },
    { id: 'b', thread_id: 't1', role: 'user', content: 'y', seq: 4, created_at: 4, topic_id: 'b' },
    { id: 'd', thread_id: 't1', role: 'assistant', content: '…', seq: Number.MAX_SAFE_INTEGER, created_at: 5, streaming: true },
  ]

  it('finds what waits in a topic, and where the streaming reply goes', () => {
    expect(waitingIn(msgs, 'a').map((m) => m.id)).toEqual(['c'])
    expect(waitingIn(msgs, 'b').map((m) => m.id)).toEqual(['b'])
    expect(messagesOf(msgs, 'a', 'a').map((m) => m.id)).toEqual(['a', 'r', 'c', 'd'])
    expect(messagesOf(msgs, 'b', 'a').map((m) => m.id)).toEqual(['b'])
  })

  it('starts topics like the Hub', () => {
    expect(topicOfEvent('user', undefined, 'm', undefined)).toBe('m')
    expect(topicOfEvent('assistant', undefined, 'm', undefined)).toBeUndefined()
    expect(topicOfEvent('assistant', 'notice', 'm', undefined)).toBe('m')
    expect(topicOfEvent('assistant', 'step', 'm', 't')).toBe('t')
  })

  it('groups cards: what needs you, open, closed', () => {
    const approvals = [{ id: 'ap', task_id: 'k2', review: 'user', status: 'pending' } as Approval]
    const cards = topicCards(
      [
        topic('closed', { status: 'closed', updated_at: 9 }),
        topic('old', { updated_at: 1 }),
        topic('new', { updated_at: 5 }),
        topic('blocked', { tasks: [task('k1', 'blocked')], updated_at: 2 }),
        topic('asking', { tasks: [task('k2', 'running')], updated_at: 3 }),
        topic('empty', { count: 0 }),
      ],
      [],
      approvals,
      undefined,
    )
    expect(cards.map((c) => `${c.group}:${c.topic.id}`)).toEqual([
      'attention:asking', 'attention:blocked', 'active:new', 'active:old', 'closed:closed',
    ])
  })

  it('keeps a closed topic the supervisor is on among the open ones', () => {
    const cards = topicCards([topic('a', { status: 'closed' })], [], [], 'a')
    expect(cards[0].group).toBe('active')
    expect(cards[0].busy).toBe(true)
  })

  it('marks topics with news from sous as unread', () => {
    const t = topic('a', { last: msgs[1] })
    expect(unread(t, [], {})).toBe(true)
    expect(unread(t, [], { a: 2 })).toBe(false)
    expect(unread(topic('b', { last: msgs[3] }), [], {})).toBe(false)
  })

  it('sums up a topic\'s tasks', () => {
    expect(taskSummary([task('a', 'running'), task('b', 'blocked')])).toBe('2 个任务 · 1 个要你处理 · 1 个进行中')
    expect(taskSummary([task('a', 'done')])).toBe('1 个任务 · 都已结束')
    expect(taskSummary([])).toBe('')
  })
})
