// Application state, shared by the components.
import { api } from './api'
import { applyEvent, emptyState, mergeHistory, sortThreads } from './reduce'
import { EventStream, type StreamStatus } from './stream'

export const chat = $state(emptyState())

export const ui = $state({
  status: 'connecting' as StreamStatus,
  current: '' as string,
  loaded: {} as Record<string, boolean>,
  error: '' as string,
})

let stream: EventStream | null = null

export async function startSession(onLoggedOut: () => void) {
  const [{ threads }] = await Promise.all([api.threads()])
  chat.threads = sortThreads(threads)
  if (!ui.current || !threads.some((t) => t.id === ui.current)) {
    ui.current = threads[0]?.id ?? ''
  }
  if (ui.current) await openThread(ui.current)
  stream?.stop()
  stream = new EventStream(
    () => chat.lastSeq,
    (ev) => applyEvent(chat, ev),
    (s) => (ui.status = s),
    onLoggedOut,
  )
  stream.start()
}

export function stopSession() {
  stream?.stop()
  stream = null
  Object.assign(chat, emptyState())
  ui.loaded = {}
  ui.current = ''
}

export async function openThread(id: string) {
  ui.current = id
  if (ui.loaded[id]) return
  const page = await api.messages(id)
  mergeHistory(chat, id, page.messages)
  // Start the stream after what the page already covers.
  if (chat.lastSeq === 0) chat.lastSeq = page.last_seq
  ui.loaded[id] = true
}

export async function newThread() {
  const t = await api.createThread('新对话')
  if (!chat.threads.some((x) => x.id === t.id)) chat.threads.push(t)
  sortThreads(chat.threads)
  ui.loaded[t.id] = true
  ui.current = t.id
  return t
}

export async function send(content: string) {
  await api.send(ui.current, content)
}
