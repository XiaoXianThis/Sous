// Pure state transitions for the chat, driven by Hub events. Kept free of the
// UI so it can be unit tested.
import { applyJobEvent, type JobView } from './jobs'
import { applyRunEvent, type RunView } from './runs'
import { topicOfEvent } from './topics'
import type { HubEvent, Message, Thread } from './types'

export interface ChatState {
  threads: Thread[]
  messages: Record<string, Message[]>
  /** Highest stored seq applied; the event stream resumes from here. */
  lastSeq: number
  /** Bumped whenever a memory changes, so open memory views reload. */
  memoryRev: number
  /** Bumped whenever a machine changes, so the machine list reloads. */
  nodesRev: number
  /** Bumped whenever a task or project changes, so task views reload. */
  tasksRev: number
  /** Bumped whenever something that belongs in the inbox may have changed. */
  inboxRev: number
  /** Bumped whenever a schedule changes or fires. */
  schedulesRev: number
  /** Bumped when a Run starts, ends, pauses or resumes; drives the workbench. */
  runsRev: number
  /** Bumped when a thread's context segment changes (thread.compacted). */
  contextRev: number
  /** Bumped when a topic may have changed: its messages, its tasks or itself. */
  topicsRev: number
  /** The topic the supervisor is working on, by thread (message.seen). */
  answering: Record<string, string>
  /** Messages the supervisor attached to another topic, by id: the topic they started. */
  moved: Record<string, string>
  runs: Record<string, RunView>
  /** Installs and logins on machines, by job id. */
  jobs: Record<string, JobView>
}

export function emptyState(): ChatState {
  return { threads: [], messages: {}, lastSeq: 0, memoryRev: 0, nodesRev: 0, tasksRev: 0, inboxRev: 0, schedulesRev: 0, runsRev: 0, contextRev: 0, topicsRev: 0, answering: {}, moved: {}, runs: {}, jobs: {} }
}

const DRAFT_SEQ = Number.MAX_SAFE_INTEGER

/** Messages to show: a silent end of a turn that already spoke in steps
 * says nothing new, so it is left out. */
export function visibleMessages(msgs: Message[]): Message[] {
  const spoke = new Set(msgs.filter((m) => m.kind === 'step').map((m) => m.reply_to))
  return msgs.filter((m) => !(m.kind === 'silent' && !m.content && spoke.has(m.reply_to)))
}

export function sortThreads(threads: Thread[]): Thread[] {
  return threads.sort((a, b) => {
    if (a.kind === 'main' && b.kind !== 'main') return -1
    if (b.kind === 'main' && a.kind !== 'main') return 1
    return b.updated_at - a.updated_at
  })
}

