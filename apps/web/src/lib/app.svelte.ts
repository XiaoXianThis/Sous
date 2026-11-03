// Application state, shared by the components.
import { api } from './api'
import { applyEvent, emptyState, mergeHistory, sortThreads } from './reduce'
import { EventStream, type StreamStatus } from './stream'
import type { HubEvent } from './types'

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
    (ev) => {
      if (applyEvent(chat, ev)) maybeNotify(ev)
    },
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

/** Shows a system notification for a notice while the page is in the background. */
function maybeNotify(ev: HubEvent) {
  if (ev.type !== 'message.assistant' || ev.payload?.kind !== 'notice') return
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
  if (document.visibilityState === 'visible') return
  const n = new Notification('sous', { body: ev.payload.content, tag: ev.payload.message_id, icon: '/icon.svg' })
  n.onclick = () => {
    window.focus()
    if (ev.thread_id) openThread(ev.thread_id)
  }
}

export function notificationState(): NotificationPermission | 'unsupported' {
  return typeof Notification === 'undefined' ? 'unsupported' : Notification.permission
}
