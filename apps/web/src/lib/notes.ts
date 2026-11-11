import { MAX_FILES } from './attachments'
import type { Attachment, Note } from './types'

export function joinNotes(notes: Note[]): { text: string; attachments: Attachment[]; dropped: boolean } {
  const ordered = [...notes].sort((a, b) => a.created_at - b.created_at)
  const files = ordered.flatMap((n) => n.attachments ?? [])
  return { text: ordered.map((n) => n.text).filter(Boolean).join('\n\n'), attachments: files.slice(0, MAX_FILES), dropped: files.length > MAX_FILES }
}
export function noteGroups(notes: Note[]): Record<Note['status'], Note[]> {
  return { open: notes.filter((n) => n.status === 'open'), sent: notes.filter((n) => n.status === 'sent'), archived: notes.filter((n) => n.status === 'archived') }
}
