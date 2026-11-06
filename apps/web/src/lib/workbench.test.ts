import { describe, expect, it } from 'vitest'
import { archivedCards, projectCards, runMeta, taskSummary } from './workbench'
import type { Overview } from './types'

const node = (id: string, extra = {}) =>
  ({ id, name: id, online: true, role: 'dev', os: 'darwin', concurrency: 2, agents: [], ...extra }) as never
const project = (id: string, nodes: string[] = [], extra = {}) =>
  ({ id, name: id, repo_url: '', nodes: nodes.map((n) => ({ node_id: n, path: '/x', priority: 0, source: 'scan' })), ...extra }) as never
const task = (id: string, project_id: string, status: string, extra = {}) =>
  ({
    id, project_id, status, status_reason: '', kind: 'code', title: `任务 ${id}`, persona_id: '',
    agent_pref: '', node_pref: '', pr_url: '', dismissed: false, updated_at: 1, created_at: 1, ...extra,
  }) as never
const run = (id: string, task_id: string, extra = {}) =>
  ({
    id, task_id, node_id: 'mac', status: 'running', agent: 'opencode', purpose: 'work', options: {},
    persona: '', created_at: 1, ...extra,
  }) as never

function overview(partial: Partial<Overview> = {}): Overview {
  return {
    projects: [project('p', ['mac']), project('misc')],
    nodes: [node('mac')],
    personas: [{ id: 'be', name: '后端', glyph: '后', color: 'purple', agent: 'opencode' } as never],
    tasks: [],
    runs: [],
    approvals: [],
    discovered: [],
    ...partial,
  }
}

describe('project cards', () => {
  it('puts a running task in the agent list, not twice in the task list', () => {
    const o = overview({
      tasks: [task('t1', 'p', 'running', { persona_id: 'be' }), task('t2', 'p', 'planned')],
      runs: [run('r1', 't1')],
    })
    const p = projectCards(o).find((c) => c.project.id === 'p')!
    expect(p.agents).toHaveLength(1)
    expect(p.agents[0]).toMatchObject({ title: '任务 t1', persona: { name: '后端' }, look: 'running' })
    expect(p.tasks.map((t) => t.task.id)).toEqual(['t2'])
    expect(p.counts).toEqual({ todo: 1, active: 1, attention: 0, closed: 0 })
  })

  it('attaches a clone run to the project it is setting up', () => {
    const o = overview({ runs: [run('r1', '', { purpose: 'clone', prompt: 'p', title: 'clone p' })] })
    const p = projectCards(o).find((c) => c.project.id === 'p')!
    expect(p.agents).toHaveLength(1)
    expect(p.agents[0].title).toBe('clone p')
  })

  it('puts a consultant on its project', () => {
    const o = overview({ runs: [run('r1', '', { purpose: 'consult', project_id: 'p', title: '顾问：p' })] })
    const p = projectCards(o).find((c) => c.project.id === 'p')!
    expect(p.agents).toHaveLength(1)
    expect(p.agents[0].title).toBe('顾问：p')
  })

  it('merges inbox tasks and does not count duplicates twice', () => {
    const stale = task('old', 'p', 'review', { status_reason: '等你验收' })
    const o = overview({
      tasks: [task('t1', 'p', 'planned'), stale],
    })
    const p = projectCards(o, [stale, task('t2', 'p', 'blocked')]).find((c) => c.project.id === 'p')!
    expect(p.allTasks.map((t) => t.task.id).sort()).toEqual(['old', 't1', 't2'])
    expect(p.counts).toEqual({ todo: 1, active: 0, attention: 2, closed: 0 })
    expect(p.rank).toBe('attention')
  })

  it('counts unmerged branches, which need no attention', () => {
    const o = overview({
      tasks: [task('a', 'p', 'done', { git_state: 'unmerged' }), task('b', 'p', 'done', { git_state: 'merged' }), task('c', 'p', 'done', { git_state: 'unmerged' })],
    })
    const p = projectCards(o).find((c) => c.project.id === 'p')!
    expect(p.unmerged).toBe(2)
    expect(p.counts).toEqual({ todo: 0, active: 0, attention: 0, closed: 3 })
  })

  it('caps agents and tasks and keeps the rest for the project sheet', () => {
    const tasks = [task('a1', 'p', 'running'), task('a2', 'p', 'running'), task('a3', 'p', 'running')]
    for (let i = 0; i < 6; i++) tasks.push(task(`t${i}`, 'p', 'planned'))
    const o = overview({ tasks, runs: [run('r1', 'a1'), run('r2', 'a2'), run('r3', 'a3')] })
    const p = projectCards(o).find((c) => c.project.id === 'p')!
    expect(p.agents).toHaveLength(2)
    expect(p.hiddenAgents).toBe(1)
    expect(p.tasks).toHaveLength(4)
    expect(p.hiddenTasks).toBe(2)
    expect(p.allTasks).toHaveLength(6)
  })

  it('orders attention first and misc last', () => {
    const o = overview({
      projects: [project('idle'), project('misc'), project('work'), project('attention')],
      tasks: [
        task('a', 'attention', 'blocked'),
        task('b', 'work', 'running'),
        task('c', 'idle', 'planned'),
        task('d', 'misc', 'planned'),
      ],
      runs: [run('r1', 'b')],
    })
    expect(projectCards(o).map((c) => c.project.id)).toEqual(['attention', 'work', 'idle', 'misc'])
  })
})

