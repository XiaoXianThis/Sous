import { describe, expect, it } from 'vitest'
import { brief, columnOf, DONE_MS, floorOf, focusOf, inFocus, moveOf, seatingOf, spotName } from './dispatch'
import type { Approval, HubEvent, Node, Overview, Run, Task } from './types'

const node = (id: string, extra: Partial<Node> = {}): Node => ({ id, name: id.toUpperCase(), online: true, concurrency: 2, ...extra }) as Node
const run = (id: string, extra: Partial<Run> = {}): Run => ({ id, node_id: 'a', agent: 'claude', status: 'running', task_id: '', thread_id: '', started_at: 1, created_at: 1, ...extra }) as Run
const task = (id: string, extra: Partial<Task> = {}): Task => ({ id, title: `任务${id}`, status: 'running', updated_at: 1000, dismissed: false, origin_thread_id: '', ...extra }) as Task
const approval = (id: string, extra: Partial<Approval> = {}): Approval => ({ id, run_id: 'r1', task_id: 't1', review: 'user', escalation: '', status: 'pending', options: [], ...extra }) as Approval
const data = (o: Partial<Overview> = {}): Overview => ({ projects: [], nodes: [], personas: [], tasks: [], runs: [], approvals: [], discovered: [], ...o })
const ev = (type: string, payload: unknown = {}, extra: Partial<HubEvent> = {}): HubEvent => ({ seq: 1, id: 'e1', ts: 5, type, actor: 'system', payload, ...extra })

describe('floor', () => {
  it('seats running runs per machine, queues the queued ones, online machines first', () => {
    const d = data({
      nodes: [node('b', { online: false }), node('a')],
      runs: [run('r1', { started_at: 2 }), run('r2', { started_at: 1 }), run('r3', { status: 'queued' }), run('r4', { status: 'succeeded' }), run('r5', { archived: true })],
    })
    const f = floorOf(d, 0)
    expect(f.shops.map((s) => s.node.id)).toEqual(['a', 'b'])
    expect(f.shops[0].seats.map((r) => r?.id ?? null)).toEqual(['r2', 'r1'])
    expect(f.shops[0].queue.map((r) => r.id)).toEqual(['r3'])
    expect(f.shops[1].seats).toEqual([null, null])
  })
  it('adds seats when more run than the machine allows', () => {
    const d = data({ nodes: [node('a', { concurrency: 1 })], runs: [run('r1'), run('r2')] })
    expect(floorOf(d, 0).shops[0].seats).toHaveLength(2)
  })
  it('keeps an agent in its seat while another leaves', () => {
    const d = data({ nodes: [node('a', { concurrency: 3 })], runs: [run('r1', { started_at: 1 }), run('r2', { started_at: 2 }), run('r3', { started_at: 3 })] })
    const seating = seatingOf(floorOf(d, 0))
    expect(seating.a).toEqual(['r1', 'r2', 'r3'])
    const after = floorOf({ ...d, runs: [d.runs[1], d.runs[2], run('r4', { started_at: 9 })] }, 0, seating)
    expect(after.shops[0].seats.map((r) => r?.id)).toEqual(['r4', 'r2', 'r3'])
  })
  it('sorts tasks onto the board and drops old finished ones', () => {
    const now = 10 * DONE_MS
    const d = data({
      tasks: [task('1', { status: 'inbox' }), task('2', { status: 'review' }), task('3', { status: 'done', updated_at: now - 1000 }), task('4', { status: 'done', updated_at: 0 }), task('5', { status: 'cancelled' }), task('6', { status: 'blocked', dismissed: true })],
    })
    const f = floorOf(d, now)
    expect(f.board.todo.map((t) => t.id)).toEqual(['1'])
    expect(f.board.review.map((t) => t.id)).toEqual(['2'])
    expect(f.board.done.map((t) => t.id)).toEqual(['3'])
    expect(f.board.blocked).toEqual([])
    expect(columnOf('waiting_approval')).toBe('approval')
    expect(columnOf('cancelled')).toBe('')
  })
  it('puts your requests on your desk and the approver its own', () => {
    const d = data({ approvals: [approval('1'), approval('2', { review: 'ai' }), approval('3', { review: 'ai', escalation: '风险高' }), approval('4', { status: 'decided' })] })
    const f = floorOf(d, 0)
    expect(f.mine.map((a) => a.id)).toEqual(['1', '3'])
    expect(f.theirs.map((a) => a.id)).toEqual(['2'])
  })
})

