import { utf8, fromUtf8, type Bytes } from '../trust/bytes'
import { BrainSocket, rpc, type RpcResponse } from '../trust/rpc'
import type { Me } from './types'

let direct = false
export const isDirectWorkspace = () => direct

/** Hosted mode is marked in the same-origin HTML by the serving Hub. Static
 * relay pages do not probe an extra endpoint or incur an extra WAN RTT. */
export const expectsHostedWorkspace = () => document.querySelector('meta[name="sous-workspace"]')?.getAttribute('content') === 'hosted'

export async function detectDirectWorkspace(): Promise<Me | undefined> {
  const hosted = expectsHostedWorkspace()
  if (hosted ? location.protocol !== 'https:' : location.hostname !== 'localhost' && location.hostname !== '127.0.0.1') return
  try {
    const res = await fetch('/api/v1/me', {
      cache: 'no-store', redirect: 'error', credentials: 'same-origin', signal: AbortSignal.timeout(hosted ? 6000 : 2500),
    })
    if (!res.ok || !res.headers.get('Content-Type')?.startsWith('application/json')) return
    const me = await res.json() as Me
    if (hosted ? me.hosted_workspace !== true : me.local_workspace !== true) return
    if (typeof me.logged_in !== 'boolean' || typeof me.has_passkey !== 'boolean') return
    direct = true
    return me
  } catch { /* network unavailable or a static relay host */ }
}

function csrf(): string {
  const key = expectsHostedWorkspace() ? '__Host-sous_csrf=' : 'sous_csrf='
  return document.cookie.split(/;\s*/).find((c) => c.startsWith(key))?.slice(key.length) ?? ''
}

export async function workspaceRequest(method: string, path: string, body?: Uint8Array, headers: Record<string, string> = {}, signal?: AbortSignal): Promise<RpcResponse> {
  if (!direct) return rpc(method, path, body, headers)
  const url = new URL(path, location.origin)
  if (url.origin !== location.origin) throw new Error('工作区请求必须留在工作区页面')
  const payload: Bytes | undefined = body ? new Uint8Array(body) : undefined
  const res = await fetch(url, {
    method, body: payload,
    headers: { ...headers, 'X-CSRF-Token': csrf() }, signal, credentials: 'same-origin', cache: 'no-store', redirect: 'error',
  })
  return { status: res.status, body: new Uint8Array(await res.arrayBuffer()), contentType: res.headers.get('Content-Type') ?? '' }
}

export async function workspaceJSON<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}, signal?: AbortSignal) {
  const result = await workspaceRequest(method, path, body === undefined ? undefined : utf8(JSON.stringify(body)), {
    'Content-Type': 'application/json', ...headers,
  }, signal)
  let json: T | undefined
  try { json = JSON.parse(fromUtf8(result.body)) as T } catch { /* may be a bodyless response */ }
  return { ...result, json }
}

export class WorkspaceSocket {
  onopen: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  private socket: BrainSocket | WebSocket

  constructor(path: string) {
    if (direct) {
      const url = new URL(path, location.origin)
      if (url.origin !== location.origin) throw new Error('工作区事件流必须留在工作区页面')
      url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
      this.socket = new WebSocket(url)
    } else {
      this.socket = new BrainSocket(path)
    }
    this.socket.onopen = () => this.onopen?.()
    this.socket.onmessage = (e: { data: unknown }) => { if (typeof e.data === 'string') this.onmessage?.({ data: e.data }) }
    this.socket.onclose = () => this.onclose?.()
    this.socket.onerror = () => this.onerror?.()
  }

  close() { this.socket.close() }
}
