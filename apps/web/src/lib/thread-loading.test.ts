import { afterEach, expect, it, vi } from 'vitest'
import { api } from './api'
import { chat, loadRunHistory, loadThread, startSession, stopSession, ui } from './app'
import { emptyState } from './reduce'
import type { Run, Thread } from './types'

const feed = vi.hoisted(() => ({ starts: [] as number[] }))
vi.mock('./stream', () => ({
  EventStream: class {
    constructor(private since: () => number) {}
    start() { feed.starts.push(this.since()) }
    stop() {}
  },
}))

afterEach(() => {
  stopSession()
  feed.starts = []
  vi.restoreAllMocks()
})

it('shares a thread load and bounds history requests on the relay tunnel', async () => {
  Object.assign(chat.state, emptyState())
  ui.state.loaded = {}
  const runs = Array.from({ length: 6 }, (_, i) => ({ id: `history-${i}`, status: 'running' } as Run))
  vi.spyOn(api, 'messages').mockResolvedValue({ messages: [], last_seq: 0 })
  vi.spyOn(api, 'runs').mockResolvedValue({ runs })
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  let active = 0
  let peak = 0
  const details = vi.spyOn(api, 'run').mockImplementation(async (id) => {
    peak = Math.max(peak, ++active)
    await gate
    active--
    return { run: runs.find((r) => r.id === id)!, events: [], approvals: [] }
  })

  const first = loadThread('history')
  const second = loadThread('history')
  expect(second).toBe(first)
  await vi.waitFor(() => expect(details).toHaveBeenCalledTimes(2))
  release()
  await first
  expect(details).toHaveBeenCalledTimes(6)
  expect(peak).toBe(2)
  expect(ui.state.loaded.history).toBe(true)
})

it('opens the live stream only after the history cursor is known', async () => {
  Object.assign(chat.state, emptyState())
  ui.state.current = ''
  ui.state.loaded = {}
  vi.spyOn(api, 'threads').mockResolvedValue({ threads: [{ id: 'startup', kind: 'main' } as Thread] })
  vi.spyOn(api, 'runs').mockResolvedValue({ runs: [] })
  let release!: (page: { messages: []; last_seq: number }) => void
  vi.spyOn(api, 'messages').mockImplementation(() => new Promise((resolve) => { release = resolve }))
  const starting = startSession(() => {})
  await vi.waitFor(() => expect(api.messages).toHaveBeenCalled())
  expect(feed.starts).toEqual([])
  release({ messages: [], last_seq: 41328 })
  await starting
  expect(feed.starts).toEqual([41328])
})

it('loads completed run processes on demand instead of on entry', async () => {
  Object.assign(chat.state, emptyState())
  ui.state.loaded = {}
  const run = { id: 'completed', status: 'succeeded' } as Run
  vi.spyOn(api, 'messages').mockResolvedValue({ messages: [], last_seq: 100 })
  vi.spyOn(api, 'runs').mockResolvedValue({ runs: [run] })
  const details = vi.spyOn(api, 'run').mockResolvedValue({ run, events: [], approvals: [] })
  await loadThread('lazy-history')
  expect(details).not.toHaveBeenCalled()
  expect(chat.state.runs.completed.historyLoaded).toBe(false)
  const first = loadRunHistory(run.id)
  expect(loadRunHistory(run.id)).toBe(first)
  await first
  expect(details).toHaveBeenCalledTimes(1)
  expect(chat.state.runs.completed.historyLoaded).toBe(true)
})
