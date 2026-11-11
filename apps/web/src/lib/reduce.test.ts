import { describe, expect, it } from 'vitest'
import { applyEvent, emptyState, isAnswering, mergeHistory } from './reduce'
import type { HubEvent } from './types'

const ev = (seq: number, type: string, payload: any, thread_id = 't1'): HubEvent => ({
  seq, id: `e${seq}-${type}`, ts: seq * 1000, type, actor: 'x', thread_id, payload,
})

describe('applyEvent', () => {
  it('refreshes notes for note events', () => { const s = emptyState(); ['note.put', 'note.sent', 'note.deleted'].forEach((t, i) => applyEvent(s, ev(i + 1, t, {}))); expect(s.notesRev).toBe(3) })
  it('keeps a meeting one topic and tracks its status', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'thread.created', { title: '要不要换框架', kind: 'meeting', parent_thread_id: 'm1', meeting_topic: 'open1', host: { model: 'x' } }, 'mt'))
    const t = s.threads.find((x) => x.id === 'mt')!
    expect(t.meeting_topic).toBe('open1')
    expect(t.meeting_status).toBe('open')
    expect(t.parent_thread_id).toBe('m1')
    applyEvent(s, ev(2, 'message.user', { message_id: 'u1', content: '先说目标' }, 'mt'))
    expect(s.messages.mt[0].topic_id).toBe('open1')
    applyEvent(s, ev(3, 'meeting.closed', { meeting_id: 'mt' }, 'mt'))
    expect(s.threads.find((x) => x.id === 'mt')!.meeting_status).toBe('closed')
    applyEvent(s, ev(4, 'minutes.drafted', { meeting_id: 'mt', version: 1 }, 'mt'))
    expect(s.meetingsRev).toBe(2)
  })
  it('adds threads with the main thread first', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'thread.created', { title: '新对话', kind: 'chat' }, 'c1'))
    applyEvent(s, ev(2, 'thread.created', { title: '主线', kind: 'main' }, 'm1'))
    expect(s.threads.map((t) => t.id)).toEqual(['m1', 'c1'])
    expect(s.lastSeq).toBe(2)
  })

  it('keeps the project of a project chat', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'thread.created', { title: 'blog 的对话', kind: 'project', project_id: 'p1' }, 'pc'))
    applyEvent(s, ev(2, 'thread.created', { title: '新对话', kind: 'chat' }, 'c1'))
    expect(s.threads.find((t) => t.id === 'pc')?.project_id).toBe('p1')
    expect(s.threads.find((t) => t.id === 'c1')?.project_id).toBeUndefined()
  })

  it('keeps the files of a user message', () => {
    const s = emptyState()
    const a = { id: 'A', name: 'x.png', mime: 'image/png', size: 3 }
    applyEvent(s, ev(1, 'message.user', { message_id: 'u1', content: '', attachments: [a] }))
    applyEvent(s, ev(2, 'message.user', { message_id: 'u2', content: 'hi' }))
    expect(s.messages.t1[0].attachments).toEqual([a])
    expect(s.messages.t1[1].attachments).toBeUndefined()
  })

  it('keeps the context and @ references of a user message', () => {
    const s = emptyState()
    const context = { project_id: 'p1', mentions: [{ project_id: 'p2', name: 'blog' }] }
    applyEvent(s, ev(1, 'message.user', { message_id: 'u1', content: '看看 @blog ', context }))
    expect(s.messages.t1[0].context).toEqual(context)
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

  it('bumps runsRev for lifecycle events of runs it has not loaded', () => {
    const s = emptyState()
    applyEvent(s, run(1, 'run.started', { workdir: '/w' }))
    applyEvent(s, run(2, 'run.paused', { reason: '用户暂停' }))
    applyEvent(s, run(3, 'run.resumed', {}))
    applyEvent(s, run(4, 'run.ended', { status: 'succeeded' }))
    expect(s.runsRev).toBe(4)
    applyEvent(s, run(0, 'run.output', { kind: 'text', text: 'x' }))
    expect(s.runsRev).toBe(4)
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

describe('task and approval events', () => {
  it('bumps task and inbox revisions', () => {
    const s = emptyState()
    applyEvent(s, { ...ev(1, 'task.created', { title: 'x' }), task_id: 'k1' })
    applyEvent(s, { ...ev(2, 'task.status', { from: 'planned', to: 'queued' }), task_id: 'k1' })
    expect(s.tasksRev).toBe(2)
    expect(s.inboxRev).toBe(1)
  })

  it('tracks task runs, policy decisions and escalations', () => {
    const s = emptyState()
    applyEvent(s, { ...ev(1, 'run.queued', { node_id: 'n', agent: 'sous', kind: 'code', purpose: 'deliver', title: 'Add x', permission_mode: 'auto' }), run_id: 'r1', task_id: 'k1' })
    const v = s.runs.r1
    expect(v.run).toMatchObject({ purpose: 'deliver', task_id: 'k1', title: 'Add x', permission_mode: 'auto' })
    applyEvent(s, { ...ev(2, 'run.update', { kind: 'permission', title: 'rm -rf ~', category: 'floor', decision: 'deny' }), run_id: 'r1' })
    expect(v.tools[0]).toMatchObject({ status: 'failed', title: '已拒绝：rm -rf ~' })
    applyEvent(s, { ...ev(3, 'approval.requested', { approval_id: 'a1', title: 'push', category: 'git-remote', review: 'ai', options: [] }), run_id: 'r1', task_id: 'k1' })
    expect(v.approvals[0]).toMatchObject({ review: 'ai', category: 'git-remote' })
    expect(s.inboxRev).toBe(1)
    applyEvent(s, { ...ev(4, 'approval.escalated', { approval_id: 'a1', note: '拿不准' }), run_id: 'r1' })
    expect(v.approvals[0]).toMatchObject({ review: 'user', escalation: '拿不准' })
    applyEvent(s, { ...ev(5, 'run.ended', { status: 'succeeded', pr_url: 'https://x/pull/1' }), run_id: 'r1' })
    expect(v.run.pr_url).toBe('https://x/pull/1')
    expect(v.approvals[0].status).toBe('expired')
    expect(s.inboxRev).toBe(3) // the badge reloads when a run's approvals expire
  })
})

describe('consult messages', () => {
  it('keeps who answered', () => {
    const s = emptyState()
    applyEvent(s, {
      seq: 3, id: 'e3', ts: 1, type: 'message.assistant', actor: 'system', thread_id: 't', run_id: 'r1',
      payload: { message_id: 'c1', content: '登录在 auth.go', kind: 'consult', reply_to: 2, source: { run_id: 'r1', agent: 'claude', project_id: 'p', project: 'Sous' } },
    })
    const m = s.messages.t.find((x) => x.id === 'c1')!
    expect(m.kind).toBe('consult')
    expect(m.source).toMatchObject({ agent: 'claude', project: 'Sous' })
  })
})

describe('supervisor tool calls', () => {
  it('keeps them from the message, message.tools and a later history page', () => {
    const s = emptyState()
    const one = { name: 'task_list', args: 'status=running' }
    const two = { name: 'machine_look', args: 'command=ls', error: '机器离线' }
    applyEvent(s, ev(1, 'message.user', { message_id: 'u1', content: '查查' }))
    applyEvent(s, ev(2, 'message.assistant', { message_id: 's1', content: '先看看', kind: 'step', reply_to: 1, tools: [one] }))
    expect(s.messages.t1[1].tools).toEqual([one])
    applyEvent(s, ev(3, 'message.tools', { message_id: 's1', tools: [one, two] }))
    expect(s.messages.t1[1].tools).toEqual([one, two])
    // A page fetched before the event does not drop them.
    mergeHistory(s, 't1', [{ id: 's1', thread_id: 't1', role: 'assistant', content: '先看看', kind: 'step', seq: 2, created_at: 0 }])
    expect(s.messages.t1[1].tools).toEqual([one, two])
  })
})
