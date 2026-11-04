import { describe, expect, it } from 'vitest'
import { agentChoices } from './tasks'

describe('agentChoices', () => {
  it('offers the known agents before any machine reports', () => {
    expect(agentChoices([])).toContain('opencode')
    const node = { agents: [{ name: 'fake', installed: true }, { name: 'claude', installed: true }] } as any
    expect(agentChoices([node]).filter((a) => a === 'claude')).toHaveLength(1)
    expect(agentChoices([node])).toContain('fake')
  })
})
