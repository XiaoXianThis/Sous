import { describe, expect, it } from 'vitest'
import { dmTargets, dmTo } from './dm'
import type { Run, Task } from './types'

const task = (id: string, status: Task['status'], updated_at = 0): Task => ({
  id, title: `任务 ${id}`, status, status_reason: '', project_id: 'p1', kind: 'code', priority: 0,
  permission_mode: 'auto', created_at: 0, updated_at, place: 'scratch', review: 'none', on_fail: 'rework',
  base_from: 'local', depends_on: [], last_agent: 'claude',
} as unknown as Task)

const run = (id: string, task_id: string, status: Run['status'], purpose: Run['purpose'] = 'work'): Run => ({
  id, thread_id: 't', task_id, purpose, permission_mode: 'auto', base_branch: '', pr_url: '', conflict: false,
  title: '', node_id: 'n', agent: 'claude', kind: 'code', repo_path: '/r', prompt: '', status,
  workdir: '', branch: '', summary: '', diffstat: '', error: '', created_at: 1, started_at: 0, ended_at: 0,
})

describe('dmTargets', () => {
  it('puts a running run first and the consultant last', () => {
    const t = [task('a', 'done'), task('b', 'running')]
    const r = [run('r1', 'b', 'running'), run('r2', 'a', 'succeeded')]
    const out = dmTargets({ id: 'p1' }, t, r)
    expect(out[0]).toMatchObject({ kind: 'run', id: 'r1' })
    expect(out[out.length - 1]).toMatchObject({ kind: 'consult' })
  })

  it('lists one target per task and at most ten idle tasks', () => {
    const tasks = Array.from({ length: 12 }, (_, i) => task(`t${i}`, 'done', 100 - i))
    const runs = [run('r1', 't0', 'running'), run('r2', 't0', 'running', 'chat')]
    const out = dmTargets({ id: 'p1' }, tasks, runs)
    expect(out.filter((x) => x.kind === 'run')).toHaveLength(1)
    expect(out.filter((x) => x.kind === 'task')).toHaveLength(10)
    expect(out.filter((x) => x.kind === 'task')[0].id).toBe('t1')
  })

  it('leaves out queued tasks and other projects', () => {
    const tasks = [task('q', 'queued'), { ...task('x', 'done'), project_id: 'p2' }]
    const out = dmTargets({ id: 'p1' }, tasks, [])
    expect(out.map((x) => x.key)).toEqual(['consult'])
  })

  it('turns a target into the API shape', () => {
    expect(dmTo({ key: 'run:r1', kind: 'run', label: '', id: 'r1' })).toEqual({ run_id: 'r1' })
    expect(dmTo({ key: 'task:t1', kind: 'task', label: '', id: 't1' })).toEqual({ task_id: 't1' })
    expect(dmTo({ key: 'consult', kind: 'consult', label: '' })).toEqual({ consult: true })
  })
})
