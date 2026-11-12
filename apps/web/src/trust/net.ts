// The page's connection to the relay (Go: internal/tunnel). It
// authenticates with the session certificate, keeps the signed documents
// after checking them against the identity, and carries end-to-end
// encrypted channels to machines. Nothing the relay says is believed
// without a signature.
import { b64, fromUtf8, unb64, utf8, type Bytes } from './bytes'
import { InChannel, OutChannel, GapError, type Wire } from './channel'
import type { KemKey } from './hpke'
import { openDoc, peekDoc, sign, type SignKey, type Signed } from './sign'
import type { Alert, DomainKeys, Lease, Pause, Roster, RosterUpdate, Scopes } from './docs'

export type NetState = 'connecting' | 'online' | 'offline'

interface Frame {
  t: string
  rid?: string
  nonce?: string
  addr?: string
  pub?: string
  sig?: string
  cert?: Signed
  token?: string
  to?: string
  from?: string
  id?: number
  name?: string
  room?: string
  data?: unknown
  body?: unknown
  blob?: string
  error?: string
  online?: string[]
}

export function wsUrl(base: string): string {
  let u = base.replace(/\/+$/, '').replace(/^https:/, 'wss:').replace(/^http:/, 'ws:')
  if (!u.endsWith('/v1/ws')) u += '/v1/ws'
  return u
}

/** One request/response over a fresh anonymous connection (unlock, claim). */
export async function anonRequest(relay: string, f: Frame): Promise<Frame> {
  const ws = new WebSocket(wsUrl(relay))
  try {
    return await new Promise<Frame>((resolve, reject) => {
      let step = 0
      const timer = setTimeout(() => reject(new Error('中继没有应答')), 20000)
      ws.onerror = () => reject(new Error('连不上中继：' + relay))
      ws.onmessage = (m) => {
        const r = JSON.parse(m.data as string) as Frame
        if (step === 0 && r.t === 'challenge') {
          step = 1
          ws.send(JSON.stringify({ t: 'anon' }))
        } else if (step === 1 && r.t === 'welcome') {
          step = 2
          ws.send(JSON.stringify({ ...f, rid: '1' }))
        } else if (step === 2 && r.rid === '1') {
          clearTimeout(timer)
          if (r.t === 'err') reject(new Error(r.error))
          else resolve(r)
        }
      }
    })
  } finally {
    ws.close()
  }
}

export interface NetOptions {
  url: string
  addr: string
  sign: SignKey
  kem: KemKey
  cert: () => Signed
  identityPub: Bytes
  onChange: () => void
  onMessage: (from: string, msg: Record<string, unknown>) => void
}

const leaseNewer = (l: Lease, cur: Lease | null) => !cur || l.term > cur.term || (l.term === cur.term && l.holder === cur.holder && l.exp > cur.exp)

export class Net {
  state: NetState = 'connecting'
  roster: Roster | null = null
  rosterUpdate: RosterUpdate | null = null
  scopes: Scopes | null = null
  pause: Pause | null = null
  lease: Lease | null = null
  domain: DomainKeys | null = null
  domainDoc: Signed | null = null
  alerts: Record<string, Alert> = {}
  online: string[] = []

  private ws: WebSocket | null = null
  private stopped = false
  private retry = 0
  private rid = 0
  private pending = new Map<string, { resolve: (f: Frame) => void; reject: (e: Error) => void }>()
  private out = new Map<string, OutChannel>()
  private sendChain = new Map<string, Promise<void>>()
  private in = new Map<string, InChannel>()
  private rooms = new Map<string, (data: unknown) => void>()
  private readyWaiters: (() => void)[] = []
  private recvChain: Promise<void> = Promise.resolve()

  constructor(private o: NetOptions) {}

  start() {
    this.stopped = false
    this.connect()
  }

  stop() {
    this.stopped = true
    this.ws?.close()
    for (const p of this.pending.values()) p.reject(new Error('已锁定'))
    this.pending.clear()
  }

  /** Resolves once connected and the documents are loaded. */
  ready(): Promise<void> {
    if (this.state === 'online' && this.roster) return Promise.resolve()
    return new Promise((r) => this.readyWaiters.push(r))
  }

  private set(s: NetState) {
    this.state = s
    this.o.onChange()
  }

