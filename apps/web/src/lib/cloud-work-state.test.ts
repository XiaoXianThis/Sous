import { describe, expect, it } from 'vitest'
import { cloudWorkDescription } from './cloud-work-state'

describe('cloud work progress', () => {
  it('distinguishes boot, queue, retirement and a lost worker without misreporting job execution', () => {
    expect(cloudWorkDescription('queued', 'starting')).toContain('正在启动')
    expect(cloudWorkDescription('queued', 'online')).toContain('等待云工作机领取')
    expect(cloudWorkDescription('queued', 'retiring')).toContain('留在队列')
    expect(cloudWorkDescription('queued', 'needs_reconciliation')).toContain('运维对账')
    expect(cloudWorkDescription('queued', 'off')).toBe(cloudWorkDescription('queued'))
    expect(cloudWorkDescription('cancelled', 'retiring')).toBe(cloudWorkDescription('cancelled'))
  })
})
