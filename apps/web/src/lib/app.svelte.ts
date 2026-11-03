// Application state, shared by the components.
import { api } from './api'
import { applyEvent, emptyState, mergeHistory, sortThreads } from './reduce'
import { runFromHistory } from './runs'
import { parseHash } from './tasks'
import { EventStream, type StreamStatus } from './stream'
import type { HubEvent } from './types'

export const chat = $state(emptyState())

export const ui = $state({
  status: 'connecting' as StreamStatus,
  current: '' as string,
  /** The main pane: a conversation or the task board. */
  view: 'chat' as 'chat' | 'tasks',
  /** The task open in the detail sheet: an id, 'new', or ''. */
  task: '' as string,
  /** Scroll the inbox into view (after opening an approval link). */
  focusInbox: 0,
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
  ui.view = 'chat'
  if (ui.loaded[id]) return
  const [page, { runs }] = await Promise.all([api.messages(id), api.runs(id)])
  mergeHistory(chat, id, page.messages)
  const details = await Promise.all(runs.filter((r) => !chat.runs[r.id]).map((r) => api.run(r.id)))
  for (const d of details) {
    if (!chat.runs[d.run.id]) chat.runs[d.run.id] = runFromHistory(d.run, d.events, d.approvals)
  }
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
  let body = ''
  if (ev.type === 'message.assistant' && ev.payload?.kind === 'notice') body = ev.payload.content
  else if (ev.type === 'approval.requested' && ev.payload.review !== 'ai') body = `需要你审批：${ev.payload.title}`
  if (!body) return
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
  if (document.visibilityState === 'visible') return
  const n = new Notification('sous', { body, tag: ev.id, icon: '/icon.svg' })
  n.onclick = () => {
    window.focus()
    if (ev.thread_id) openThread(ev.thread_id)
  }
}

/** Follows a link from a push notification: #approvals, #task=<id>, #thread=<id>. */
export async function followHash(hash: string) {
  const r = parseHash(hash)
  if (r.thread && chat.threads.some((t) => t.id === r.thread)) await openThread(r.thread)
  if (r.task) ui.task = r.task
  if (r.approvals) {
    const main = chat.threads.find((t) => t.kind === 'main')
    if (main) await openThread(main.id)
    ui.focusInbox++
  }
  if (r.thread || r.task || r.approvals) history.replaceState(null, '', location.pathname)
}