  private connect() {
    this.set('connecting')
    const ws = new WebSocket(wsUrl(this.o.url))
    this.ws = ws
    let authed = false
    ws.onmessage = (m) => {
      const f = JSON.parse(m.data as string) as Frame
      if (!authed) {
        if (f.t === 'challenge') {
          void sign(this.o.sign, 'sous/v1/relay-auth', unb64(f.nonce!)).then((sig) =>
            ws.send(JSON.stringify({ t: 'auth', addr: this.o.addr, pub: b64(this.o.sign.pub), sig: b64(sig), cert: this.o.cert() })),
          )
        } else if (f.t === 'welcome') {
          authed = true
          this.retry = 0
          void this.sync()
        } else if (f.t === 'err') {
          console.warn('relay refused', f.error)
        }
        return
      }
      this.dispatch(f)
    }
    ws.onclose = () => {
      this.ws = null
      for (const p of this.pending.values()) p.reject(new Error('与中继的连接断开了'))
      this.pending.clear()
      this.out.clear()
      if (this.stopped) return
      this.set('offline')
      const delay = Math.min(30000, 1000 * 2 ** this.retry++)
      setTimeout(() => !this.stopped && this.connect(), delay)
    }
  }

  private async sync() {
    for (const name of ['roster', 'scopes', 'pause', 'domain', 'lease']) {
      try {
        const r = await this.request({ t: 'get', name })
        if (r.body) await this.acceptDoc(name, r.body)
      } catch (e) {
        console.warn('load', name, e)
      }
    }
    for (const n of this.roster?.nodes ?? []) {
      this.request({ t: 'get', name: 'alert/' + n.id })
        .then((r) => r.body && this.acceptDoc('alert/' + n.id, r.body))
        .catch(() => {})
    }
    try {
      this.online = (await this.request({ t: 'presence' })).online ?? []
    } catch {
      /* the next presence push fills it */
    }
    this.set('online')
    for (const w of this.readyWaiters.splice(0)) w()
  }

