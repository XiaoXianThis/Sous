import { describe, expect, it } from 'vitest'
import { avatarOf, mostUrgent, progress, runLook, stages, taskLook } from './status'

const t = (status: string, extra: Record<string, unknown> = {}) =>
  ({ kind: 'code', status, pr_url: '', next: '', status_reason: '', ...extra }) as never

describe('looks', () => {
  it('maps task statuses', () => {
    expect(taskLook(t('running'))).toBe('running')
    expect(taskLook(t('running', { status_reason: '已暂停：重复调用' }))).toBe('paused')
    expect(taskLook(t('waiting_approval'))).toBe('waiting')
    expect(taskLook(t('blocked'))).toBe('failed')
    expect(taskLook(t('inbox'))).toBe('planned')
  })
  it('maps runs', () => {
    expect(runLook({ status: 'running', paused: false }, true)).toBe('waiting')
    expect(runLook({ status: 'running', paused: true })).toBe('paused')
    expect(runLook({ status: 'interrupted' })).toBe('failed')
  })
  it('puts what needs the user first', () => {
    expect(mostUrgent(['done', 'running', 'waiting'])).toBe('waiting')
    expect(mostUrgent([])).toBeUndefined()
  })
})

describe('stages', () => {
  it('follows a code task through its pipeline', () => {
    const states = (s: ReturnType<typeof stages>) => s.map((x) => x.state).join(' ')
    expect(states(stages(t('planned')))).toBe('current todo todo todo')
    expect(states(stages(t('running')))).toBe('done current todo todo')
    expect(states(stages(t('review')))).toBe('done done current todo')
    expect(states(stages(t('running', { status_reason: '正在交付：push 分支并创建 PR' })))).toBe('done done done current')
    expect(states(stages(t('waiting_approval', { next: 'deliver', status_reason: '等你审批' })))).toBe('done done done current')
    expect(states(stages(t('done', { pr_url: 'x' })))).toBe('done done done done')
    expect(states(stages(t('done', { pr_url: 'x' }), { hasDeploy: true }))).toBe('done done done done todo')
    expect(states(stages(t('done', { pr_url: 'x' }), { hasDeploy: true, deployed: true }))).toBe('done done done done done')
    expect(states(stages(t('failed')))).toBe('done failed todo todo')
    expect(progress(stages(t('review')))).toBe('2/4')
  })
  it('has fewer stages for other kinds', () => {
    expect(stages(t('running', { kind: 'ops' })).map((s) => s.text)).toEqual(['执行', '健康检查'])
    expect(stages(t('done', { kind: 'general' })).every((s) => s.state === 'done')).toBe(true)
  })
})

describe('avatars', () => {
  it('uses the persona, else the agent', () => {
    expect(avatarOf({ name: '后端', glyph: '后', color: 'purple' } as never)).toEqual({ glyph: '后', color: 'purple', title: '后端' })
    expect(avatarOf(undefined, 'codex')).toEqual({ glyph: 'C', color: 'gray', title: 'codex' })
  })
})
