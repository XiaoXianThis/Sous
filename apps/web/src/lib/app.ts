// Application state, shared by the components. The state objects are plain and
// mutable; every change calls emit() and subscribed components re-render.
import { useSyncExternalStore } from 'react'
import { api } from './api'
import { applyLive, seedLive, type Live } from './live'
import { applyEvent, emptyState, mergeHistory, sortThreads, type ChatState } from './reduce'
import { runFromHistory } from './runs'
import { home, parseRoute, routeHash, type Route } from './route'
import { EventStream, type StreamStatus } from './stream'
import type { HubEvent, Inbox, MessageContext, Overview, Run, Thread } from './types'

/** A mutable state object plus a version subscribers watch. */
export class Store<T extends object> {
  readonly state: T
  private listeners = new Set<() => void>()
  private version = 0

  constructor(initial: T) {
    this.state = initial
  }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  private snapshot = () => this.version

  /** Tells subscribers that the state changed; React batches the renders. */
  emit() {
    this.version++
    for (const listener of this.listeners) listener()
  }

  use(): T {
    useSyncExternalStore(this.subscribe, this.snapshot)
    return this.state
  }
}

export const chat = new Store<ChatState>(emptyState())

interface UIState {
  status: StreamStatus
  current: string
  loaded: Record<string, boolean>
  error: string
  /** The workbench filter and the sheet on top of it. */
  route: Route
  /** The chat panel is open (desktop) or the chat tab is shown (phone). */
  chatOpen: boolean
  /** The settings tab open in the full-screen settings, or ''. */
  settings: string
  /** Phone: which bottom tab is shown. */
  tab: 'chat' | 'work'
  /** The workbench context sent with the next message; cleared by the user. */
  dropContext: boolean
  /** The Run whose detail sheet is open, '' when none. */
  runSheet: string
  /** The project a new task is being created in, '' when none. */
  newTask: string
  /** The chat shown in each project's chat panel, by project id. */
  projectThread: Record<string, string>
  /** A project whose dialog should open (a link to its chat); '' when none. */
  openProject: string
}

export const ui = new Store<UIState>({
  status: 'connecting',
  current: '',
  loaded: {},
  error: '',
  route: { ...home },
  chatOpen: true,
  settings: '',
  tab: 'chat',
  dropContext: false,
  runSheet: '',
  newTask: '',
  projectThread: {},
  openProject: '',
})

interface WBState {
  /** Everything the workbench draws; reloaded when tasks, machines or approvals change. */
  data: Overview | null
  inbox: Inbox | null
  /** Live activities of running Runs, by id. */
  live: Live
  error: string
}

export const wb = new Store<WBState>({ data: null, inbox: null, live: {}, error: '' })

export const useChat = () => chat.use()
export const useUi = () => ui.use()
export const useWb = () => wb.use()

let overviewLoading: Promise<void> | null = null
export function loadOverview(): Promise<void> {
  overviewLoading ??= api
    .overview()
    .then((d) => {
      wb.state.data = d
      wb.state.error = ''
      pruneLive(d.runs)
      wb.emit()
      void seedLogs(d.runs)
    })
    .catch((e) => {
      wb.state.error = (e as Error).message
      wb.emit()
    })
    .finally(() => (overviewLoading = null))
  return overviewLoading
}

let inboxLoading: Promise<void> | null = null
export function loadInbox(): Promise<void> {
  inboxLoading ??= api
    .inbox()
    .then((d) => {
      wb.state.inbox = d
      wb.emit()
    })
    .catch(() => {})
    .finally(() => (inboxLoading = null))
  return inboxLoading
}

/** Drops feeds for Runs that are no longer active, keeping the open one. */
function pruneLive(runs: Run[]) {
  const keep = new Set(runs.map((r) => r.id))
  for (const id of Object.keys(wb.state.live)) {
    if (!keep.has(id) && id !== ui.state.runSheet) delete wb.state.live[id]
  }
  const ids = Object.keys(wb.state.live)
  for (const id of ids.slice(0, Math.max(0, ids.length - 50))) {
    if (id !== ui.state.runSheet) delete wb.state.live[id]
  }
}

