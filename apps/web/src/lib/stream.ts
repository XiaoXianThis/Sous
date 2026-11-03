// The /ws event stream with automatic reconnect from the last seq.
import type { HubEvent } from './types'

export type StreamStatus = 'connecting' | 'online' | 'offline'

export class EventStream {
  private ws: WebSocket | null = null
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
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${proto}//${location.host}/ws?since=${this.since()}`)
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
      if (!opened) {
        // The handshake failed: maybe the session expired.
        const res = await fetch('/api/v1/me').then((r) => r.json()).catch(() => null)
        if (res && !res.logged_in) return this.onUnauthorized()
      }
      const delay = Math.min(30000, 1000 * 2 ** this.retry++)
      this.timer = setTimeout(() => this.open(), delay)
    }
  }
}
