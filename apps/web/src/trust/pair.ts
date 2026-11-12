// Pairing strings (Go: internal/trust/pairing.go): the page makes one, the
// user pastes it into the machine's terminal. It never travels through the
// relay, so the relay cannot swap the keys the two sides exchange.
import { hmac } from '@noble/hashes/hmac.js'
import { sha256 as nsha256 } from '@noble/hashes/sha2.js'
import { b64, concat, equal, labeled, utf8, type Bytes } from './bytes'

const ALPHA = 'abcdefghijklmnopqrstuvwxyz234567'

function base32(b: Uint8Array): string {
  let bits = 0, v = 0, out = ''
  for (const x of b) {
    v = (v << 8) | x
    bits += 8
    while (bits >= 5) {
      out += ALPHA[(v >>> (bits - 5)) & 31]
      bits -= 5
    }
  }
  if (bits > 0) out += ALPHA[(v << (5 - bits)) & 31]
  return out
}

function unbase32(s: string): Bytes {
  let bits = 0, v = 0
  const out: number[] = []
  for (const c of s.toLowerCase()) {
    const i = ALPHA.indexOf(c)
    if (i < 0) throw new Error('配对串里有不认识的字符')
    v = (v << 5) | i
    bits += 5
    if (bits >= 8) {
      out.push((v >>> (bits - 8)) & 0xff)
      bits -= 8
    }
  }
  return new Uint8Array(out)
}

export interface PairCode {
  relayHash: Bytes
  identityFp: Bytes
  secret: Bytes
  exp: number
}

export const relayHash = (url: string): Bytes => nsha256(utf8(url.replace(/\/+$/, ''))).subarray(0, 8) as Bytes

export function encodePair(c: PairCode): string {
  const exp = new Uint8Array(4)
  new DataView(exp.buffer).setUint32(0, c.exp)
  return 'sous1-' + base32(concat(new Uint8Array([1]), c.relayHash, c.identityFp, c.secret, exp))
}

export function parsePair(s: string): PairCode {
  s = s.trim()
  if (!s.startsWith('sous1-')) throw new Error('不是 sous 的配对串')
  const raw = unbase32(s.slice(6))
  if (raw.length !== 61 || raw[0] !== 1) throw new Error('配对串不完整')
  return {
    relayHash: raw.slice(1, 9), identityFp: raw.slice(9, 41), secret: raw.slice(41, 57),
    exp: new DataView(raw.buffer, raw.byteOffset + 57, 4).getUint32(0),
  }
}

export const pairRoom = (secret: Uint8Array): string => b64(nsha256(labeled('sous/v1/pair-room', secret)).subarray(0, 16))

export const pairMac = (secret: Uint8Array, side: string, msg: Uint8Array): string =>
  b64(hmac(nsha256, secret, labeled('sous/v1/pair-' + side, msg)))

export const checkPairMac = (secret: Uint8Array, side: string, msg: Uint8Array, mac: string): boolean =>
  equal(utf8(pairMac(secret, side, msg)), utf8(mac))
