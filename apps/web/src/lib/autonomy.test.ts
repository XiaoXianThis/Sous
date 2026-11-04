import { describe, expect, it } from 'vitest'
import { applyEvent, emptyState, isAnswering } from './reduce'
import { byRole, dailyTotals, describeCron, tokens } from './schedules'
import type { HubEvent } from './types'

const ev = (seq: number, type: string, payload: any, extra: Partial<HubEvent> = {}): HubEvent => ({
  seq, id: `e${seq}`, ts: seq * 1000, type, actor: 'x', thread_id: 't1', payload, ...extra,
})

describe('M4 events', () => {
  it('shows a trigger as input the supervisor answers', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'trigger.fired', { message_id: 'm1', content: '【定时任务「日报」】\n汇总', schedule_id: 's1', kind: 'cron' }))
    expect(s.messages.t1[0]).toMatchObject({ id: 'm1', role: 'user', kind: 'trigger' })
    expect(s.schedulesRev).toBe(1)
    expect(isAnswering(s, 't1')).toBe(true)
    applyEvent(s, ev(2, 'message.assistant', { message_id: 'a1', content: '昨天完成 2 个', reply_to: 1 }))
    expect(isAnswering(s, 't1')).toBe(false)
  })

  it('pauses and resumes a run, expiring its approvals', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'run.queued', { node_id: 'n', agent: 'claude', kind: 'general', prompt: 'x' }, { run_id: 'r1' }))
    applyEvent(s, ev(2, 'run.started', { workdir: '/w' }, { run_id: 'r1' }))
    applyEvent(s, ev(3, 'approval.requested', { approval_id: 'a1', title: 'rm', options: [] }, { run_id: 'r1' }))
    const inbox = s.inboxRev
    applyEvent(s, ev(4, 'run.paused', { reason: '巡检：重复读文件' }, { run_id: 'r1' }))
    expect(s.runs.r1.run).toMatchObject({ paused: true, pause_reason: '巡检：重复读文件' })
    expect(s.runs.r1.approvals[0].status).toBe('expired')
    expect(s.inboxRev).toBeGreaterThan(inbox)
    applyEvent(s, ev(5, 'run.resumed', {}, { run_id: 'r1' }))
    expect(s.runs.r1.run.paused).toBe(false)
    applyEvent(s, ev(6, 'inspector.finding', { rule: 'repeat' }, { run_id: 'r1' }))
    expect(s.lastSeq).toBe(6)
  })

  it('bumps schedules on schedule events', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'schedule.put', { schedule_id: 's1' }))
    applyEvent(s, ev(2, 'schedule.deleted', { schedule_id: 's1' }))
    expect(s.schedulesRev).toBe(2)
  })
})

describe('schedule and usage helpers', () => {
  it('describes common cron expressions', () => {
    expect(describeCron('0 9 * * *')).toBe('每天 9:00')
    expect(describeCron('30 18 * * 1-5')).toBe('工作日 18:30')
    expect(describeCron('0 9 * * 1')).toBe('每周一 9:00')
    expect(describeCron('*/15 * * * *')).toBe('每 15 分钟')
    expect(describeCron('0 0 1 * *')).toBe('每月 1 日 0:00')
    expect(describeCron('5 4 * 2 *')).toBe('5 4 * 2 *')
  })

  it('formats tokens', () => {
    expect(tokens(1234)).toBe('1,234')
    expect(tokens(2_500_000)).toBe('250 万')
  })

  it('fills days without calls and sums roles', () => {
    const since = Date.UTC(2026, 10, 1)
    const d = dailyTotals([
      { day: '2026-11-02', role: 'supervisor', calls: 2, input_tokens: 10, output_tokens: 5, cached_tokens: 1 },
      { day: '2026-11-02', role: 'reviewer', calls: 1, input_tokens: 4, output_tokens: 1, cached_tokens: 0 },
    ], since, 3, 'UTC')
    expect(d.map((x) => x.day)).toEqual(['2026-11-01', '2026-11-02', '2026-11-03'])
    expect(d[1]).toMatchObject({ input: 14, output: 6, calls: 3 })
    const r = byRole([
      { provider: 'p', model: 'a', role: 'supervisor', calls: 1, input_tokens: 1, output_tokens: 1, cached_tokens: 0 },
      { provider: 'p', model: 'b', role: 'supervisor', calls: 1, input_tokens: 5, output_tokens: 1, cached_tokens: 0 },
    ])
    expect(r).toEqual([{ role: 'supervisor', calls: 2, input: 6, output: 2, cached: 0 }])
  })
})
