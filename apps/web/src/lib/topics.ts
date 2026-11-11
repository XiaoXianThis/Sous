// Topics of a conversation (work/specs/chat-topics), kept free of the UI so
// they can be unit tested. The rules mirror the Hub's: a user message, a
// trigger, or a report, notice or inspector note without a topic starts
// one whose id is its own id.
import { taskState, type TaskState } from './status'
import type { Approval, Message, Task, Topic } from './types'

/** Whether a message without a topic starts its own. */
export function startsTopic(role: Message['role'], kind?: string): boolean {
  // A direct message to an agent stays where it is: it is not a topic of its own.
  if (kind === 'dm') return false
  return role === 'user' || kind === 'report' || kind === 'notice' || kind === 'inspector'
}

/** The topic a message from an event belongs to. */
export function topicOfEvent(role: Message['role'], kind: string | undefined, id: string, topic: string | undefined): string | undefined {
  if (topic) return topic
  return startsTopic(role, kind) ? id : undefined
}

/** Replies that answer: not steps, a consultant's words, or a direct message. */
function answers(m: Message): boolean {
  return m.role === 'assistant' && m.kind !== 'step' && m.kind !== 'consult' && m.kind !== 'dm' && !m.streaming && (m.reply_to ?? 0) > 0
}

/** The user messages of a topic no reply in it covers yet. */
export function waitingIn(msgs: Message[], topic: string): Message[] {
  let answered = 0
  for (const m of msgs) if ((m.topic_id ?? '') === topic && answers(m)) answered = Math.max(answered, m.reply_to!)
  return msgs.filter((m) => m.role === 'user' && m.kind !== 'dm' && (m.topic_id ?? '') === topic && m.seq > answered && m.seq !== Number.MAX_SAFE_INTEGER)
}

/** The messages of a topic, with the reply streaming for it. */
export function messagesOf(msgs: Message[], topic: string, answering: string | undefined): Message[] {
  return msgs.filter((m) => (m.topic_id ?? '') === topic || (m.streaming && !m.topic_id && answering === topic))
}

export type TopicGroup = 'attention' | 'active' | 'closed'

export interface TopicCardView {
  topic: Topic
  group: TopicGroup
  /** The supervisor is working on it, or messages wait for it. */
  busy: boolean
  states: TaskState[]
}

/** Where a topic card goes: anything that needs the user first, then open
 * topics, then closed ones. */
export function topicGroup(t: Topic, approvals: Approval[]): { group: TopicGroup; states: TaskState[] } {
  const states = t.tasks.map((x) => taskState(x))
  const ids = new Set(t.tasks.map((x) => x.id))
  const asking = approvals.some((a) => a.review === 'user' && a.status === 'pending' && ids.has(a.task_id))
  if (asking || states.includes('attention')) return { group: 'attention', states }
  if (t.status === 'closed') return { group: 'closed', states }
  return { group: 'active', states }
}

/** Orders the cards: by group, then most recent activity first. */
export function topicCards(topics: Topic[], msgs: Message[], approvals: Approval[], answering: string | undefined): TopicCardView[] {
  const rank: Record<TopicGroup, number> = { attention: 0, active: 1, closed: 2 }
  return topics
    .filter((t) => t.count > 0 || msgs.some((m) => m.topic_id === t.id))
    .map((t) => {
      const { group, states } = topicGroup(t, approvals)
      const busy = answering === t.id || waitingIn(msgs, t.id).length > 0
      // A topic the supervisor is on is open whatever it was.
      return { topic: t, group: busy && group === 'closed' ? 'active' : group, busy, states }
    })
    .sort((a, b) => rank[a.group] - rank[b.group] || activity(b.topic, msgs) - activity(a.topic, msgs))
}

/** The newest activity of a topic, from the list or messages that came since. */
export function activity(t: Topic, msgs: Message[]): number {
  let ts = t.updated_at
  for (const m of msgs) if (m.topic_id === t.id && m.created_at > ts) ts = m.created_at
  return ts
}

/** The newest message of a topic worth a line on its card. */
export function lastLine(t: Topic, msgs: Message[]): Message | undefined {
  let last = t.last
  for (const m of msgs) {
    if (m.topic_id !== t.id || m.kind === 'silent' || !m.content.trim() || m.streaming) continue
    if (!last || m.seq > last.seq) last = m
  }
  return last
}

/** A task's line in a topic card: how many, and how far. */
export function taskSummary(tasks: Task[]): string {
  if (tasks.length === 0) return ''
  const states = tasks.map((t) => taskState(t))
  const n = (s: TaskState) => states.filter((x) => x === s).length
  const parts = [`${tasks.length} 个任务`]
  if (n('attention')) parts.push(`${n('attention')} 个要你处理`)
  if (n('active')) parts.push(`${n('active')} 个进行中`)
  if (n('closed') === tasks.length) parts.push('都已结束')
  return parts.join(' · ')
}

// Unread marks: the newest seq of each topic the user has seen, in this
// browser only.
const SEEN_KEY = 'sous.topicSeen'

export function readSeen(): Record<string, number> {
  try {
    return JSON.parse(localStorage.getItem(SEEN_KEY) ?? '{}') as Record<string, number>
  } catch {
    return {}
  }
}

export function markSeen(topic: string, seq: number) {
  try {
    const all = readSeen()
    if ((all[topic] ?? 0) >= seq) return
    all[topic] = seq
    // Keep the newest 500 marks.
    const keep = Object.entries(all).sort((a, b) => b[1] - a[1]).slice(0, 500)
    localStorage.setItem(SEEN_KEY, JSON.stringify(Object.fromEntries(keep)))
  } catch {
    /* storage may be blocked; marks are a convenience */
  }
}

/** Whether a topic has something new from sous the user has not opened. */
export function unread(t: Topic, msgs: Message[], seen: Record<string, number>): boolean {
  const last = lastLine(t, msgs)
  if (!last || last.role === 'user') return false
  return last.seq > (seen[t.id] ?? 0)
}
