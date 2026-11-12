// Requests and streams to the brain inside channels (Go: internal/tunnel/
// rpc.go, the brain's internal/brain/serve.go). The page's REST client and
// event streams sit on top of these instead of fetch and WebSocket.
import { b64, fromUtf8, random, unb64, utf8 } from './bytes'
import { nodeAddr } from './docs'
import { ask, isUnlocked, Locked, net, onMachineMessage } from './vault'

export class BrainOffline extends Error {
  constructor() {
    super('没有在线的大脑机器：AI 暂停工作，任务会排队，等任意一台大脑机器上线')
  }
}

export interface RpcResponse {
  status: number
  body: Uint8Array
  contentType: string
}

async function brainAddr(): Promise<string> {
  if (!isUnlocked()) throw new Locked()
  const n = net()
  await n.ready()
  for (let i = 0; i < 20; i++) {
    const b = n.brain()
    if (b) return nodeAddr(b)
    await new Promise((r) => setTimeout(r, 500))
  }
  throw new BrainOffline()
}

export async function rpc(method: string, path: string, body?: Uint8Array, headers: Record<string, string> = {}): Promise<RpcResponse> {
  let lastErr: Error | null = null
  for (let attempt = 0; attempt < 3; attempt++) {
    const to = await brainAddr()
    try {
      const res = await ask(to, { k: 'rpc', method, path, header: headers, body: body ? b64(body) : '' }, 'rpc.res', 120000)
      if (typeof res.error === 'string' && res.error) {
        // The lease moved between our look and its answer: ask the new one.
        lastErr = new Error(res.error)
        await new Promise((r) => setTimeout(r, 1000))
        continue
      }
      return { status: res.status as number, body: unb64((res.body as string) || ''), contentType: (res.header as Record<string, string>)?.['Content-Type'] ?? '' }
    } catch (e) {
      lastErr = e as Error
      if (e instanceof Locked) throw e
    }
  }
  throw lastErr ?? new BrainOffline()
}

export async function rpcJSON<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<RpcResponse & { json: T }> {
  const raw = body === undefined ? undefined : utf8(JSON.stringify(body))
  const res = await rpc(method, path, raw, { 'Content-Type': 'application/json', ...headers })
  let json = {} as T
  try {
    json = JSON.parse(fromUtf8(res.body || new Uint8Array())) as T
  } catch {
    /* not JSON */
  }
  return { ...res, json }
}

/** A WebSocket-shaped stream from the brain. */
export class BrainStream {
  private id = b64(random(9))
  private to = ''
  private closed = false
  private off: (() => void) | null = null

  constructor(
    private path: string,
    private onMessage: (data: string) => void,
    private onOpen: () => void,
    private onClose: (error: string) => void,
  ) {}

  async open() {
    try {
      this.to = await brainAddr()
    } catch (e) {
      this.onClose((e as Error).message)
      return
    }
    this.off = onMachineMessage((from, m) => {
      if (from !== this.to || m.id !== this.id) return
      if (m.k === 'ws.ok') this.onOpen()
      else if (m.k === 'ws.msg') this.onMessage(fromUtf8(unb64(m.body as string)))
      else if (m.k === 'ws.close') this.finish((m.error as string) || '')
    })
    try {
      await net().send(this.to, { k: 'ws.open', id: this.id, path: this.path })
    } catch (e) {
      this.finish((e as Error).message)
    }
  }

  private finish(err: string) {
    if (this.closed) return
    this.closed = true
    this.off?.()
    this.onClose(err)
  }

  close() {
    if (this.closed) return
    if (this.to && isUnlocked()) net().send(this.to, { k: 'ws.close', id: this.id }).catch(() => {})
    this.finish('')
  }
}

/** A WebSocket look-alike over BrainStream, so the event-stream code stays as it was. */
export class BrainSocket {
  onopen: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  private s: BrainStream

  constructor(path: string) {
    this.s = new BrainStream(
      path,
      (d) => this.onmessage?.({ data: d }),
      () => this.onopen?.(),
      (err) => {
        if (err) this.onerror?.()
        this.onclose?.()
      },
    )
    void this.s.open()
  }

  close() {
    this.s.close()
  }
}
