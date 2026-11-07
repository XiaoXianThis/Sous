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
    expect(taskState(t('review', { status_reason: '等你验收' }))).toBe('attention')
    expect(taskState(t('waiting_approval'))).toBe('attention')
    expect(taskState(t('blocked'))).toBe('attention')
    expect(taskState(t('failed'))).toBe('attention')
    expect(taskState(t('done'))).toBe('closed')
    // A done task is closed whatever became of its branch (decision 0022).
    expect(taskState(t('done', { git_state: 'unmerged' }))).toBe('closed')
    expect(taskState(t('done', { pr_url: 'https://x/pull/1', git_state: 'pr' }))).toBe('closed')
    expect(taskState(t('cancelled'))).toBe('closed')
  })

  it('keeps a readable detail for the badge', () => {
    expect(taskStateDetail(t('queued'))).toBe('排队中')
    expect(taskStateDetail(t('running', { status_reason: '已暂停：重复调用' }))).toBe('已暂停')
    expect(taskStateDetail(t('review'))).toBe('验收中')
    expect(taskStateDetail(t('review', { status_reason: '没有配置验收模型，等你验收' }))).toBe('等你验收')
    expect(taskStateDetail(t('done', { git_state: 'unmerged' }))).toBe('未合并')
    expect(taskStateDetail(t('done', { git_state: 'merged' }))).toBe('已合并')
    expect(taskStateDetail(t('done', { git_state: 'pr' }))).toBe('PR 已开')
    expect(taskStateDetail(t('done'))).toBe('已完成')
    expect(taskStateDetail(t('failed'))).toBe('失败')
  })
})

describe('stages', () => {
  it('follows a code task through its stages, with no delivery stage', () => {
    const states = (s: ReturnType<typeof stages>) => s.map((x) => x.state).join(' ')
    expect(states(stages(t('planned')))).toBe('current todo todo')
    expect(states(stages(t('running')))).toBe('done current todo')
    expect(states(stages(t('review')))).toBe('done done current')
    expect(states(stages(t('running', { next: 'review' })))).toBe('done done current')
    expect(states(stages(t('done', { git_state: 'unmerged' })))).toBe('done done done')
    expect(states(stages(t('failed')))).toBe('done failed todo')
    expect(states(stages(t('blocked', { status_reason: '验收没有通过：缺测试' })))).toBe('done done failed')
    expect(progress(stages(t('review')))).toBe('2/3')
  })
  it('leaves out the review stage when there is none', () => {
    expect(stages(t('running', { review: 'none' })).map((s) => s.text)).toEqual(['计划', '编码'])
    // Decision 0024: a task nobody asked to review has no review stage.
    expect(stages(t('running', { review: '' })).map((s) => s.text)).toEqual(['计划', '编码'])
  })
  it('has fewer stages for other kinds', () => {
    // An ops task has a health check stage only when it deploys a commit.
    expect(stages(t('running', { kind: 'ops' })).map((s) => s.text)).toEqual(['执行'])
    expect(stages(t('running', { kind: 'ops', deploy_commit: 'abc' })).map((s) => s.text)).toEqual(['执行', '健康检查'])
    expect(stages(t('running', { kind: 'ops', deploy_commit: 'abc', next: 'health' })).map((s) => s.state)).toEqual(['done', 'current'])
    expect(stages(t('blocked', { kind: 'ops', deploy_commit: 'abc', status_reason: '部署后的健康检查没有通过：502' })).map((s) => s.state)).toEqual(['done', 'failed'])
    expect(stages(t('done', { kind: 'general' })).every((s) => s.state === 'done')).toBe(true)
  })
})

describe('avatars', () => {
  it('uses the persona, else the agent', () => {
    expect(avatarOf({ name: '后端', glyph: '后', color: 'purple' } as never)).toEqual({ glyph: '后', color: 'purple', title: '后端' })
    expect(avatarOf(undefined, 'codex')).toEqual({ glyph: 'C', color: 'gray', title: 'codex' })
  })
})