  request(f: Frame): Promise<Frame> {
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN) return Promise.reject(new Error('连不上中继'))
    const rid = String(++this.rid)
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(rid)
        reject(new Error('中继没有应答'))
      }, 60000)
      this.pending.set(rid, {
        resolve: (r) => {
          clearTimeout(timer)
          r.t === 'err' ? reject(new Error(r.error)) : resolve(r)
        },
        reject: (e) => {
          clearTimeout(timer)
          reject(e)
        },
      })
      ws.send(JSON.stringify({ ...f, rid }))
    })
  }

  async put(name: string, body: unknown) {
    await this.request({ t: 'put', name, body })
    await this.acceptDoc(name, body)
  }

  async get(name: string): Promise<unknown> {
    return (await this.request({ t: 'get', name })).body
  }

  private dispatch(f: Frame) {
    if (f.rid && this.pending.has(f.rid)) {
      this.pending.get(f.rid)!.resolve(f)
      this.pending.delete(f.rid)
      return
    }
    switch (f.t) {
      case 'msg':
        // Messages are handled one at a time, in order (channels count).
        this.recvChain = this.recvChain.then(() => this.receive(f)).catch((e) => console.warn('message', e))
        break
      case 'doc':
        if (f.name && !f.name.includes('@')) void this.acceptDoc(f.name, f.body)
        break
      case 'presence':
        this.online = f.online ?? []
        this.o.onChange()
        break
      case 'room':
        this.rooms.get(f.room!)?.(f.data)
        break
    }
  }

  private async acceptDoc(name: string, body: unknown) {
    if (!body) return
    try {
      const id = this.o.identityPub
      if (name === 'roster') {
        const u = body as RosterUpdate
        const r = await openDoc<Roster>(id, 'roster', u.doc)
        if (r.user.sign_pub !== b64(id)) throw new Error('名册属于另一个身份')
        const seen = Number(localStorage.getItem(this.rosterKey()) ?? 0)
        if (r.v < seen) throw new Error(`中继给了旧的名册 v${r.v}（已见过 v${seen}）`)
        if (!this.roster || r.v > this.roster.v) {
          this.roster = r
          this.rosterUpdate = u
          localStorage.setItem(this.rosterKey(), String(r.v))
        }
      } else if (name === 'scopes') {
        const sc = await openDoc<Scopes>(id, 'scopes', (body as { doc: Signed }).doc)
        if (!this.scopes || sc.v > this.scopes.v) this.scopes = sc
      } else if (name === 'pause') {
        const p = await openDoc<Pause>(id, 'pause', body as Signed)
        if (!this.pause || p.v > this.pause.v) this.pause = p
      } else if (name === 'domain') {
        const d = await openDoc<DomainKeys>(id, 'domain', body as Signed)
        if (!this.domain || d.v > this.domain.v) {
          this.domain = d
          this.domainDoc = body as Signed
        }
      } else if (name === 'lease') {
        const head = peekDoc<Lease>(body as Signed)
        const n = this.roster?.nodes.find((x) => x.id === head.holder)
        if (!n?.brain) throw new Error('租约的持有者不是大脑机器')
        const l = await openDoc<Lease>(unb64(n.sign_pub), 'lease', body as Signed)
        if (leaseNewer(l, this.lease)) this.lease = l
      } else if (name.startsWith('alert/')) {
        const nid = name.slice(6)
        const n = this.roster?.nodes.find((x) => x.id === nid)
        if (!n) return
        this.alerts[nid] = await openDoc<Alert>(unb64(n.sign_pub), 'audit', body as Signed)
      } else {
        return
      }
      this.o.onChange()
    } catch (e) {
      console.warn('rejected document', name, e)
    }
  }

  private rosterKey() {
    return 'sous.rosterV:' + this.o.url
  }

  /** The machine thinking for everyone now, if its lease is live. */
  brain(): string | null {
    const l = this.lease
    if (!l || Date.now() / 1000 > l.exp) return null
    const n = this.roster?.nodes.find((x) => x.id === l.holder)
    if (!n?.brain || n.frozen) return null
    return l.holder
  }

  send(to: string, msg: unknown): Promise<void> {
    const prev = this.sendChain.get(to) ?? Promise.resolve()
    const next = prev.catch(() => {}).then(() => this.sendNow(to, msg))
    this.sendChain.set(to, next)
    return next
  }

  private async sendNow(to: string, msg: unknown) {
    let oc = this.out.get(to)
    if (!oc) {
      const id = to.startsWith('node:') ? to.slice(5) : ''
      const n = this.roster?.nodes.find((x) => x.id === id)
      if (!n) throw new Error('名册里没有这台机器')
      oc = await OutChannel.open(this.o.addr, to, this.o.sign, unb64(n.kem_pub))
      this.out.set(to, oc)
    }
    const w = await oc.seal(utf8(JSON.stringify(msg)))
    if (w.open) w.cert = this.o.cert()
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      this.out.delete(to)
      throw new Error('连不上中继')
    }
    ws.send(JSON.stringify({ t: 'send', to, data: w }))
  }

  private async receive(f: Frame) {
    const from = f.from ?? ''
    const data = f.data as Wire & { reset?: string }
    if (!data.ch) {
      if (data.reset && this.out.get(from)?.id === data.reset) this.out.delete(from)
      return
    }
    let ch = this.in.get(data.ch)
    if (!ch) {
      if (!data.open) {
        this.ws?.send(JSON.stringify({ t: 'send', to: from, data: { reset: data.ch } }))
        return
      }
      const o = InChannel.peek(data.open)
      const n = this.roster?.nodes.find((x) => 'node:' + x.id === o.from)
      if (!n || n.frozen || o.ch !== data.ch) return
      ch = await InChannel.accept(data.open, unb64(n.sign_pub), this.o.addr, this.o.kem.seed, this.o.kem.pub)
      this.in.set(data.ch, ch)
    }
    let pt: Bytes
    try {
      pt = await ch.open(data)
    } catch (e) {
      if (!(e instanceof GapError)) return
      pt = e.plaintext
    }
    this.o.onMessage(ch.from, JSON.parse(fromUtf8(pt)))
  }

  /** New channels from the next message on (they carry a renewed certificate). */
  resetChannels() {
    this.out.clear()
  }

  async openRoom(room: string, onData: (data: unknown) => void) {
    this.rooms.set(room, onData)
    await this.request({ t: 'room.open', room })
  }

  roomSend(room: string, data: unknown) {
    this.ws?.send(JSON.stringify({ t: 'room.send', room, data }))
  }

  closeRoom(room: string) {
    this.rooms.delete(room)
  }
}
