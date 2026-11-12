// ECDSA P-256 signatures over SHA-256 of a labeled message, r||s encoded:
// the same as Go's trust.SignKey. Keys made here stay non-extractable.
import { b64, labeled, unb64, utf8, type Bytes } from './bytes'

const ALG = { name: 'ECDSA', namedCurve: 'P-256' } as const
const SIG = { name: 'ECDSA', hash: 'SHA-256' } as const

export interface SignKey {
  priv: CryptoKey
  pub: Bytes // uncompressed point, 65 bytes
}

export async function signGenerate(extractable = false): Promise<SignKey> {
  const kp = await crypto.subtle.generateKey(ALG, extractable, ['sign', 'verify'])
  const pub = new Uint8Array(await crypto.subtle.exportKey('raw', kp.publicKey))
  return { priv: kp.privateKey, pub }
}

/** Imports a private key from its JWK (from an unwrapped identity). */
export async function signFromJwk(jwk: JsonWebKey): Promise<SignKey> {
  const priv = await crypto.subtle.importKey('jwk', jwk, ALG, false, ['sign'])
  const pubJwk = { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, ext: true }
  const pk = await crypto.subtle.importKey('jwk', pubJwk, ALG, true, ['verify'])
  return { priv, pub: new Uint8Array(await crypto.subtle.exportKey('raw', pk)) }
}

export async function sign(k: SignKey, ctx: string, msg: Uint8Array): Promise<Bytes> {
  return new Uint8Array(await crypto.subtle.sign(SIG, k.priv, labeled(ctx, msg)))
}

export async function verify(pub: Uint8Array, ctx: string, msg: Uint8Array, sig: Uint8Array): Promise<boolean> {
  try {
    const k = await crypto.subtle.importKey('raw', pub as Bytes, ALG, false, ['verify'])
    return await crypto.subtle.verify(SIG, k, sig as Bytes, labeled(ctx, msg))
  } catch {
    return false
  }
}

/** A short stable id of a public key (Go: trust.KeyID). */
export async function keyId(pub: Uint8Array): Promise<string> {
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', pub as Bytes))
  return b64(h.subarray(0, 12))
}

// Signed documents: the signature covers the payload bytes as sent.

export interface Signed {
  p: string
  k: string
  s: string
}

const docCtx = (t: string) => 'sous/v1/doc/' + t

export async function signDoc<T extends { t: string }>(k: SignKey, payload: T): Promise<Signed> {
  const raw = utf8(JSON.stringify(payload))
  return { p: b64(raw), k: await keyId(k.pub), s: b64(await sign(k, docCtx(payload.t), raw)) }
}

export function payloadOf(s: Signed): Bytes {
  return unb64(s.p)
}

export function peekDoc<T>(s: Signed): T {
  return JSON.parse(new TextDecoder().decode(unb64(s.p))) as T
}

/** Verifies that pub signed s as type t, and returns the payload. */
export async function openDoc<T>(pub: Uint8Array, t: string, s: Signed): Promise<T> {
  const raw = unb64(s.p)
  if (s.k !== (await keyId(pub))) throw new Error('signed by another key')
  if (!(await verify(pub, docCtx(t), raw, unb64(s.s)))) throw new Error('bad signature')
  const v = JSON.parse(new TextDecoder().decode(raw)) as T & { t: string }
  if (v.t !== t) throw new Error(`document type ${v.t}, want ${t}`)
  return v
}

export async function docHash(s: Signed): Promise<string> {
  const h = new Uint8Array(await crypto.subtle.digest('SHA-256', unb64(s.p)))
  return b64(h)
}
