// One-way encrypted channels between addresses on the relay (Go:
// internal/trust/channel.go). The sender encapsulates a fresh key to the
// recipient's X-Wing key and signs the opening; messages are AES-256-GCM
// with the sequence number as nonce, so replays and reordering fail.
import { b64, concat, u64, unb64, utf8, now, random, type Bytes } from './bytes'
import { hpkeExport, setupRecipient, setupSender } from './hpke'
import { openDoc, peekDoc, signDoc, type SignKey, type Signed } from './sign'

export interface Wire {
  open?: Signed
  cert?: Signed
  ch: string
  seq: number
  ct: string
}

export interface ChanOpen {
  t: 'chan'
  ch: string
  from: string
  to: string
  to_key: string
  enc: string
  iat: number
}

export const CHAN_OPEN_TTL = 600
const SKEW = 120

const info = (ch: string, from: string, to: string) => utf8(`sous/v1/chan\x00${ch}\x00${from}\x00${to}`)
const nonce = (seq: number) => concat(new Uint8Array(4), u64(seq))
const aad = (ch: string, seq: number) => concat(utf8(`sous/v1/msg\x00${ch}\x00`), u64(seq))

async function aesKey(raw: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', raw as Bytes, 'AES-GCM', false, ['encrypt', 'decrypt'])
}

async function keyIdOf(pub: Uint8Array): Promise<string> {
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', pub as Bytes))
  return b64(h.subarray(0, 12))
}

export class OutChannel {
  private seq = 0
  private constructor(readonly id: string, readonly opening: Signed, private key: CryptoKey) {}

  static async open(from: string, to: string, signer: SignKey, recipientKem: Uint8Array): Promise<OutChannel> {
    const ch = b64(random(16))
    const { enc, ctx } = setupSender(recipientKem, info(ch, from, to))
    const key = await aesKey(hpkeExport(ctx, 'sous/v1/chan-key', 32))
    const o: ChanOpen = { t: 'chan', ch, from, to, to_key: await keyIdOf(recipientKem), enc: b64(enc), iat: now() }
    return new OutChannel(ch, await signDoc(signer, o), key)
  }

  async seal(pt: Uint8Array): Promise<Wire> {
    const seq = ++this.seq
    const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce(seq), additionalData: aad(this.id, seq) }, this.key, pt as Bytes))
    const w: Wire = { ch: this.id, seq, ct: b64(ct) }
    if (seq === 1) w.open = this.opening
    return w
  }
}

export class GapError extends Error {
  constructor(readonly plaintext: Bytes) {
    super('messages missing before this one')
  }
}

export class InChannel {
  private last = 0
  private constructor(readonly id: string, readonly from: string, private key: CryptoKey) {}

  static peek(open: Signed): ChanOpen {
    return peekDoc<ChanOpen>(open)
  }

  static async accept(open: Signed, senderPub: Uint8Array, me: string, mineSeed: Uint8Array, minePub: Uint8Array, at = now()): Promise<InChannel> {
    const o = await openDoc<ChanOpen>(senderPub, 'chan', open)
    if (o.to !== me || o.to_key !== (await keyIdOf(minePub))) throw new Error('channel is for someone else')
    if (o.iat > at + SKEW || o.iat < at - CHAN_OPEN_TTL - SKEW) throw new Error('stale channel opening')
    const ctx = setupRecipient(unb64(o.enc), mineSeed, info(o.ch, o.from, o.to))
    return new InChannel(o.ch, o.from, await aesKey(hpkeExport(ctx, 'sous/v1/chan-key', 32)))
  }

  async open(w: Wire): Promise<Bytes> {
    if (w.ch !== this.id) throw new Error('wrong channel')
    if (w.seq <= this.last) throw new Error('replayed message')
    let pt: Bytes
    try {
      pt = new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonce(w.seq), additionalData: aad(this.id, w.seq) }, this.key, unb64(w.ct)))
    } catch {
      throw new Error('message does not decrypt')
    }
    const gap = w.seq !== this.last + 1
    this.last = w.seq
    if (gap) throw new GapError(pt)
    return pt
  }
}
