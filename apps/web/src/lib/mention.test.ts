import { describe, expect, it } from 'vitest'
import { activeProjects, filterProjects, insertMention, keptMentions, matchScore, mentionQuery, splitMentions } from './mention'
import type { Project } from './types'

const project = (id: string, name: string, archived_at = 0) => ({ id, name, archived_at }) as Project

describe('mentionQuery', () => {
  it('finds the query between the @ and the caret', () => {
    expect(mentionQuery('@', 1)).toEqual({ start: 0, query: '' })
    expect(mentionQuery('看看 @so', 6)).toEqual({ start: 3, query: 'so' })
    expect(mentionQuery('看看\n@博客', 6)).toEqual({ start: 3, query: '博客' })
  })

  it('ignores an @ inside a word, after a space in the query, or after the caret', () => {
    expect(mentionQuery('a@b.com', 7)).toBeNull()
    expect(mentionQuery('@sous 好的', 8)).toBeNull()
    expect(mentionQuery('hi @x', 2)).toBeNull()
    expect(mentionQuery('no mention', 10)).toBeNull()
  })
})

describe('candidates', () => {
  it('leaves out archived projects', () => {
    const list = activeProjects([project('a', 'alpha'), project('b', 'beta', 1700000000000)])
    expect(list.map((p) => p.id)).toEqual(['a'])
    expect(activeProjects(undefined)).toEqual([])
  })

  it('matches names fuzzily, prefix first', () => {
    expect(matchScore('Sous', 'so')).toBeGreaterThan(matchScore('Ask-sous', 'so'))
    expect(matchScore('my-blog', 'mbg')).toBeGreaterThan(0)
    expect(matchScore('my-blog', 'xyz')).toBe(-1)
    const list = [project('1', 'web-shop'), project('2', 'shop'), project('3', 'sh-o-p'), project('4', 'docs')]
    expect(filterProjects(list, 'shop').map((p) => p.id)).toEqual(['2', '1', '3'])
    expect(filterProjects(list, '').map((p) => p.id)).toEqual(['1', '2', '3', '4'])
    expect(filterProjects(list, 'SHOP').map((p) => p.id)).toEqual(['2', '1', '3'])
  })
})

describe('insertMention', () => {
  it('replaces the query with the name and puts the caret after it', () => {
    const text = '看看 @so 怎么样'
    const r = insertMention(text, { start: 3, query: 'so' }, 6, 'Sous')
    expect(r.text).toBe('看看 @Sous 怎么样')
    expect(r.caret).toBe(9)
  })

  it('adds a space at the end of the text', () => {
    expect(insertMention('@', { start: 0, query: '' }, 1, 'blog')).toEqual({ text: '@blog ', caret: 6 })
  })
})

describe('keptMentions', () => {
  it('drops references whose tag was deleted and repeats', () => {
    const picked = [{ project_id: 'a', name: 'alpha' }, { project_id: 'b', name: 'beta' }, { project_id: 'a', name: 'alpha' }]
    expect(keptMentions('@alpha 和 @beta', picked)).toEqual(picked.slice(0, 2))
    expect(keptMentions('@alpha 和 beta', picked)).toEqual([picked[0]])
  })
})

describe('splitMentions', () => {
  it('splits text into plain runs and tags, longest name first', () => {
    const m = [{ project_id: 'a', name: 'web' }, { project_id: 'b', name: 'web shop' }]
    expect(splitMentions('看 @web shop 和 @web。', m)).toEqual([
      { text: '看 ' },
      { text: '@web shop', mention: m[1] },
      { text: ' 和 ' },
      { text: '@web', mention: m[0] },
      { text: '。' },
    ])
  })

  it('returns the text as is without references', () => {
    expect(splitMentions('a@b', undefined)).toEqual([{ text: 'a@b' }])
    expect(splitMentions('', [])).toEqual([])
  })
})
