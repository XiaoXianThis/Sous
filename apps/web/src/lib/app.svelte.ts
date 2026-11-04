// Application state, shared by the components.
import { api } from './api'
import { applyEvent, emptyState, mergeHistory, sortThreads } from './reduce'
import { runFromHistory } from './runs'
import { home, parseRoute, routeHash, type Route } from './route'
import { EventStream, type StreamStatus } from './stream'
import type { HubEvent, MessageContext, Overview } from './types'

export const chat = $state(emptyState())

export const ui = $state({
  status: 'connecting' as StreamStatus,
  current: '' as string,
  loaded: {} as Record<string, boolean>,
  error: '' as string,
  /** M5: what the workbench shows. */
  route: { ...home } as Route,
  /** The chat panel is open (desktop) or the chat tab is shown (phone). */
  chatOpen: true,
  /** The settings tab open in the full-screen settings, or ''. */
  settings: '' as string,
  /** Phone: which bottom tab is shown. */
  tab: 'chat' as 'chat' | 'work' | 'inbox',
  /** The workbench context sent with the next message; cleared by the user. */
  dropContext: false,
})

/** Everything the workbench draws; reloaded when tasks, machines or approvals change. */
export const wb = $state({ data: null as Overview | null, error: '' })

let overviewLoading: Promise<void> | null = null
export function loadOverview(): Promise<void> {
  overviewLoading ??= api
    .overview()
    .then((d) => {
      wb.data = d
      wb.error = ''
    })
    .catch((e) => {
      wb.error = (e as Error).message
    })
    .finally(() => (overviewLoading = null))
  return overviewLoading
}

/** Shows a page of the workbench and records it in the URL. */
export function go(r: Route) {
  ui.route = { ...r }
  ui.tab = 'work'
  ui.dropContext = false
  const h = routeHash(r)
  if (location.hash !== h) history.pushState(null, '', h)
}

/** The project or task the user is looking at, sent with a message. */
export function currentContext(): MessageContext | undefined {
  if (ui.dropContext) return undefined
  if (ui.route.view === 'task' && ui.route.id) return { task_id: ui.route.id }
  if (ui.route.view === 'project' && ui.route.id) return { project_id: ui.route.id }
  return undefined
}

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
  await api.send(ui.current, content, currentContext())
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

/** Follows the URL hash: workbench routes and links from push notifications. */
export async function followHash(hash: string) {
  const r = parseRoute(hash)
  if (r.thread && chat.threads.some((t) => t.id === r.thread)) {
    await openThread(r.thread)
    ui.chatOpen = true
    ui.tab = 'chat'
    history.replaceState(null, '', routeHash(ui.route))
    return
  }
  ui.route = { view: r.view, id: r.id }
  if (r.view !== 'overview') ui.tab = r.view === 'inbox' ? 'inbox' : 'work'
  // Old push links (#approvals, #task=) become workbench routes.
  if (!hash.startsWith('#/') && hash !== '') history.replaceState(null, '', routeHash(ui.route))
}
