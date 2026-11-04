import { describe, expect, it } from 'vitest'
import { columnCount, distribute, estimateHeight } from './masonry'
import type { ProjectCard } from './workbench'

function card(id: string, extra: Partial<ProjectCard> = {}): ProjectCard {
  return {
    project: { id, name: id } as never,
    locations: [],
    agents: [],
    tasks: [],
    counts: { todo: 0, active: 0, attention: 0, closed: 0 },
    rank: 'closed',
    allAgents: [],
    allTasks: [],
    hiddenAgents: 0,
    hiddenTasks: 0,
    ...extra,
  }
}

describe('columnCount', () => {
  it('grows with the width and stays in 1..4', () => {
    expect(columnCount(0)).toBe(1)
    expect(columnCount(300)).toBe(1)
    expect(columnCount(700)).toBe(2)
    expect(columnCount(2000)).toBe(4)
  })
})

describe('distribute', () => {
  it('puts each card in the currently shortest column', () => {
    const items = [
      { id: 'a', h: 300 },
      { id: 'b', h: 100 },
      { id: 'c', h: 100 },
      { id: 'd', h: 100 },
    ]
    const cols = distribute(items, 2, (x) => x.h)
    expect(cols.map((c) => c.map((x) => x.id))).toEqual([['a'], ['b', 'c', 'd']])
  })

  it('keeps the source order inside a column', () => {
    const items = [{ id: 'a', h: 10 }, { id: 'b', h: 10 }, { id: 'c', h: 10 }]
    expect(distribute(items, 1, (x) => x.h)[0].map((x) => x.id)).toEqual(['a', 'b', 'c'])
  })
})

describe('estimateHeight', () => {
  it('counts tasks, agents and notes', () => {
    const small = estimateHeight(card('a'))
    expect(estimateHeight(card('a', { tasks: [{ state: 'todo' } as never] }))).toBeGreaterThan(small)
    expect(estimateHeight(card('a', { agents: [{}, {}] as never }))).toBeGreaterThan(small)
    expect(estimateHeight(card('a', { project: { id: 'a', name: 'a', notes: 'x' } as never }))).toBeGreaterThan(small)
  })
})
