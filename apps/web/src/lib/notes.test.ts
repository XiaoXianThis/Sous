import { describe, expect, it } from 'vitest'
import { joinNotes, noteGroups } from './notes'
import type { Note } from './types'
const note = (id: string, created_at: number, status: Note['status'] = 'open'): Note => ({ id, text: id, status, created_at, updated_at: created_at })
describe('notes', () => {
  it('joins notes in chronological order and caps attachments', () => {
    const a = note('a', 2), b = note('b', 1)
    a.attachments = Array.from({ length: 9 }, (_, i) => ({ id: String(i), name: 'file', mime: 'text/plain', size: 1 }))
    expect(joinNotes([a, b])).toMatchObject({ text: 'b\n\na', dropped: true })
    expect(joinNotes([a, b]).attachments).toHaveLength(8)
  })
  it('groups by status', () => { expect(noteGroups([note('a', 1), note('b', 2, 'sent')]).sent.map((n) => n.id)).toEqual(['b']) })
})
