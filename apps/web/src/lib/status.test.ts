import { describe, expect, it } from 'vitest'
import { avatarOf, progress, runLook, stages, taskLook, taskState, taskStateDetail } from './status'

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
})

describe('task states', () => {
  it('collapses every status into the four states', () => {
    expect(taskState(t('inbox'))).toBe('todo')
    expect(taskState(t('planned'))).toBe('todo')
    expect(taskState(t('queued'))).toBe('active')
    expect(taskState(t('queued', { status_reason: '等待机器：没有在线的开发机' }))).toBe('attention')
    expect(taskState(t('running'))).toBe('active')
    expect(taskState(t('running', { status_reason: '已暂停：重复调用' }))).toBe('attention')
    expect(taskState(t('review'))).toBe('active')
    expect(taskState(t('waiting_approval'))).toBe('attention')
    expect(taskState(t('blocked'))).toBe('attention')
    expect(taskState(t('failed'))).toBe('attention')
    expect(taskState(t('done'))).toBe('closed')
    expect(taskState(t('done', { pr_url: 'https://x/pull/1' }))).toBe('attention')
    expect(taskState(t('done', { pr_url: 'https://x/pull/1', dismissed: true }))).toBe('closed')
    expect(taskState(t('cancelled'))).toBe('closed')
  })

  it('keeps a readable detail for the badge', () => {
    expect(taskStateDetail(t('queued'))).toBe('排队')
    expect(taskStateDetail(t('running', { status_reason: '已暂停：重复调用' }))).toBe('已暂停')
    expect(taskStateDetail(t('review'))).toBe('验收中')
    expect(taskStateDetail(t('done', { pr_url: 'https://x/pull/1' }))).toBe('PR 待合并')
    expect(taskStateDetail(t('failed'))).toBe('失败')
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
