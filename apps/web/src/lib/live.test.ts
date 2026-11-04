import { describe, expect, it } from 'vitest'
import { applyLive, oneLine, parseLogTail, seedLive, tail, type Live } from './live'
import type { HubEvent } from './types'

const ev = (type: string, payload: any, runId = 'r1'): HubEvent =>
  ({ seq: 0, id: `${type}-${Math.random()}`, ts: 1, type, actor: 'system', run_id: runId, payload })

describe('live activities', () => {
  it('merges consecutive text and thought chunks', () => {
    const live: Live = {}
    applyLive(live, ev('run.output', { kind: 'text', text: '正在' }))
    applyLive(live, ev('run.output', { kind: 'text', text: '修改' }))
    applyLive(live, ev('run.output', { kind: 'thought', text: '先看' }))
    applyLive(live, ev('run.output', { kind: 'thought', text: '结构' }))
    expect(live.r1.map((a) => [a.kind, a.text])).toEqual([
      ['text', '正在修改'],
      ['thought', '先看结构'],
    ])
  })

  it('updates a tool call in place', () => {
    const live: Live = {}
    applyLive(live, ev('run.update', { kind: 'tool', tool_call_id: 't1', title: '读取 a.go', status: 'pending' }))
    applyLive(live, ev('run.update', { kind: 'tool', tool_call_id: 't1', status: 'completed' }))
    expect(live.r1).toEqual([{ id: 't1', kind: 'tool', text: '读取 a.go', status: 'completed' }])
  })

  it('keeps policy decisions and the status line', () => {
    const live: Live = {}
    applyLive(live, ev('run.update', { kind: 'permission', decision: 'deny', title: 'rm -rf ~' }))
    applyLive(live, ev('run.update', { kind: 'status', text: '正在启动' }))
    applyLive(live, ev('run.update', { kind: 'status', text: '正在读取' }))
    expect(live.r1.map((a) => a.text)).toEqual(['已拒绝：rm -rf ~', '正在读取'])
  })

  it('caps the feed and each text', () => {
    const live: Live = {}
    for (let i = 0; i < 30; i++) applyLive(live, ev('run.update', { kind: 'tool', tool_call_id: `t${i}`, title: `步骤 ${i}` }))
    applyLive(live, ev('run.output', { kind: 'text', text: 'x'.repeat(5000) }))
    expect(live.r1).toHaveLength(24)
    expect(live.r1[live.r1.length - 1].text.length).toBe(2000)
    expect(live.r1[live.r1.length - 1].text.endsWith('x')).toBe(true)
  })

  it('ignores events without a run and plans', () => {
    const live: Live = {}
    expect(applyLive(live, { ...ev('run.update', { kind: 'plan', plan: [] }), run_id: '' })).toBe(false)
    expect(applyLive(live, ev('run.update', { kind: 'plan', plan: [] }))).toBe(false)
    expect(live).toEqual({})
  })
})

describe('tail and oneLine', () => {
  it('returns the newest activities, oldest first', () => {
    const acts = [
      { id: '1', kind: 'text' as const, text: 'a' },
      { id: '2', kind: 'text' as const, text: 'b' },
      { id: '3', kind: 'text' as const, text: 'c' },
    ]
    expect(tail(acts, 2).map((a) => a.text)).toEqual(['b', 'c'])
    expect(tail([], 2)).toEqual([])
  })

  it('collapses whitespace and keeps the newest part', () => {
    expect(oneLine({ id: '1', kind: 'text', text: '  正在\n\n读取  文件 ' })).toBe('正在 读取 文件')
    const long = oneLine({ id: '1', kind: 'text', text: 'a'.repeat(200) }, 10)
    expect(long).toBe('…aaaaaaaaaa')
  })
})

describe('log tail', () => {
  const log = [
    '开始检查',
    '[pending] read 读取 src/app.go',
    '[completed] read 读取 src/app.go',
    '发现两个问题',
    '[failed] exec 运行测试',
  ].join('\n')

  it('parses tool lines and text', () => {
    const acts = parseLogTail(log)
    expect(acts.map((a) => [a.kind, a.text, a.status])).toEqual([
      ['text', '开始检查', undefined],
      ['tool', '读取 src/app.go', 'pending'],
      ['tool', '读取 src/app.go', 'completed'],
      ['text', '发现两个问题', undefined],
      ['tool', '运行测试', 'failed'],
    ])
  })

  it('only reads the tail of a long log', () => {
    const acts = parseLogTail('x'.repeat(5000) + '\n最后一行', 100)
    expect(acts[acts.length - 1].text).toBe('最后一行')
    expect(acts.length).toBeLessThanOrEqual(3)
  })

  it('seeds only an empty feed', () => {
    const live: Live = {}
    expect(seedLive(live, 'r1', log)).toBe(true)
    expect(live.r1.length).toBe(5)
    expect(seedLive(live, 'r1', 'again')).toBe(false)
    expect(seedLive(live, 'r2', '')).toBe(false)
  })
})
