// HPKE (RFC 9180, base mode) with the X-Wing KEM (MLKEM768-X25519, id
// 0x647a), HKDF-SHA256 and AES-256-GCM: the same suite as Go's crypto/hpke
// in apps/core/internal/trust. X-Wing comes from @noble/post-quantum; the
// X25519 half alone keeps today's security if the ML-KEM half had a flaw.
import { ml_kem768_x25519 } from '@noble/post-quantum/hybrid.js'
import { extract, expand } from '@noble/hashes/hkdf.js'
import { sha256 } from '@noble/hashes/sha2.js'
import { concat, u16, utf8, type Bytes } from './bytes'

const KEM_ID = 0x647a
const SUITE = concat(utf8('HPKE'), u16(KEM_ID), u16(0x0001), u16(0x0002))
const ENC_SIZE = 1120

export interface KemKey {
  seed: Bytes
  pub: Bytes
}

export function kemGenerate(): KemKey {
  const seed = crypto.getRandomValues(new Uint8Array(32)) as Bytes
  return kemFromSeed(seed)
}

export function kemFromSeed(seed: Uint8Array): KemKey {
  const k = ml_kem768_x25519.keygen(seed)
  return { seed: new Uint8Array(seed) as Bytes, pub: new Uint8Array(k.publicKey) as Bytes }
}

function labeledExtract(salt: Uint8Array, label: string, ikm: Uint8Array): Bytes {
  return extract(sha256, concat(utf8('HPKE-v1'), SUITE, utf8(label), ikm), salt) as Bytes
}

function labeledExpand(prk: Uint8Array, label: string, info: Uint8Array, len: number): Bytes {
  return expand(sha256, prk, concat(u16(len), utf8('HPKE-v1'), SUITE, utf8(label), info), len) as Bytes
}

export interface HpkeContext {
  key: Bytes
  baseNonce: Bytes
  exporter: Bytes
}

function schedule(shared: Uint8Array, info: Uint8Array): HpkeContext {
  const empty = new Uint8Array(0)
  const pskIdHash = labeledExtract(empty, 'psk_id_hash', empty)
  const infoHash = labeledExtract(empty, 'info_hash', info)
  const ksc = concat(new Uint8Array([0]), pskIdHash, infoHash)
  const secret = labeledExtract(shared, 'secret', empty)
  return {
    key: labeledExpand(secret, 'key', ksc, 32),
    baseNonce: labeledExpand(secret, 'base_nonce', ksc, 12),
    exporter: labeledExpand(secret, 'exp', ksc, 32),
  }
}

export function hpkeExport(ctx: HpkeContext, label: string, len: number): Bytes {
  return labeledExpand(ctx.exporter, 'sec', utf8(label), len)
}

export function setupSender(pub: Uint8Array, info: Uint8Array): { enc: Bytes; ctx: HpkeContext } {
  const { cipherText, sharedSecret } = ml_kem768_x25519.encapsulate(pub)
  return { enc: new Uint8Array(cipherText) as Bytes, ctx: schedule(sharedSecret, info) }
}

export function setupRecipient(enc: Uint8Array, seed: Uint8Array, info: Uint8Array): HpkeContext {
  if (enc.length !== ENC_SIZE) throw new Error('bad encapsulation size')
  const shared = ml_kem768_x25519.decapsulate(enc, seed)
  return schedule(shared, info)
}

async function gcm(key: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', key as Bytes, 'AES-GCM', false, ['encrypt', 'decrypt'])
}

/** Single-shot HPKE seal: enc || ciphertext, as Go's hpke.Seal. */
export async function sealTo(pub: Uint8Array, info: string, pt: Uint8Array): Promise<Bytes> {
  const { enc, ctx } = setupSender(pub, utf8(info))
  const k = await gcm(ctx.key)
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: ctx.baseNonce }, k, pt as Bytes))
  return concat(enc, ct)
}

export async function openFrom(seed: Uint8Array, info: string, data: Uint8Array): Promise<Bytes> {
  const ctx = setupRecipient(data.subarray(0, ENC_SIZE), seed, utf8(info))
  const k = await gcm(ctx.key)
  return new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: ctx.baseNonce }, k, data.subarray(ENC_SIZE) as Bytes))
}
