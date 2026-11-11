import { describe, expect, it } from 'vitest'
import { parseRoute, routeHash } from './route'

describe('routes', () => {
  it('round-trips', () => {
    for (const r of [{ view: 'overview', id: '' }, { view: 'projects', id: '' }, { view: 'project', id: 'p 1' }, { view: 'task', id: '01X' }, { view: 'inbox', id: '' }] as const) {
      expect(parseRoute(routeHash(r))).toEqual(r)
    }
  })
  it('reads push links', () => {
    expect(parseRoute('#approvals')).toEqual({ view: 'inbox', id: '' })
    expect(parseRoute('#task=abc')).toEqual({ view: 'task', id: 'abc' })
    expect(parseRoute('#thread=t1')).toEqual({ view: 'overview', id: '', thread: 't1' })
    expect(parseRoute('#nonsense')).toEqual({ view: 'overview', id: '' })
  })
})

it('routes system traces', () => {
  expect(parseRoute('#/system')).toEqual({view:'system',id:''})
  expect(parseRoute('#/system?trace=task%3Aabc')).toEqual({view:'system',id:'task:abc'})
  expect(routeHash({view:'system',id:'run:abc'})).toBe('#/system?trace=run%3Aabc')
})