describe('moves', () => {
  const d = data({ nodes: [node('a')], runs: [run('r1', { task_id: 't1' })], tasks: [task('t1', { title: '限流方案' })], approvals: [approval('ap', { options: [{ option_id: 'o1', name: '允许', kind: 'allow_once' }, { option_id: 'o2', name: '拒绝', kind: 'reject_once' }] })] })
  it('carries your message to the supervisor and its reply back', () => {
    expect(moveOf(ev('message.user', { content: '帮我看看网关' }), d)).toMatchObject({ item: 'note', from: { kind: 'actor', actor: 'you' }, to: { kind: 'actor', actor: 'sup' }, text: '帮我看看网关' })
    expect(moveOf(ev('message.user', { kind: 'trigger' }), d)).toBeNull()
    expect(moveOf(ev('message.assistant', { content: '好的' }), d)).toMatchObject({ item: 'letter', to: { actor: 'you' } })
    expect(moveOf(ev('message.assistant', { kind: 'report' }, { task_id: 't1' }), d)?.text).toBe('汇报：限流方案')
    expect(moveOf(ev('message.assistant', { kind: 'step' }), d)).toBeNull()
  })
  it('turns tools into what the supervisor is doing, and ignores the rest', () => {
    expect(moveOf(ev('message.tools', { tools: [{ name: 'create_task' }, { name: 'web_search' }] }), d)).toMatchObject({ item: 'bubble', text: '上网查资料' })
    expect(moveOf(ev('message.tools', { tools: [{ name: 'create_task' }] }), d)).toBeNull()
  })
  it('puts new tickets on the board and slides them between columns', () => {
    expect(moveOf(ev('task.created', { title: '新功能', status: 'planned' }), d)).toMatchObject({ item: 'ticket', to: { kind: 'board', col: 'todo' } })
    expect(moveOf(ev('task.status', { from: 'running', to: 'review' }, { task_id: 't1' }), d)).toMatchObject({ from: { col: 'doing' }, to: { col: 'review' }, text: '限流方案' })
    expect(moveOf(ev('task.status', { from: 'inbox', to: 'planned' }), d)).toBeNull()
  })
  it('follows a run: dispatched, told more, asking, decided, delivered', () => {
    expect(moveOf(ev('run.queued', { node_id: 'a', agent: 'codex' }), d)).toMatchObject({ item: 'ticket', to: { kind: 'machine', node: 'a' }, text: '派给 Codex' })
    expect(moveOf(ev('run.message', { text: '顺便加测试' }, { run_id: 'r1' }), d)).toMatchObject({ item: 'memo', to: { kind: 'run', run: 'r1', node: 'a' } })
    expect(moveOf(ev('approval.requested', { title: 'git push', review: 'user' }, { run_id: 'r1' }), d)).toMatchObject({ item: 'request', from: { kind: 'run' }, to: { actor: 'you' } })
    expect(moveOf(ev('approval.requested', { title: 'rm', review: 'ai' }, { run_id: 'r1' }), d)?.to).toEqual({ kind: 'actor', actor: 'approver' })
    expect(moveOf(ev('approval.decided', { approval_id: 'ap', option_id: 'o2', decided_by: 'user' }, { run_id: 'r1' }), d)).toMatchObject({ item: 'stamp', text: '驳回', ok: false })
    expect(moveOf(ev('approval.decided', { approval_id: 'ap', option_id: 'o1', decided_by: 'approver' }, { run_id: 'r1' }), d)).toMatchObject({ from: { actor: 'approver' }, text: '批准' })
    expect(moveOf(ev('run.ended', { status: 'succeeded', pr_url: 'x' }, { run_id: 'r1' }), d)).toMatchObject({ item: 'parcel', from: { kind: 'run' }, text: '交付 · PR' })
    expect(moveOf(ev('run.ended', { status: 'failed' }, { run_id: 'gone' }), d)).toMatchObject({ from: { kind: 'machine' }, ok: false })
  })
  it('has the helpers stamp and report', () => {
    expect(moveOf(ev('task.reviewed', { pass: false }), d)).toMatchObject({ from: { actor: 'reviewer' }, to: { col: 'blocked' }, text: '退回' })
    expect(moveOf(ev('inspector.finding', { rule: 'stalled' }), d)?.text).toBe('巡检：卡住了')
    expect(moveOf(ev('trigger.fired', { schedule_id: 's' }), d)).toMatchObject({ from: { actor: 'clock' }, to: { actor: 'sup' } })
    expect(moveOf(ev('node.offline', { node_id: 'a' }), d)).toMatchObject({ item: 'bubble', text: '下线' })
    expect(moveOf(ev('usage.recorded'), d)).toBeNull()
  })
  it('names spots for the feed', () => {
    expect(spotName({ kind: 'run', run: 'r1', node: 'a' }, d)).toBe('Claude Code')
    expect(spotName({ kind: 'machine', node: 'a' }, d)).toBe('A')
    expect(spotName({ kind: 'board', col: 'review' }, d)).toBe('看板·待验收')
    expect(spotName({ kind: 'actor', actor: 'approver' }, d)).toBe('审批员')
  })
})

describe('focus', () => {
  const d = data({ runs: [run('r1', { task_id: 't1' }), run('r2', { thread_id: 'th' })], tasks: [task('t1', { origin_thread_id: 'th' })] })
  it('covers a task, its runs and the conversation it came from', () => {
    const f = focusOf('task:t1', d)
    expect(inFocus(f, { run_id: 'r1' })).toBe(true)
    expect(inFocus(f, { run_id: 'r2' })).toBe(false)
    const t = focusOf('thread:th', d)
    expect(inFocus(t, { task_id: 't1' })).toBe(true)
    expect(inFocus(t, { run_id: 'r2' })).toBe(true)
    expect(inFocus(focusOf('run:r1', d), { task_id: 't1' })).toBe(true)
    expect(inFocus(null, {})).toBe(true)
  })
  it('shortens text to one line', () => {
    expect(brief('第一行\n第二行', 20)).toBe('第一行 第二行')
    expect(brief('一二三四五六七', 4)).toBe('一二三…')
  })
})
