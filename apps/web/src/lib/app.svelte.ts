// Application state, shared by the components.
import { api } from './api'
import { applyLive, seedLive, type Live } from './live'
import { applyEvent, emptyState, mergeHistory, sortThreads } from './reduce'
import { runFromHistory } from './runs'
import { home, parseRoute, routeHash, type Route } from './route'
import { EventStream, type StreamStatus } from './stream'
import type { HubEvent, Inbox, MessageContext, Overview, Run } from './types'

export const chat = $state(emptyState())

export const ui = $state({
  status: 'connecting' as StreamStatus,
  current: '' as string,
  loaded: {} as Record<string, boolean>,
  error: '' as string,
  /** The workbench filter and the sheet on top of it. */
  route: { ...home } as Route,
  /** The chat panel is open (desktop) or the chat tab is shown (phone). */
  chatOpen: true,
  /** The settings tab open in the full-screen settings, or ''. */
  settings: '' as string,
  /** Phone: which bottom tab is shown. */
  tab: 'chat' as 'chat' | 'work',
  /** The workbench context sent with the next message; cleared by the user. */
  dropContext: false,
  /** The Run whose detail sheet is open, '' when none. */
  runSheet: '' as string,
  /** The project a new task is being created in, '' when none. */
  newTask: '' as string,
})

/** Everything the workbench draws; reloaded when tasks, machines or approvals change. */
export const wb = $state({
  data: null as Overview | null,
  inbox: null as Inbox | null,
  /** Live activities of running Runs, by id. */
  live: {} as Live,
  error: '' as string,
})

let overviewLoading: Promise<void> | null = null
export function loadOverview(): Promise<void> {
  overviewLoading ??= api
    .overview()
    .then((d) => {
      wb.data = d
      wb.error = ''
      pruneLive(d.runs)
      void seedLogs(d.runs)
    })
    .catch((e) => {
      wb.error = (e as Error).message
    })
    .finally(() => (overviewLoading = null))
  return overviewLoading
}

let inboxLoading: Promise<void> | null = null
export function loadInbox(): Promise<void> {
  inboxLoading ??= api
    .inbox()
    .then((d) => {
      wb.inbox = d
    })
    .catch(() => {})
    .finally(() => (inboxLoading = null))
  return inboxLoading
}

/** Drops feeds for Runs that are no longer active, keeping the open one. */
function pruneLive(runs: Run[]) {
  const keep = new Set(runs.map((r) => r.id))
  for (const id of Object.keys(wb.live)) {
    if (!keep.has(id) && id !== ui.runSheet) delete wb.live[id]
  }
  const ids = Object.keys(wb.live)
  for (const id of ids.slice(0, Math.max(0, ids.length - 50))) {
    if (id !== ui.runSheet) delete wb.live[id]
  }
}

const seeded = new Set<string>()

/** Shows the last lines of a running Run after a reload; the log has no thinking. */
async function seedLogs(runs: Run[]) {
  for (const r of runs) {
    if (r.status !== 'running' || seeded.has(r.id) || (wb.live[r.id]?.length ?? 0) > 0) continue
    seeded.add(r.id)
    try {
      seedLive(wb.live, r.id, await api.runText(r.id, 'log'))
    } catch {
      // The live stream still fills the card; a missing log is not an error.
    }
  }
}

/** Shows a page of the workbench and records it in the URL. */
let sheetFocus: HTMLElement | null = null

export function go(r: Route) {
  const wasSheet = ui.route.view === 'task' || ui.route.view === 'project'
  const isSheet = r.view === 'task' || r.view === 'project'
  if (!wasSheet && isSheet && typeof document !== 'undefined') sheetFocus = document.activeElement as HTMLElement
  ui.route = { ...r }
  ui.tab = 'work'
  ui.dropContext = false
  const h = routeHash(r)
  if (location.hash !== h) history.pushState(isSheet ? { sheet: true } : null, '', h)
  if (wasSheet && !isSheet) restoreSheetFocus()
}

/** Closes the sheet on top: back when it was opened from the workbench. */
export function closeSheet() {
  if (history.state?.sheet) {
    history.back()
    return
  }
  ui.route = { ...home }
  ui.tab = 'work'
  history.replaceState(null, '', routeHash(ui.route))
  restoreSheetFocus()
}

function restoreSheetFocus() {
  const el = sheetFocus
  sheetFocus = null
  if (el && typeof el.focus === 'function') requestAnimationFrame(() => el.focus())
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
  const feed = liveFeed()
  stream = new EventStream(
    () => chat.lastSeq,
    (ev) => {
      feed(ev)
      if (applyEvent(chat, ev)) maybeNotify(ev)
    },
    (s) => (ui.status = s),
    onLoggedOut,
  )
  stream.start()
}

/** Buffers streaming activity so token chunks do not re-render the grid. */
function liveFeed() {
  let buf: HubEvent[] = []
  let timer: ReturnType<typeof setTimeout> | null = null
  const flush = () => {
    timer = null
    const events = buf
    buf = []
    for (const ev of events) applyLive(wb.live, ev)
  }
  return (ev: HubEvent) => {
    if (ev.type !== 'run.output' && ev.type !== 'run.update') return
    buf.push(ev)
    if (!timer) timer = setTimeout(flush, 100)
  }
}

export function stopSession() {
  stream?.stop()
  stream = null
  Object.assign(chat, emptyState())
  ui.loaded = {}
  ui.current = ''
  ui.runSheet = ''
  ui.newTask = ''
  wb.live = {}
  wb.inbox = null
  seeded.clear()
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
  ui.runSheet = ''
  ui.newTask = ''
  const r = parseRoute(hash)
  if (r.thread && chat.threads.some((t) => t.id === r.thread)) {
    await openThread(r.thread)
    ui.chatOpen = true
    ui.tab = 'chat'
    history.replaceState(null, '', routeHash(ui.route))
    return
  }
  const wasSheet = ui.route.view === 'task' || ui.route.view === 'project'
  ui.route = { view: r.view, id: r.id }
  // The workbench is always the page; inbox is just its attention filter.
  ui.tab = 'work'
  if (wasSheet && r.view !== 'task' && r.view !== 'project') restoreSheetFocus()
  // Old push links (#approvals, #task=) become workbench routes.
  if (!hash.startsWith('#/') && hash !== '') history.replaceState(null, '', routeHash(ui.route))
}
