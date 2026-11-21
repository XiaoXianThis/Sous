// The /ws event stream with automatic reconnect from the last seq.
import type { HubEvent } from './types'
import { isDirectWorkspace, workspaceJSON, WorkspaceSocket } from './workspace-transport'
import { isUnlocked } from '../trust/vault'
import type { Me } from './types'

export type StreamStatus = 'connecting' | 'online' | 'offline'

export class EventStream {
  private ws: WorkspaceSocket | null = null
  private retry = 0
  private timer: ReturnType<typeof setTimeout> | null = null
  private stopped = false

  constructor(
    private since: () => number,
    private onEvent: (ev: HubEvent) => void,
    private onStatus: (s: StreamStatus) => void,
    private onUnauthorized: () => void,
  ) {}

  start() {
    this.stopped = false
    this.open()
  }

  stop() {
    this.stopped = true
    if (this.timer) clearTimeout(this.timer)
    this.ws?.close()
  }

  private open() {
    this.onStatus('connecting')
    const ws = new WorkspaceSocket(`/ws?since=${this.since()}`)
    this.ws = ws
    let opened = false
    ws.onopen = () => {
      opened = true
      this.retry = 0
      this.onStatus('online')
    }
    ws.onmessage = (m) => {
      try {
        this.onEvent(JSON.parse(m.data))
      } catch (e) {
        console.error('bad event', e)
      }
    }
    ws.onclose = async () => {
      this.ws = null
      if (this.stopped) return
      this.onStatus('offline')
      if (isDirectWorkspace()) {
        // A socket close does not distinguish an expired session from a
        // stopped Hub. Only an explicit /me response locks the workspace.
        const me = (await workspaceJSON<Me>('GET', '/api/v1/me', undefined, {}, AbortSignal.timeout(2500)).catch(() => undefined))?.json
        if (this.stopped) return
        if (me && !me.logged_in) return this.onUnauthorized()
      } else if (!opened && !isUnlocked()) return this.onUnauthorized()
      const delay = Math.min(30000, 1000 * 2 ** this.retry++)
      this.timer = setTimeout(() => this.open(), delay)
    }
  }
}
