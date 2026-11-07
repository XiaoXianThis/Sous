import { describe, expect, it } from 'vitest'
import { triggerView } from './trigger'

describe('triggerView', () => {
  it('reads a schedule and drops the note for the model', () => {
    const v = triggerView('【定时任务「早报」】\n整理昨天的进展\n\n（这是用户设定的定时任务，现在到时间了。照着做。）')
    expect(v).toEqual({ kind: 'schedule', title: '早报', body: '整理昨天的进展' })
  })

  it('keeps the manual mark of a schedule', () => {
    expect(triggerView('【定时任务「早报」（手动触发）】\n整理').title).toBe('早报（手动触发）')
  })

  it('heads a follow-up with its first clause', () => {
    const v = triggerView('【跟进】任务「th：动画」（id 01M）已经受阻 4 小时，原因：push 失败。')
    expect(v.kind).toBe('followup')
    expect(v.title).toBe('任务「th：动画」已经受阻 4 小时')
    expect(v.body).toBe('任务「th：动画」（id 01M）已经受阻 4 小时，原因：push 失败。')
  })

  it('drops the follow-up note for the model', () => {
    expect(triggerView('【跟进】审批等了 2 小时\n\n（这是 sous 的自动跟进，不是用户发的消息。）').body).toBe('审批等了 2 小时')
  })

  it('heads task news with its kind and first clause', () => {
    const v = triggerView('【任务进展】任务「改端口」（id 01M）现在已完成，汇报在上面。\n\n（这是 sous 的自动通知，不是用户发的消息；判断要不要接着推进。）')
    expect(v).toEqual({ kind: 'task', label: '任务进展', title: '任务「改端口」现在已完成', body: '任务「改端口」（id 01M）现在已完成，汇报在上面。' })
    const q = triggerView('【agent 的问题】任务 01M「改端口」的 codex（mac）在执行中问：\n用 8080 吗？')
    expect(q.kind).toBe('task')
    expect(q.label).toBe('agent 的问题')
  })

  it('falls back to the first line', () => {
    expect(triggerView('【别的】\n正文')).toEqual({ kind: 'other', title: '别的', body: '正文' })
  })
})
