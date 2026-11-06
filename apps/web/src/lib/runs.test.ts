import { describe, expect, it } from 'vitest'
import { gitOpText } from './runs'

describe('git operations in words', () => {
  it('names the operation and its branch', () => {
    expect(gitOpText({ op: 'merge', into: 'feature-x' })).toBe('合并到 feature-x')
    expect(gitOpText({ op: 'push', branch: 'main' })).toBe('推送 main')
    expect(gitOpText({ op: 'commit' })).toBe('提交任务分支')
    expect(gitOpText({ op: 'commit', in_place: true })).toBe('提交任务的改动')
    expect(gitOpText(undefined)).toBe('git 操作')
  })
})
