import { describe, expect, it } from 'vitest'
import { activities, activityOf, laneOf, laneState, laneSummary, recordText, runTasks, steps, visibleLanes } from './coord'
import type { SysGraph, SysRecord } from './types'

const rec = (ts: number, edge: string, summary: string, refs: SysRecord['refs'] = {}): SysRecord => ({ ts, edge, summary, refs })

describe('laneOf', () => {
  it('maps modules and instances to lanes', () => {
    expect(laneOf('ui')).toBe('you')
    expect(laneOf('cli')).toBe('you')
    expect(laneOf('provider:mock')).toBe('model')
    expect(laneOf('machine:abc')).toBe('machine')
    expect(laneOf('mcpserver:gh')).toBe('tools')
    expect(laneOf('effects')).toBe('log')
    expect(laneOf('nope')).toBe('')
  })
})

describe('recordText', () => {
  it('names http calls, events, frames, model and tool calls', () => {
    expect(recordText(rec(0, 'ui>httpapi', 'POST /api/v1/threads/01M6G9541K4XTZSM2PZJB3J7WE/messages'))).toBe('发消息')
    expect(recordText(rec(0, 'ui>httpapi', 'PUT /api/v1/projects/01M6G9541K4XTZSM2PZJB3J7WE'))).toBe('PUT projects/…')
    expect(recordText(rec(0, 'httpapi>eventlog', 'message.user'))).toBe('用户消息')
    expect(recordText(rec(0, 'nodehub>machine:a', 'run.start'))).toBe('开始 Run')
    expect(recordText(rec(0, 'supervisor>models', 'supervisor · echo'))).toBe('主管调用 echo')
    expect(recordText(rec(0, 'supervisor>task', 'task_create'))).toBe('工具 task_create')
    expect(recordText(rec(0, 'effects>task', 'task.advance'))).toBe('推进任务')
  })
})

