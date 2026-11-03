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

describe('runs', () => {
  const run = (seq: number, type: string, payload: any) => ({ ...ev(seq, type, payload), run_id: 'r1' })

  it('follows a run from queued to ended with tools, output and approvals', () => {
    const s = emptyState()
    applyEvent(s, run(1, 'run.queued', { node_id: 'n', agent: 'claude', kind: 'code', repo_path: '/r', prompt: '改一下' }))
    applyEvent(s, run(2, 'run.started', { workdir: '/w', branch: 'sous/abc' }))
    applyEvent(s, run(3, 'run.update', { kind: 'tool', tool_call_id: 't1', title: 'Edit a.go', status: 'pending', tool_kind: 'edit' }))
    applyEvent(s, run(0, 'run.output', { kind: 'text', text: '正在' }))
    applyEvent(s, run(0, 'run.output', { kind: 'text', text: '修改' }))
    applyEvent(s, run(4, 'approval.requested', { approval_id: 'a1', title: 'Edit a.go', options: [{ option_id: 'ok', name: 'Allow', kind: 'allow_once' }] }))
    applyEvent(s, run(5, 'approval.decided', { approval_id: 'a1', option_id: 'ok' }))
    applyEvent(s, run(6, 'run.update', { kind: 'tool', tool_call_id: 't1', status: 'completed' }))
    applyEvent(s, run(7, 'run.ended', { status: 'succeeded', summary: '改好了', diffstat: '1 file changed' }))
    const v = s.runs.r1
    expect(v.run).toMatchObject({ status: 'succeeded', branch: 'sous/abc', summary: '改好了', thread_id: 't1' })
    expect(v.tools).toEqual([{ id: 't1', title: 'Edit a.go', status: 'completed', kind: 'edit' }])
    expect(v.output).toBe('正在修改')
    expect(v.approvals[0]).toMatchObject({ status: 'decided', option_id: 'ok' })
    expect(v.seq).toBe(1)
  })

  it('expires pending approvals when the run ends', () => {
    const s = emptyState()
    applyEvent(s, run(1, 'run.queued', { node_id: 'n', agent: 'x', kind: 'general', prompt: 'p' }))
    applyEvent(s, run(2, 'approval.requested', { approval_id: 'a1', title: 't', options: [] }))
    applyEvent(s, run(3, 'run.ended', { status: 'interrupted', error: '重启了' }))
    expect(s.runs.r1.approvals[0].status).toBe('expired')
  })

  it('bumps nodesRev on machine events', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'node.online', { node_id: 'n' }))
    applyEvent(s, ev(2, 'node.inventory', { node_id: 'n' }))
    expect(s.nodesRev).toBe(2)
  })
})

describe('run status line', () => {
  it('clears once the agent reports progress', () => {
    const s = emptyState()
    const run = (seq: number, type: string, payload: any) => ({ ...ev(seq, type, payload), run_id: 'r1' })
    applyEvent(s, run(1, 'run.queued', { node_id: 'n', agent: 'x', kind: 'general', prompt: 'p' }))
    applyEvent(s, run(2, 'run.started', { workdir: '/w' }))
    applyEvent(s, run(3, 'run.update', { kind: 'status', text: '正在启动' }))
    expect(s.runs.r1.statusLine).toBe('正在启动')
    applyEvent(s, run(4, 'run.update', { kind: 'tool', tool_call_id: 't', title: 'Read', status: 'pending' }))
    expect(s.runs.r1.statusLine).toBe('')
  })
})
