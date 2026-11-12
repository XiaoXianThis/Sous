// Byte helpers shared by the trust code. base64url without padding is the
// encoding of every binary field, as in apps/core/internal/trust.

export type Bytes = Uint8Array<ArrayBuffer>

export function b64(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i++) s += String.fromCharCode(b[i])
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function unb64(s: string): Bytes {
  const t = s.replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(t + '==='.slice((t.length + 3) % 4))
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

const enc = new TextEncoder()
const dec = new TextDecoder()

export const utf8 = (s: string): Bytes => enc.encode(s) as Bytes
export const fromUtf8 = (b: Uint8Array): string => dec.decode(b)

export function concat(...parts: Uint8Array[]): Bytes {
  let n = 0
  for (const p of parts) n += p.length
  const out = new Uint8Array(n)
  let o = 0
  for (const p of parts) {
    out.set(p, o)
    o += p.length
  }
  return out
}

export function u16(n: number): Bytes {
  return new Uint8Array([(n >> 8) & 0xff, n & 0xff])
}

export function u64(n: number): Bytes {
  const out = new Uint8Array(8)
  let v = BigInt(n)
  for (let i = 7; i >= 0; i--) {
    out[i] = Number(v & 0xffn)
    v >>= 8n
  }
  return out
}

export function equal(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false
  let d = 0
  for (let i = 0; i < a.length; i++) d |= a[i] ^ b[i]
  return d === 0
}

export function random(n: number): Bytes {
  const b = new Uint8Array(n)
  crypto.getRandomValues(b)
  return b
}

/** ctx, a zero byte, then the message: what every signature covers. */
export function labeled(ctx: string, msg: Uint8Array): Bytes {
  return concat(utf8(ctx), new Uint8Array([0]), msg)
}

export async function sha256(b: Uint8Array): Promise<Bytes> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', b as Bytes))
}

export const now = () => Math.floor(Date.now() / 1000)