function list(state: ChatState, threadId: string): Message[] {
  // Read the array back after assigning it, so every caller shares the stored
  // array rather than a copy.
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
    // Tool calls only grow: a page of history fetched before a message.tools
    // event must not drop them.
    const tools = m.tools?.length ? m.tools : msgs[i].tools
    msgs[i] = { ...m, thinking: msgs[i].thinking, streaming: false, ...(tools && { tools }) }
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
  if (applyJobEvent(state.jobs, ev)) {
    if (ev.type === 'agent.job.ended') state.nodesRev++
    return true
  }
  if (ev.type.startsWith('node.')) {
    state.nodesRev++
    state.tasksRev++ // queued tasks may start
    return true
  }
  if (ev.type.startsWith('task.') || ev.type.startsWith('project.')) {
    state.tasksRev++
    if (ev.type.startsWith('task.')) state.topicsRev++
    if (ev.type === 'task.status' || ev.type === 'task.updated' || ev.type === 'task.git') state.inboxRev++
    return true
  }
  if (ev.type.startsWith('schedule.')) {
    state.schedulesRev++
    return true
  }
  // A run that ends (or pauses) expires its pending approvals without an approval event.
  if (ev.type.startsWith('approval.') || ev.type === 'run.ended' || ev.type === 'run.paused' || ev.type === 'run.resumed') {
    state.inboxRev++
  }
  if (ev.type === 'inspector.finding') {
    state.inboxRev++
    return true
  }
  if (ev.type === 'run.ended' || ev.type === 'run.queued') state.tasksRev++
  if (ev.type === 'consult.closed') state.tasksRev++
  // The workbench reloads on any Run lifecycle change. Output does not bump:
  // it is ephemeral and would reload the whole grid for every chunk.
  if (ev.type === 'run.queued' || ev.type === 'run.started' || ev.type === 'run.ended' || ev.type === 'run.paused' || ev.type === 'run.resumed') {
    state.runsRev++
  }
  if (applyRunEvent(state.runs, ev)) return true
  switch (ev.type) {
    case 'thread.created': {
      if (!state.threads.some((t) => t.id === ev.thread_id)) {
        state.threads.push({
          id: ev.thread_id!,
          title: p.title,
          kind: p.kind,
          ...(p.project_id && { project_id: p.project_id }),
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
      const role = ev.type === 'message.user' ? 'user' : 'assistant'
      const topic = topicOfEvent(role, p.kind || undefined, p.message_id, p.topic_id)
      upsertMessage(state, {
        id: p.message_id,
        thread_id: ev.thread_id!,
        role: ev.type === 'message.user' ? 'user' : 'assistant',
        content: p.content,
        error: p.error || undefined,
        reply_to: p.reply_to || undefined,
        kind: p.kind || undefined,
        attachments: p.attachments?.length ? p.attachments : undefined,
        context: p.context || undefined,
        source: p.source || undefined,
        tools: p.tools?.length ? p.tools : undefined,
        ...(topic && { topic_id: topic }),
        ...(ev.task_id && { task_id: ev.task_id }),
        seq: ev.seq,
        created_at: ev.ts,
      })
      // A reply that ends a turn: the supervisor is off that topic.
      if (role === 'assistant' && p.kind !== 'step' && p.reply_to && state.answering[ev.thread_id!] === topic) delete state.answering[ev.thread_id!]
      state.topicsRev++
      touch(state, ev.thread_id!, ev.ts)
      break
    }
    case 'message.moved': {
      const msgs = list(state, ev.thread_id!)
      const i = msgs.findIndex((x) => x.id === p.message_id)
      if (i >= 0) msgs[i] = { ...msgs[i], topic_id: p.topic_id }
      if (p.topic_id === p.message_id) delete state.moved[p.message_id]
      else state.moved[p.message_id] = p.from
      state.topicsRev++
      break
    }
    case 'message.seen': {
      const msgs = list(state, ev.thread_id!)
      for (const id of p.message_ids ?? []) {
        const i = msgs.findIndex((x) => x.id === id)
        if (i >= 0) {
          msgs[i] = { ...msgs[i], seen: true }
          // The first one names the topic the turn is on.
          if (!state.answering[ev.thread_id!] && msgs[i].topic_id) state.answering[ev.thread_id!] = msgs[i].topic_id!
        }
      }
      break
    }
    case 'topic.updated':
      state.topicsRev++
      break
    case 'message.tools': {
      // The supervisor's tool calls after one of its messages (the full list so far).
      const msgs = list(state, ev.thread_id!)
      const i = msgs.findIndex((x) => x.id === p.message_id)
      if (i >= 0) msgs[i] = { ...msgs[i], tools: p.tools?.length ? p.tools : undefined }
      break
    }
    case 'trigger.fired': {
      // Input nobody typed: a schedule or a follow-up, stored as a user message.
      upsertMessage(state, {
        id: p.message_id,
        thread_id: ev.thread_id!,
        role: 'user',
        content: p.content,
        kind: 'trigger',
        topic_id: p.topic_id || p.message_id,
        ...(ev.task_id && { task_id: ev.task_id }),
        seq: ev.seq,
        created_at: ev.ts,
      })
      state.topicsRev++
      touch(state, ev.thread_id!, ev.ts)
      if (p.schedule_id) state.schedulesRev++
      break
    }
    case 'memory.put':
    case 'memory.deleted':
      state.memoryRev++
      break
    case 'thread.compacted':
      state.contextRev++
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

/** True while the supervisor is answering in this thread. A step (what it
 * said before calling tools) does not end the answer; the turn's last
 * message does. */
export function isAnswering(state: ChatState, threadId: string): boolean {
  const msgs = state.messages[threadId] ?? []
  if (msgs.some((m) => m.streaming)) return true
  let answered = 0
  let newest = 0
  for (const m of msgs) {
    if (m.role === 'assistant' && m.kind !== 'step' && (m.reply_to ?? 0) > answered) answered = m.reply_to!
    if (m.role === 'user' && m.seq !== DRAFT_SEQ) newest = m.seq
  }
  return newest > answered
}
