import { describe, expect, it } from 'vitest'
import { ago } from './time'

describe('ago', () => {
  const now = Date.UTC(2026, 10, 4, 12)
  const min = 60_000
  const day = 1440 * min
  it('counts minutes, hours, days, months and years', () => {
    expect(ago(0, now)).toBe('')
    expect(ago(now - 20_000, now)).toBe('刚刚')
    expect(ago(now + 5_000, now)).toBe('刚刚')
    expect(ago(now - 3 * min, now)).toBe('3 分钟前')
    expect(ago(now - 4 * 60 * min - 59 * min, now)).toBe('4 小时前')
    expect(ago(now - 2 * day, now)).toBe('2 天前')
    expect(ago(now - 29 * day, now)).toBe('29 天前')
    expect(ago(now - 95 * day, now)).toBe('3 个月前')
    expect(ago(now - 400 * day, now)).toBe('1 年前')
  })
})