const seeded = new Set<string>()

/** Shows the last lines of a running Run after a reload; the log has no thinking. */
async function seedLogs(runs: Run[]) {
  for (const r of runs) {
    if (r.status !== 'running' || seeded.has(r.id) || (wb.state.live[r.id]?.length ?? 0) > 0) continue
    seeded.add(r.id)
    try {
      seedLive(wb.state.live, r.id, await api.runText(r.id, 'log'))
      wb.emit()
    } catch {
      // The live stream still fills the card; a missing log is not an error.
    }
  }
}

/** Shows a page of the workbench and records it in the URL. */
let sheetFocus: HTMLElement | null = null

export function go(r: Route) {
  const wasSheet = ui.state.route.view === 'task' || ui.state.route.view === 'project'
  const isSheet = r.view === 'task' || r.view === 'project'
  if (!wasSheet && isSheet && typeof document !== 'undefined') sheetFocus = document.activeElement as HTMLElement
  ui.state.route = { ...r }
  ui.state.tab = 'work'
  ui.state.dropContext = false
  ui.emit()
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
  ui.state.route = { ...home }
  ui.state.tab = 'work'
  history.replaceState(null, '', routeHash(ui.state.route))
  ui.emit()
  restoreSheetFocus()
}

function restoreSheetFocus() {
  const el = sheetFocus
  sheetFocus = null
  if (el && typeof el.focus === 'function') requestAnimationFrame(() => el.focus())
}

/** The project or task the user is looking at, sent with a message. */
export function currentContext(): MessageContext | undefined {
  if (ui.state.dropContext) return undefined
  if (ui.state.route.view === 'task' && ui.state.route.id) return { task_id: ui.state.route.id }
  if (ui.state.route.view === 'project' && ui.state.route.id) return { project_id: ui.state.route.id }
  return undefined
}

let stream: EventStream | null = null

