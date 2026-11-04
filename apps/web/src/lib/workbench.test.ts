import { describe, expect, it } from 'vitest'
import { columns, load, rows } from './workbench'
import type { Overview } from './types'

const node = (id: string, extra = {}) => ({ id, name: id, online: true, role: 'dev', concurrency: 2, ...extra }) as never
const project = (id: string, nodes: string[], extra = {}) =>
  ({ id, name: id, nodes: nodes.map((n) => ({ node_id: n, path: '/x', priority: 0, source: 'scan' })), ...extra }) as never
const task = (id: string, project_id: string, status: string, extra = {}) =>
  ({ id, project_id, status, status_reason: '', kind: 'code', persona_id: '', ...extra }) as never

function overview(): Overview {
  return {
    projects: [project('sous', ['mac', 'srv'], { deploy: { node_id: 'srv' } }), project('blog', ['pc']), project('misc', [])],
    nodes: [node('srv', { role: 'server' }), node('pc', { online: false }), node('mac')],
    personas: [{ id: 'be', name: '后端', glyph: '后', color: 'purple' } as never],
    tasks: [task('t1', 'sous', 'running', { persona_id: 'be' }), task('t2', 'sous', 'queued'), task('t3', 'blog', 'done')],
    runs: [{ id: 'r1', task_id: 't1', node_id: 'mac', status: 'running', persona: '后端' } as never],
    approvals: [{ run_id: 'r1', review: 'user' } as never],
    discovered: [],
  }
}

describe('overview', () => {
  it('orders machines: dev online, dev offline, servers', () => {
    expect(columns(overview().nodes).map((n) => n.id)).toEqual(['mac', 'pc', 'srv'])
  })
  it('places workers in cells', () => {
    const [sous, blog] = rows(overview())
    expect(sous.project.id).toBe('sous')
    expect(sous.cells.mac.workers).toHaveLength(1)
    expect(sous.cells.mac.workers[0].look).toBe('waiting')
    expect(sous.cells.mac.workers[0].persona?.name).toBe('后端')
    expect(sous.cells.srv).toMatchObject({ place: true, deploy: true, workers: [] })
    expect(sous.waiting.map((t) => t.id)).toEqual(['t2'])
    expect(sous.look).toBe('running')
    expect(blog.look).toBe('done')
    expect(rows(overview()).some((r) => r.project.id === 'misc')).toBe(false)
  })
  it('counts load', () => {
    expect(load(overview(), 'mac')).toEqual({ busy: 1, slots: 2 })
  })
})
