// Pure state transitions for the chat, driven by Hub events. Kept free of
// Svelte so it can be unit tested.
import type { HubEvent, Message, Thread } from './types'

export interface ChatState {
  threads: Thread[]
  messages: Record<string, Message[]>
  /** Highest stored seq applied; the event stream resumes from here. */
  lastSeq: number
  /** Bumped whenever a memory changes, so open memory views reload. */
  memoryRev: number
}

export function emptyState(): ChatState {
  return { threads: [], messages: {}, lastSeq: 0, memoryRev: 0 }
}

const DRAFT_SEQ = Number.MAX_SAFE_INTEGER

export function sortThreads(threads: Thread[]): Thread[] {
  return threads.sort((a, b) => {
    if (a.kind === 'main' && b.kind !== 'main') return -1
    if (b.kind === 'main' && a.kind !== 'main') return 1
    return b.updated_at - a.updated_at
  })
}

function list(state: ChatState, threadId: string): Message[] {
  // Read the array back after assigning it: with Svelte's $state the stored
  // value is a reactive proxy, not the array literal that was assigned.
  if (!state.messages[threadId]) state.messages[threadId] = []
  return state.messages[threadId]
}

function touch(state: ChatState, threadId: string, ts: number) {
  const t = state.threads.find((t) => t.id === threadId)
  if (t && ts > t.updated_at) {
    t.updated_at = ts
    sortThreads(state.threads)
  }
}

/** Inserts or replaces a stored message, keeping the list ordered by seq. */
export function upsertMessage(state: ChatState, m: Message) {
  const msgs = list(state, m.thread_id)
  const i = msgs.findIndex((x) => x.id === m.id)
  if (i >= 0) {
    msgs[i] = { ...m, thinking: msgs[i].thinking, streaming: false }
  } else {
    msgs.push(m)
  }
  msgs.sort((a, b) => a.seq - b.seq)
}

/** Merges a page of history loaded over REST. */
export function mergeHistory(state: ChatState, threadId: string, page: Message[]) {
  for (const m of page) upsertMessage(state, { ...m, thread_id: threadId })
}

/** Applies one event. Returns false for a stored event that was already applied. */
export function applyEvent(state: ChatState, ev: HubEvent): boolean {
  if (ev.seq > 0) {
    if (ev.seq <= state.lastSeq) return false
    state.lastSeq = ev.seq
  }
  const p = ev.payload ?? {}
  switch (ev.type) {
    case 'thread.created': {
      if (!state.threads.some((t) => t.id === ev.thread_id)) {
        state.threads.push({
          id: ev.thread_id!,
          title: p.title,
          kind: p.kind,
          created_at: ev.ts,
          updated_at: ev.ts,
          archived: false,
        })
        sortThreads(state.threads)
      }
      break
    }
    case 'message.user':
    case 'message.assistant': {
      upsertMessage(state, {
        id: p.message_id,
        thread_id: ev.thread_id!,
        role: ev.type === 'message.user' ? 'user' : 'assistant',
        content: p.content,
        error: p.error || undefined,
        reply_to: p.reply_to || undefined,
        kind: p.kind || undefined,
        seq: ev.seq,
        created_at: ev.ts,
      })
      touch(state, ev.thread_id!, ev.ts)
      break
    }
    case 'memory.put':
    case 'memory.deleted':
      state.memoryRev++
      break
    case 'message.assistant.delta': {
      const msgs = list(state, ev.thread_id!)
      let m = msgs.find((x) => x.id === p.message_id)
      if (!m) {
        m = {
          id: p.message_id,
          thread_id: ev.thread_id!,
          role: 'assistant',
          content: '',
          seq: DRAFT_SEQ,
          created_at: ev.ts,
          streaming: true,
        }
        msgs.push(m)
      }
      if (!m.streaming) break // the final message already arrived
      if (p.thinking) m.thinking = (m.thinking ?? '') + p.delta
      else m.content += p.delta
      break
    }
  }
  return true
}

/** True while the supervisor is answering in this thread. */
export function isAnswering(state: ChatState, threadId: string): boolean {
  const msgs = state.messages[threadId] ?? []
  if (msgs.some((m) => m.streaming)) return true
  let answered = 0
  let newest = 0
  for (const m of msgs) {
    if (m.role === 'assistant' && (m.reply_to ?? 0) > answered) answered = m.reply_to!
    if (m.role === 'user' && m.seq !== DRAFT_SEQ) newest = m.seq
  }
  return newest > answered
}
