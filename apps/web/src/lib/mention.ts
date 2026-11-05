// @ references to workbench projects in the composer: finding the query at
// the caret, matching project names, inserting the pick and keeping the
// structured list (`context.mentions`) in step with the text.
import type { Mention, Project } from './types'

/** The `@query` the caret is in: `start` is the index of the `@`. */
export interface MentionQuery {
  start: number
  query: string
}

/**
 * The query being typed at the caret, or null. The `@` must start the text
 * or follow whitespace (so `a@b.com` does not open the list), and the query
 * runs to the caret without whitespace.
 */
export function mentionQuery(text: string, caret: number): MentionQuery | null {
  const before = text.slice(0, caret)
  const at = before.lastIndexOf('@')
  if (at < 0) return null
  if (at > 0 && !/\s/.test(before[at - 1])) return null
  const query = before.slice(at + 1)
  if (/\s/.test(query)) return null
  return { start: at, query }
}

/** The projects that can be referenced: active ones only. */
export function activeProjects(projects: Project[] | undefined): Project[] {
  return (projects ?? []).filter((p) => !p.archived_at)
}

/**
 * How well `name` matches `query`, or -1. Case-insensitive; a prefix beats a
 * substring, which beats the letters appearing in order (fuzzy).
 */
export function matchScore(name: string, query: string): number {
  const n = name.toLowerCase()
  const q = query.toLowerCase()
  if (!q) return 1
  if (n.startsWith(q)) return 4
  const i = n.indexOf(q)
  if (i >= 0) return 3 - i / (n.length + 1)
  let k = 0
  let gaps = 0
  for (let j = 0; j < n.length && k < q.length; j++) {
    if (n[j] === q[k]) k++
    else if (k > 0) gaps++
  }
  return k === q.length ? 1 / (1 + gaps) : -1
}

/** The candidates for a query, best first; ties keep the given order. */
export function filterProjects(projects: Project[], query: string, limit = 8): Project[] {
  return projects
    .map((p, i) => ({ p, i, s: matchScore(p.name, query) }))
    .filter((x) => x.s >= 0)
    .sort((a, b) => b.s - a.s || a.i - b.i)
    .slice(0, limit)
    .map((x) => x.p)
}

/** Replaces the `@query` before the caret with `@name `; returns the new text and caret. */
export function insertMention(text: string, q: MentionQuery, caret: number, name: string): { text: string; caret: number } {
  const tag = `@${name} `
  const after = text.slice(caret).replace(/^ /, '')
  return { text: text.slice(0, q.start) + tag + after, caret: q.start + tag.length }
}

/** The picked mentions still present in the text, each project once. */
export function keptMentions(text: string, picked: Mention[]): Mention[] {
  const seen = new Set<string>()
  return picked.filter((m) => {
    if (seen.has(m.project_id) || !text.includes(`@${m.name}`)) return false
    seen.add(m.project_id)
    return true
  })
}

/** Text split into plain runs and `@name` references, for drawing tags. */
export type Segment = { text: string; mention?: Mention }

export function splitMentions(text: string, mentions: Mention[] | undefined): Segment[] {
  const list = (mentions ?? []).filter((m) => m.name).sort((a, b) => b.name.length - a.name.length)
  if (list.length === 0) return text ? [{ text }] : []
  const out: Segment[] = []
  let plain = ''
  let i = 0
  outer: while (i < text.length) {
    if (text[i] === '@') {
      for (const m of list) {
        const tag = `@${m.name}`
        if (text.startsWith(tag, i)) {
          if (plain) out.push({ text: plain })
          plain = ''
          out.push({ text: tag, mention: m })
          i += tag.length
          continue outer
        }
      }
    }
    plain += text[i]
    i++
  }
  if (plain) out.push({ text: plain })
  return out
}
