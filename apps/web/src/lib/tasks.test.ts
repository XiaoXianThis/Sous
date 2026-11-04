import { describe, expect, it } from 'vitest'
import { board, tone } from './tasks'
import type { Task } from './types'

const task = (id: string, status: Task['status'], priority = 0, created_at = 0): Task => ({
  id, status, priority, created_at, updated_at: created_at, parent_id: '', origin_thread_id: '', kind: 'general', title: id,
  detail: '', status_reason: '', project_id: '', agent_pref: '', node_pref: '', permission_mode: '', branch: '',
  base_branch: '', pr_url: '', review_note: '', next: '', dismissed: false, depends_on: [], persona_id: '',
})

describe('board', () => {
  it('groups by column and orders by priority, then age', () => {
    const b = board([task('a', 'planned', 0, 1), task('b', 'queued', 5, 2), task('c', 'inbox', 0, 0), task('d', 'failed'), task('e', 'waiting_approval')])
    expect(b.todo.map((t) => t.id)).toEqual(['b', 'c', 'a'])
    expect(b.attention.map((t) => t.id)).toEqual(['d'])
    expect(b.doing.map((t) => t.id)).toEqual(['e'])
    expect(b.closed).toEqual([])
  })
})


describe('tone', () => {
  it('marks what needs attention', () => {
    expect(tone('blocked')).toBe('attention')
    expect(tone('review')).toBe('active')
    expect(tone('inbox')).toBe('quiet')
  })
})

describe('agentChoices', () => {
  it('offers the known agents before any machine reports', async () => {
    const { agentChoices } = await import('./tasks')
    expect(agentChoices([])).toContain('opencode')
    const node = { agents: [{ name: 'fake', installed: true }, { name: 'claude', installed: true }] } as any
    expect(agentChoices([node]).filter((a) => a === 'claude')).toHaveLength(1)
    expect(agentChoices([node])).toContain('fake')
  })
})