describe('steps', () => {
  const t = { thread_id: 'T1' }
  it('pairs a write with its reads into one arrow', () => {
    const list = steps([rec(1000, 'httpapi>eventlog', 'message.user', t), rec(1001, 'eventlog>supervisor', 'message.user', t)], { detail: false })
    expect(list).toHaveLength(1)
    expect(list[0]).toMatchObject({ from: 'api', to: 'sup', text: '用户消息', activity: 'thread:T1', count: 1 })
    expect(list[0].records).toHaveLength(2)
  })
  it('makes a write nobody reads a note on the writer', () => {
    const list = steps([rec(1000, 'supervisor>eventlog', 'message.assistant', t)], { detail: false })
    expect(list[0]).toMatchObject({ from: 'sup', to: 'sup', text: '回复' })
  })
  it('leaves plumbing out of the plain view and keeps it in detail', () => {
    const rs = [rec(1000, 'httpapi>ui', 'task.status'), rec(1001, 'ui>httpapi', 'GET /api/v1/me'), rec(1002, 'machine:a>nodehub', 'ack'), rec(1003, 'eventlog>effects', '入队')]
    expect(steps(rs, { detail: false })).toHaveLength(0)
    expect(steps(rs, { detail: true })).toHaveLength(4)
  })
  it('folds repeats within 3 seconds and starts again after a gap', () => {
    const rs = [0, 500, 1000, 5000].map((ts) => rec(ts, 'machine:a>nodehub', 'run.output', { run_id: 'R' }))
    const list = steps(rs, { detail: false })
    expect(list.map((s) => s.count)).toEqual([3, 1])
    expect(list[0].until).toBe(1000)
  })
  it('puts runs under their task', () => {
    const list = steps([rec(0, 'nodehub>machine:a', 'run.start', { run_id: 'R' })], { detail: false, taskOfRun: (r) => (r === 'R' ? 'K' : '') })
    expect(list[0].activity).toBe('task:K')
  })
  it('shows a direct call as an arrow and drops the writer\'s echo', () => {
    const list = steps([rec(1000, 'task>nodehub', 'call run.queued', { task_id: 'K' }), rec(1000, 'nodehub>eventlog', 'run.queued', { task_id: 'K' })], { detail: false })
    expect(list).toHaveLength(1)
    expect(list[0]).toMatchObject({ from: 'task', to: 'hub', text: 'Run 排队' })
  })
  it('leaves bookkeeping, frame echoes and same-lane hops out', () => {
    const rs = [
      rec(0, 'supervisor>eventlog', 'usage.recorded'),
      rec(1, 'supervisor>eventlog', 'message.seen'),
      rec(2, 'nodehub>eventlog', 'run.update'),
      rec(3, 'models>provider:mock', 'supervisor · echo'),
    ]
    expect(steps(rs, { detail: false })).toHaveLength(0)
    expect(steps(rs, { detail: true })).toHaveLength(4)
  })
  it('drops the writer reading its own event when a call already shows it', () => {
    const k = { task_id: 'K' }
    const list = steps([rec(0, 'httpapi>task', 'call task.created', k), rec(0, 'task>eventlog', 'task.created', k), rec(0, 'eventlog>task', 'task.created', k)], { detail: false })
    expect(list.map((s) => `${s.from}>${s.to} ${s.text}`)).toEqual(['api>task 建任务'])
    expect(runTasks([rec(0, 'a>b', '', { run_id: 'R', task_id: 'K' })]).get('R')).toBe('K')
  })
  it('keeps an echo that someone reads, as an arrow', () => {
    const r = { run_id: 'R' }
    const list = steps([rec(0, 'nodehub>eventlog', 'run.ended', r), rec(0, 'eventlog>task', 'run.ended', r)], { detail: false })
    expect(list).toHaveLength(1)
    expect(list[0]).toMatchObject({ from: 'hub', to: 'task', text: 'Run 结束' })
    expect(recordText(rec(0, 'task>eventlog', 'message.assistant'))).toBe('汇报')
  })
  it('folds a repeat into a recent row even when another row came between', () => {
    const r = { run_id: 'R' }
    const list = steps([rec(0, 'machine:a>nodehub', 'run.update', r), rec(10, 'machine:a>nodehub', 'run.output', r), rec(20, 'machine:a>nodehub', 'run.update', r), rec(30, 'machine:a>nodehub', 'run.output', r)], { detail: false })
    expect(list.map((s) => `${s.text}×${s.count}`)).toEqual(['进展×2', '输出×2'])
  })
  it('orders records by time', () => {
    const list = steps([rec(2000, 'supervisor>models', 'supervisor · echo'), rec(1000, 'ui>httpapi', 'POST /api/v1/tasks')], { detail: false })
    expect(list.map((s) => s.text)).toEqual(['建任务', '主管调用 echo'])
  })
})

describe('activities and helpers', () => {
  it('lists activities newest first', () => {
    const list = steps([rec(0, 'supervisor>models', 'supervisor · echo', { thread_id: 'A' }), rec(9000, 'supervisor>models', 'supervisor · echo', { task_id: 'B' })], { detail: false })
    expect(activities(list)).toEqual(['task:B', 'thread:A'])
    expect(activityOf(rec(0, 'x>y', '', {}))).toBe('')
  })
  it('leaves the event log out of the plain view', () => {
    expect(visibleLanes(false).some((l) => l.id === 'log')).toBe(false)
    expect(visibleLanes(true).some((l) => l.id === 'log')).toBe(true)
  })
  it('summarizes lane state', () => {
    const g: SysGraph = {
      now: 0,
      edges: [],
      modules: [
        { id: 'machine:a', name: 'a', layer: 'node', desc: '' },
        { id: 'machine:b', name: 'b', layer: 'node', desc: '' },
        { id: 'supervisor', name: '主管', layer: 'hub', desc: '' },
      ],
      status: { 'machine:a': { state: 'down', summary: '' }, 'machine:b': { state: 'busy', summary: '' }, supervisor: { state: 'idle', summary: '空闲' } },
    }
    const machine = visibleLanes(false).find((l) => l.id === 'machine')!
    expect(laneState(machine, g)).toBe('busy')
    expect(laneSummary(machine, g)).toBe('1/2 台在线')
    expect(laneSummary(visibleLanes(false).find((l) => l.id === 'sup')!, g)).toBe('空闲')
  })
})
