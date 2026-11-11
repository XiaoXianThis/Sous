// Meetings (decision 0029), kept free of the UI so they can be unit tested.
import type { Message } from './types'

/** The message numbers a minutes text cites, in order, duplicates kept. */
export function minuteRefs(content: string): number[] {
  return [...content.matchAll(/\[#(\d+)\]/g)].map((m) => Number(m[1]))
}

/** The highest later version of the same meeting's minutes, or null. */
export function supersededBy(messages: Message[], m: Message): number | null {
  const meeting = m.source?.meeting_id
  const version = m.source?.version
  if (!meeting || !version) return null
  let next: number | null = null
  for (const x of messages) {
    if (x.kind !== 'minutes' || x.source?.meeting_id !== meeting) continue
    const v = x.source.version ?? 0
    if (v > version && (next === null || v > next)) next = v
  }
  return next
}

/** Turns a citation into a link the chat intercepts (#seq-12); an existing link is left alone. */
export function renderRefs(text: string): string {
  return text.replace(/\[#(\d+)\](?!\()/g, '[#$1](#seq-$1)')
}