export async function startSession(onLoggedOut: () => void) {
  const [{ threads }] = await Promise.all([api.threads()])
  chat.state.threads = sortThreads(threads)
  if (!ui.state.current || !threads.some((t) => t.id === ui.state.current)) {
    ui.state.current = threads[0]?.id ?? ''
  }
  chat.emit()
  ui.emit()
  if (ui.state.current) await openThread(ui.state.current)
  stream?.stop()
  const feed = liveFeed()
  stream = new EventStream(
    () => chat.state.lastSeq,
    (ev) => {
      feed(ev)
      if (applyEvent(chat.state, ev)) maybeNotify(ev)
      chat.emit()
      wb.emit()
    },
    (s) => {
      ui.state.status = s
      ui.emit()
    },
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
    for (const ev of events) applyLive(wb.state.live, ev)
    wb.emit()
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
  Object.assign(chat.state, emptyState())
  ui.state.loaded = {}
  ui.state.current = ''
  ui.state.runSheet = ''
  ui.state.newTask = ''
  ui.state.projectThread = {}
  for (const k of Object.keys(projectLoads)) delete projectLoads[k]
  wb.state.live = {}
  wb.state.inbox = null
  seeded.clear()
  chat.emit()
  ui.emit()
  wb.emit()
}

/** Shows a thread: a general one in the chat column, a project chat in its project's dialog. */
export async function openThread(id: string) {
  let t = chat.state.threads.find((x) => x.id === id)
  if (!t) {
    try {
      t = await api.thread(id)
      addThread(t)
    } catch {
      return
    }
  }
  if (t.project_id) {
    ui.state.projectThread[t.project_id] = id
    ui.state.openProject = t.project_id
  } else {
    ui.state.current = id
  }
  ui.emit()
  await loadThread(id)
}

function addThread(t: Thread) {
  if (!chat.state.threads.some((x) => x.id === t.id)) chat.state.threads.push(t)
  sortThreads(chat.state.threads)
  chat.emit()
}

/** Loads a thread's history and Runs once. */
export async function loadThread(id: string) {
  if (!id || ui.state.loaded[id]) return
  const [page, { runs }] = await Promise.all([api.messages(id), api.runs(id)])
  mergeHistory(chat.state, id, page.messages)
  const details = await Promise.all(runs.filter((r) => !chat.state.runs[r.id]).map((r) => api.run(r.id)))
  for (const d of details) {
    if (!chat.state.runs[d.run.id]) chat.state.runs[d.run.id] = runFromHistory(d.run, d.events, d.approvals)
  }
  // Start the stream after what the page already covers.
  if (chat.state.lastSeq === 0) chat.state.lastSeq = page.last_seq
  ui.state.loaded[id] = true
  chat.emit()
  ui.emit()
}

export async function newThread() {
  const t = await api.createThread('新对话')
  addThread(t)
  ui.state.loaded[t.id] = true
  ui.state.current = t.id
  ui.emit()
  return t
}

export async function send(content: string, attachments?: string[]) {
  await api.send(ui.state.current, content, currentContext(), attachments)
}

/** Loads a project's chats and shows the latest one (none until the first message). */
const projectLoads: Record<string, Promise<void>> = {}
export function loadProjectThreads(projectId: string): Promise<void> {
  projectLoads[projectId] ??= api.threads(projectId).then(async ({ threads }) => {
    for (const t of threads) addThread(t)
    if (!ui.state.projectThread[projectId] && threads[0]) {
      ui.state.projectThread[projectId] = threads[0].id
      ui.emit()
    }
    await loadThread(ui.state.projectThread[projectId] ?? '')
  }).catch(() => {
    delete projectLoads[projectId]
  })
  return projectLoads[projectId]
}

export async function pickProjectThread(projectId: string, id: string) {
  ui.state.projectThread[projectId] = id
  ui.emit()
  await loadThread(id)
}

/** Starts a fresh chat in a project. */
export async function newProjectThread(projectId: string, title: string) {
  const t = await api.createThread(title, projectId)
  addThread(t)
  ui.state.loaded[t.id] = true
  ui.state.projectThread[projectId] = t.id
  ui.emit()
  return t
}

/** Sends in a project's chat, creating it on the first message. */
export async function sendToProject(projectId: string, title: string, content: string, context?: MessageContext, attachments?: string[]) {
  let id = ui.state.projectThread[projectId]
  if (!id) id = (await newProjectThread(projectId, title)).id
  await api.send(id, content, context, attachments)
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
    if (ev.thread_id) void openThread(ev.thread_id)
  }
}

/** Follows the URL hash: workbench routes and links from push notifications. */
export async function followHash(hash: string) {
  ui.state.runSheet = ''
  ui.state.newTask = ''
  ui.emit()
  const r = parseRoute(hash)
  if (r.thread) {
    await openThread(r.thread)
    if (!chat.state.threads.find((t) => t.id === r.thread)?.project_id) {
      ui.state.chatOpen = true
      ui.state.tab = 'chat'
    }
    ui.emit()
    history.replaceState(null, '', routeHash(ui.state.route))
    return
  }
  const wasSheet = ui.state.route.view === 'task' || ui.state.route.view === 'project'
  ui.state.route = { view: r.view, id: r.id }
  // The workbench is always the page; inbox is just its attention filter.
  ui.state.tab = 'work'
  ui.emit()
  if (wasSheet && r.view !== 'task' && r.view !== 'project') restoreSheetFocus()
  // Old push links (#approvals, #task=) become workbench routes.
  if (!hash.startsWith('#/') && hash !== '') history.replaceState(null, '', routeHash(ui.state.route))
}