describe('locations', () => {
  it('shows machines, the deploy target and the remote', () => {
    const o = overview({
      projects: [
        project('p', ['mac'], {
          repo_url: 'git@github.com:owner/repo.git',
          remote_key: 'github.com/owner/repo',
          deploy: { node_id: 'srv', path: '/srv/app', command: 'deploy' },
        }),
      ],
      nodes: [node('mac'), node('srv', { role: 'server', os: 'linux', online: false })],
    })
    const p = projectCards(o).find((c) => c.project.id === 'p')!
    expect(p.locations.map((l) => [l.kind, l.name, l.online])).toEqual([
      ['node', 'mac', true],
      ['deploy', 'srv', false],
      ['remote', 'GitHub', undefined],
    ])
    expect(p.locations[2].href).toBe('https://github.com/owner/repo')
  })

  it('has no locations for misc', () => {
    const p = projectCards(overview()).find((c) => c.project.id === 'misc')!
    expect(p.locations).toEqual([])
  })
})

describe('run meta', () => {
  const agentInfo = {
    name: 'opencode', title: 'OpenCode',
    options: [
      { id: 'model', values: [{ value: 'opus', name: 'Opus 4.1' }], current: 'opus' },
      { id: 'thought_level', values: [{ value: 'high', name: '高' }, { value: 'low', name: '低' }], current: 'low' },
    ],
    defaults: {},
  }
  const info = { agents: [agentInfo] } as never

  it('reads the run options and maps them to readable names', () => {
    const r = run('r1', 't1', { options: { model: 'opus', thought_level: 'high' } })
    expect(runMeta(r, info)).toEqual({ agentTitle: 'OpenCode', model: 'Opus 4.1', effort: '高' })
  })

  it('falls back to the machine defaults', () => {
    const withDefault = { agents: [{ ...agentInfo, defaults: { model: 'opus' } }] } as never
    expect(runMeta(run('r1', 't1'), withDefault)).toMatchObject({ model: 'Opus 4.1', effort: '低' })
  })

  it('does not invent a model the agent never reported', () => {
    expect(runMeta(run('r1', 't1'), {} as never)).toEqual({ agentTitle: 'opencode', model: '', effort: '' })
  })
})

describe('task summary', () => {
  it('reads like 岗位 · agent · 机器', () => {
    const o = overview({ tasks: [task('t1', 'p', 'running', { persona_id: 'be' })], runs: [run('r1', 't1')] })
    expect(taskSummary(o, o.tasks[0], o.runs[0])).toBe('后端 · opencode · mac')
  })

  it('does not repeat an agent that stands in for the persona', () => {
    const o = overview({ tasks: [task('t1', 'p', 'planned', { agent_pref: 'cursor' })] })
    expect(taskSummary(o, o.tasks[0])).toBe('cursor')
  })
})

describe('archived projects', () => {
  it('marks archived cards and lists them latest change first, searchable', () => {
    const o = overview({
      projects: [
        project('p', ['mac']),
        project('old', [], { name: '旧网站', notes: 'blog', archived_at: 5, updated_at: 10, touched_at: 10 }),
        project('older', [], { name: 'older-tool', archived_at: 5, updated_at: 3, touched_at: 3 }),
        project('misc'),
      ],
      tasks: [task('t1', 'older', 'review', { updated_at: 50 })],
    })
    const cards = projectCards(o)
    expect(cards.find((c) => c.project.id === 'p')!.archived).toBe(false)
    expect(cards.find((c) => c.project.id === 'older')!.touchedAt).toBe(50)
    expect(archivedCards(cards).map((c) => c.project.id)).toEqual(['older', 'old'])
    expect(archivedCards(cards, 'BLOG').map((c) => c.project.id)).toEqual(['old'])
    expect(archivedCards(cards, '网站 旧').map((c) => c.project.id)).toEqual(['old'])
    expect(archivedCards(cards, 'nothing')).toEqual([])
  })
})
