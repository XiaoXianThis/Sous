// The vault: everything the user's identity can do, alive only while the
// page is unlocked (decision 0030). Unlocking takes Face ID, a fingerprint
// or the Windows PIN once; the keys then live in memory as non-extractable
// WebCrypto keys and vanish on lock, on quit, or after the idle timeout.
import { useSyncExternalStore } from 'react'
import { b64, fromUtf8, now, random, sha256, unb64, utf8, type Bytes } from './bytes'
import { kemFromSeed, kemGenerate, openFrom, sealTo, type KemKey } from './hpke'
import { keyId, signDoc, signFromJwk, signGenerate, payloadOf, type SignKey, type Signed } from './sign'
import { assertCritical, createPasskey, prfUnlock } from './webauthn'
import { anonRequest, Net } from './net'
import {
  defaultScopes, nodeAddr, rosterNeedsAssertion, sessionAddr,
  type Assertion, type DomainKeys, type Permit, type Roster, type RosterNode, type Scopes, type SessionCert,
} from './docs'
import { checkPairMac, encodePair, pairMac, pairRoom, relayHash } from './pair'
import { extract, expand } from '@noble/hashes/hkdf.js'
import { sha256 as nsha256 } from '@noble/hashes/sha2.js'

// --- settings kept on this device -------------------------------------------

const RELAY_KEY = 'sous.relay'
const LOCK_KEY = 'sous.lockAfter'

/** The relay this page talks to: set at first setup, or at build time. */
export function relayUrl(): string {
  return localStorage.getItem(RELAY_KEY) || (import.meta.env.VITE_SOUS_RELAY as string | undefined) || ''
}

export function setRelayUrl(u: string) {
  localStorage.setItem(RELAY_KEY, u.trim().replace(/\/+$/, ''))
}

/** Lock after this many seconds without input; 0 never locks. */
export const lockChoices = [
  { s: 60, text: '1 分钟' }, { s: 300, text: '5 分钟' }, { s: 900, text: '15 分钟' },
  { s: 3600, text: '1 小时' }, { s: 14400, text: '4 小时' }, { s: 0, text: '永不' },
]

export function lockAfter(): number {
  const v = localStorage.getItem(LOCK_KEY)
  return v === null ? 900 : Number(v)
}

export function setLockAfter(s: number) {
  localStorage.setItem(LOCK_KEY, String(s))
  armIdle()
  emit()
}

// --- state ----------------------------------------------------------------

interface Unlocked {
  relay: string
  credId: string
  idSign: SignKey
  idKem: KemKey
  sid: string
  sSign: SignKey
  sKem: KemKey
  cert: Signed
  certIat: number
  certExp: number
  net: Net
}

let u: Unlocked | null = null
let version = 0
const subs = new Set<() => void>()

function emit() {
  version++
  for (const s of subs) s()
}

export function useVault(): number {
  return useSyncExternalStore(
    (fn) => {
      subs.add(fn)
      return () => subs.delete(fn)
    },
    () => version,
  )
}

export function isUnlocked(): boolean {
  return u !== null
}

export function net(): Net {
  if (!u) throw new Locked()
  return u.net
}

export class Locked extends Error {
  constructor() {
    super('已锁定')
  }
}

export function sid(): string {
  return u?.sid ?? ''
}

export function roster(): Roster | null {
  return u?.net.roster ?? null
}

// --- identity -------------------------------------------------------------

interface Bundle {
  t: 'identity'
  sign_pub: string
  kem_pub: string
  wraps: Record<string, { salt: string; ct: string }>
}

async function wrapKey(prf: Uint8Array, salt: Uint8Array): Promise<CryptoKey> {
  const k = expand(nsha256, extract(nsha256, prf, salt), utf8('sous/v1/identity-wrap'), 32)
  return crypto.subtle.importKey('raw', k as Bytes, 'AES-GCM', false, ['encrypt', 'decrypt'])
}

async function wrapIdentity(prf: Uint8Array, secret: { sign: JsonWebKey; kem: string }) {
  const salt = random(16)
  const k = await wrapKey(prf, salt)
  const iv = random(12)
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, k, utf8(JSON.stringify(secret))))
  return { salt: b64(salt), ct: b64(iv) + '.' + b64(ct) }
}

async function unwrapIdentity(prf: Uint8Array, w: { salt: string; ct: string }): Promise<{ sign: JsonWebKey; kem: string }> {
  const k = await wrapKey(prf, unb64(w.salt))
  const [iv, ct] = w.ct.split('.')
  try {
    const pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: unb64(iv) }, k, unb64(ct))
    return JSON.parse(fromUtf8(new Uint8Array(pt)))
  } catch {
    throw new Error('这个 Passkey 解不开身份（它不属于这个中继的主人）')
  }
}

function deviceLabel(): string {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return 'iPhone'
  if (/iPad/.test(ua)) return 'iPad'
  if (/Android/.test(ua)) return 'Android'
  if (/Windows/.test(ua)) return 'Windows'
  if (/Mac/.test(ua)) return 'Mac'
  return '浏览器'
}

/**
 * First setup: makes the passkey and the identity, and claims the relay
 * with the token `sous-relay init` printed. Leaves the vault unlocked.
 */
export async function setup(relay: string, claimToken: string) {
  relay = relay.trim().replace(/\/+$/, '')
  const pk = await createPasskey(deviceLabel())
  const { credId, prf } = await prfUnlock([pk.cred_id])
  const idSignX = await signGenerate(true)
  const jwk = await crypto.subtle.exportKey('jwk', idSignX.priv)
  const idKem = kemGenerate()
  const bundle: Bundle = { t: 'identity', sign_pub: b64(idSignX.pub), kem_pub: b64(idKem.pub), wraps: {} }
  bundle.wraps[credId] = await wrapIdentity(prf, { sign: jwk, kem: b64(idKem.seed) })
  const idSign = await signFromJwk(jwk) // non-extractable from here on
  const r: Roster = {
    t: 'roster', v: 1, iat: now(), rp_id: location.hostname, origins: [location.origin], relay,
    user: { sign_pub: b64(idSign.pub), kem_pub: b64(idKem.pub), passkeys: [pk] }, nodes: [],
  }
  const doc = await signDoc(idSign, r)
  await anonRequest(relay, { t: 'claim', token: claimToken.trim(), body: { roster: { doc }, bundle } })
  setRelayUrl(relay)
  await open(relay, credId, idSign, idKem)
}

/** Unlocks with the passkey (Face ID / fingerprint / PIN). */
export async function unlock() {
  const relay = relayUrl()
  if (!relay) throw new Error('还没有设置中继地址')
  const r = await anonRequest(relay, { t: 'get', name: 'identity' })
  const bundle = r.body as Bundle | undefined
  if (!bundle?.wraps) throw new Error('这个中继还没有完成首次设置')
  const { credId, prf } = await prfUnlock(Object.keys(bundle.wraps))
  const w = bundle.wraps[credId]
  if (!w) throw new Error('这个 Passkey 没有登记在这个中继上')
  const secret = await unwrapIdentity(prf, w)
  const idSign = await signFromJwk(secret.sign)
  if (b64(idSign.pub) !== bundle.sign_pub) throw new Error('身份包被改动过')
  const idKem = kemFromSeed(unb64(secret.kem))
  if (b64(idKem.pub) !== bundle.kem_pub) throw new Error('身份包被改动过')
  await open(relay, credId, idSign, idKem)
}

async function certify(idSign: SignKey, sSign: SignKey, sKem: KemKey, sidv: string): Promise<{ cert: Signed; iat: number; exp: number }> {
  const after = lockAfter()
  const life = Math.min(24 * 3600, Math.max(600, after > 0 ? after * 2 : 3 * 3600))
  const c: SessionCert = { t: 'session', sid: sidv, sign_pub: b64(sSign.pub), kem_pub: b64(sKem.pub), label: deviceLabel(), iat: now(), exp: now() + life }
  return { cert: await signDoc(idSign, c), iat: c.iat, exp: c.exp }
}

async function open(relay: string, credId: string, idSign: SignKey, idKem: KemKey) {
  const sSign = await signGenerate(false)
  const sKem = kemGenerate()
  const sidv = await keyId(sSign.pub)
  const { cert, iat, exp } = await certify(idSign, sSign, sKem, sidv)
  const state: Unlocked = {
    relay, credId, idSign, idKem, sid: sidv, sSign, sKem, cert, certIat: iat, certExp: exp,
    net: null as unknown as Net,
  }
  state.net = new Net({
    url: relay, addr: sessionAddr(sidv), sign: sSign, kem: sKem, cert: () => state.cert, identityPub: idSign.pub,
    onChange: emit, onMessage: onMessage,
  })
  u = state
  state.net.start()
  armIdle()
  emit()
  await Promise.race([state.net.ready(), new Promise((r) => setTimeout(r, 15000))])
}

/** Forgets every key. The page needs Face ID again to continue. */
export function lock() {
  if (!u) return
  u.net.stop()
  u = null
  for (const p of waiting.values()) p.reject(new Locked())
  waiting.clear()
  if (idleTimer) clearTimeout(idleTimer)
  emit()
}

// Renew the certificate while unlocked; lock after the idle timeout.
let idleTimer: ReturnType<typeof setTimeout> | null = null
let lastInput = Date.now()

function armIdle() {
  if (idleTimer) clearTimeout(idleTimer)
  if (!u) return
  idleTimer = setTimeout(tick, 15000)
}

async function tick() {
  if (!u) return
  const after = lockAfter()
  if (after > 0 && Date.now() - lastInput > after * 1000) {
    lock()
    return
  }
  // Renew the session certificate halfway through its life. Machines that
  // already know this session keep the certificate they have; a new one
  // goes out with the next channel the page opens.
  if (now() - u.certIat > (u.certExp - u.certIat) / 2) {
    const { cert, iat, exp } = await certify(u.idSign, u.sSign, u.sKem, u.sid)
    if (u) {
      u.cert = cert
      u.certIat = iat
      u.certExp = exp
      u.net.resetChannels()
    }
  }
  armIdle()
}

if (typeof window !== 'undefined') {
  for (const ev of ['pointerdown', 'keydown', 'wheel', 'touchstart', 'scroll']) {
    window.addEventListener(ev, () => (lastInput = Date.now()), { passive: true, capture: true })
  }
}

// --- messages from machines and the brain ----------------------------------

const waiting = new Map<string, { resolve: (m: Record<string, unknown>) => void; reject: (e: Error) => void }>()
const listeners = new Set<(from: string, m: Record<string, unknown>) => void>()

export function onMachineMessage(fn: (from: string, m: Record<string, unknown>) => void) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function onMessage(from: string, m: Record<string, unknown>) {
  const id = typeof m.id === 'string' ? m.id : ''
  const key = `${m.k}|${id}`
  const w = waiting.get(key)
  if (w) {
    waiting.delete(key)
    w.resolve(m)
    return
  }
  for (const fn of listeners) fn(from, m)
}

/** Sends to an address and waits for the answer of kind k with the same id. */
export function ask(to: string, msg: Record<string, unknown>, answer: string, timeout = 30000): Promise<Record<string, unknown>> {
  if (!u) return Promise.reject(new Locked())
  const id = (msg.id as string) || b64(random(9))
  const m = { ...msg, id }
  return new Promise((resolve, reject) => {
    const key = `${answer}|${id}`
    const timer = setTimeout(() => {
      waiting.delete(key)
      reject(new Error('对方没有应答（机器可能不在线）'))
    }, timeout)
    waiting.set(key, {
      resolve: (r) => {
        clearTimeout(timer)
        resolve(r)
      },
      reject: (e) => {
        clearTimeout(timer)
        reject(e)
      },
    })
    u!.net.send(to, m).catch((e) => {
      clearTimeout(timer)
      waiting.delete(key)
      reject(e)
    })
  })
}

// --- critical operations ------------------------------------------------------

function passkeys() {
  return roster()?.user.passkeys ?? []
}

/** Face ID over exactly these bytes. */
export async function approve(payload: Uint8Array): Promise<Assertion> {
  return assertCritical(payload, passkeys())
}

/**
 * Sends a permit straight to a machine (design §5): the brain may then do
 * that one thing there. Critical permits carry the user's assertion.
 */
export async function permit(nodeId: string, action: Permit['action'], match: Record<string, string>, critical: boolean) {
  if (!u) throw new Locked()
  const p: Permit = { t: 'permit', id: b64(random(9)), sid: u.sid, node: nodeId, action, match, critical, iat: now(), exp: now() + 300 }
  const raw = utf8(JSON.stringify(p))
  const msg: Record<string, unknown> = { k: 'permit', id: p.id, raw: b64(raw) }
  if (critical) msg.assertion = await approve(raw)
  const res = await ask(nodeAddr(nodeId), msg, 'permit.res')
  if (res.ok !== true) throw new Error((res.error as string) || '机器拒绝了许可')
}

/** Headers proving the user approved one request to the brain. */
export async function intentHeaders(method: string, path: string, body: Uint8Array): Promise<Record<string, string>> {
  if (!u) throw new Locked()
  const intent = { t: 'intent', sid: u.sid, method, path, body_sha256: b64(await sha256(body)), nonce: b64(random(12)), iat: now() }
  const raw = utf8(JSON.stringify(intent))
  const a = await approve(raw)
  return { 'X-Sous-Intent': b64(raw), 'X-Sous-Assertion': b64(utf8(JSON.stringify(a))) }
}

// --- roster ------------------------------------------------------------------

/** Signs a changed roster (with Face ID when the change widens it) and publishes it. */
export async function updateRoster(change: (r: Roster) => void) {
  if (!u) throw new Locked()
  const n = u.net
  await n.ready()
  const cur = n.roster
  if (!cur || !n.rosterUpdate) throw new Error('还没有名册')
  const next: Roster = structuredClone(cur)
  change(next)
  next.v = cur.v + 1
  next.iat = now()
  next.prev = b64(await sha256(payloadOf(n.rosterUpdate.doc)))
  const doc = await signDoc(u.idSign, next)
  const upd: { doc: Signed; assertion?: Assertion } = { doc }
  if (rosterNeedsAssertion(cur, next)) upd.assertion = await approve(payloadOf(doc))
  await n.put('roster', upd)
  return next
}

/** Freezing is instant and needs no Face ID; unfreezing does. */
export function setFrozen(nodeId: string, frozen: boolean) {
  return updateRoster((r) => {
    const x = r.nodes.find((n) => n.id === nodeId)
    if (x) x.frozen = frozen || undefined
  })
}

/**
 * Opens or closes a machine's console: whether `sous chat` on it may read
 * every conversation and answer every approval. Opening needs Face ID.
 */
export function setConsole(nodeId: string, on: boolean) {
  return updateRoster((r) => {
    const x = r.nodes.find((n) => n.id === nodeId)
    if (x) x.console = on || undefined
  })
}

/** Kicks a machine out: Face ID, a new roster, and new data keys if it was a brain. */
export async function kick(nodeId: string) {
  const wasBrain = !!roster()?.nodes.find((n) => n.id === nodeId)?.brain
  await updateRoster((r) => {
    r.nodes = r.nodes.filter((n) => n.id !== nodeId)
  })
  if (wasBrain) await rotateDomain()
}

export async function setBrain(nodeId: string, priority: number | null) {
  const before = roster()?.nodes.find((n) => n.id === nodeId)?.brain
  await updateRoster((r) => {
    const x = r.nodes.find((n) => n.id === nodeId)
    if (!x) return
    if (priority === null) delete x.brain
    else x.brain = { priority }
  })
  if (priority === null && before) await rotateDomain()
  else await ensureDomainWraps()
}

export function rename(nodeId: string, name: string) {
  return updateRoster((r) => {
    const x = r.nodes.find((n) => n.id === nodeId)
    if (x) x.name = name
  })
}

/** Adds a passkey of another device (Face ID on this one first). */
export async function addPasskey() {
  if (!u) throw new Locked()
  const pk = await createPasskey(deviceLabel())
  const { credId, prf } = await prfUnlock([pk.cred_id])
  const r = await anonRequest(u.relay, { t: 'get', name: 'identity' })
  const bundle = r.body as Bundle
  // Wrap the identity for the new passkey: re-derive it from this session's
  // original passkey is impossible (keys are non-extractable), so ask the
  // user to unlock once more with the existing one to get the secret.
  const old = await prfUnlock(Object.keys(bundle.wraps))
  const secret = await unwrapIdentity(old.prf, bundle.wraps[old.credId])
  bundle.wraps[credId] = await wrapIdentity(prf, secret)
  await updateRoster((ro) => {
    ro.user.passkeys.push(pk)
  })
  await u.net.put('identity', bundle)
}

// --- data keys of the brain (design §7) -------------------------------------

async function userDomainKey(d: DomainKeys, epoch: number): Promise<Bytes> {
  const e = d.epochs.find((x) => x.epoch === epoch)
  if (!e?.wraps.user || !u) throw new Error('没有这个时期的数据密钥')
  return openFrom(u.idKem.seed, `sous/v1/domain\x00${epoch}\x00user`, unb64(e.wraps.user))
}

async function wrapsFor(key: Uint8Array, epoch: number): Promise<Record<string, string>> {
  const wraps: Record<string, string> = {}
  wraps.user = b64(await sealTo(u!.idKem.pub, `sous/v1/domain\x00${epoch}\x00user`, key))
  for (const n of roster()?.nodes ?? []) {
    if (n.brain && !n.frozen) wraps[n.id] = b64(await sealTo(unb64(n.kem_pub), `sous/v1/domain\x00${epoch}\x00${n.id}`, key))
  }
  return wraps
}

/** Makes sure every brain machine has the current data key. */
export async function ensureDomainWraps() {
  if (!u) throw new Locked()
  const n = u.net
  const d = n.domain
  if (!d || d.epochs.length === 0) return rotateDomain()
  const cur = d.epochs[0]
  const brains = (roster()?.nodes ?? []).filter((x) => x.brain && !x.frozen)
  if (brains.every((b) => cur.wraps[b.id])) return
  const key = await userDomainKey(d, cur.epoch)
  const next: DomainKeys = { ...d, v: d.v + 1, iat: now(), epochs: [{ epoch: cur.epoch, wraps: await wrapsFor(key, cur.epoch) }, ...d.epochs.slice(1)] }
  await n.put('domain', await signDoc(u.idSign, next))
}

/** Starts a new epoch: machines that left cannot read what comes next. */
export async function rotateDomain() {
  if (!u) throw new Locked()
  const n = u.net
  const d = n.domain
  const epoch = (d?.epochs[0]?.epoch ?? 0) + 1
  const key = random(32)
  const older = []
  for (const e of (d?.epochs ?? []).slice(0, 2)) {
    try {
      older.push({ epoch: e.epoch, wraps: await wrapsFor(await userDomainKey(d!, e.epoch), e.epoch) })
    } catch {
      /* an epoch this identity cannot open is dropped */
    }
  }
  const next: DomainKeys = { t: 'domain', v: (d?.v ?? 0) + 1, iat: now(), epochs: [{ epoch, wraps: await wrapsFor(key, epoch) }, ...older] }
  await n.put('domain', await signDoc(u.idSign, next))
}

// --- autonomy scope and pause ----------------------------------------------

export function scopes(): Scopes {
  return u?.net.scopes ?? defaultScopes()
}

export async function saveScopes(sc: Omit<Scopes, 't' | 'v' | 'iat'>) {
  if (!u) throw new Locked()
  const cur = u.net.scopes
  const doc = await signDoc(u.idSign, { ...sc, t: 'scopes', v: (cur?.v ?? 0) + 1, iat: now() })
  const assertion = await approve(payloadOf(doc))
  await u.net.put('scopes', { doc, assertion })
}

export function paused(): boolean {
  return !!u?.net.pause?.paused
}

export async function setPaused(p: boolean) {
  if (!u) throw new Locked()
  const doc = await signDoc(u.idSign, { t: 'pause', v: (u.net.pause?.v ?? 0) + 1, iat: now(), paused: p })
  await u.net.put('pause', doc)
}

// --- adding a machine (design §4) ------------------------------------------

export interface PairRequest {
  node: string
  name: string
  os: string
  role?: string
  sign_pub: string
  kem_pub: string
  key_storage: string
}

/**
 * Starts "add a machine": returns the pairing string to paste on the
 * machine, and a promise of the machine's request when it joins.
 */
export async function startPairing(): Promise<{ code: string; exp: number; request: Promise<PairRequest>; confirm: (brain: number | null, console: boolean) => Promise<void>; cancel: () => void }> {
  if (!u) throw new Locked()
  const n = u.net
  const secret = random(16)
  const exp = now() + 600
  const code = encodePair({ relayHash: relayHash(u.relay), identityFp: await sha256(u.idSign.pub), secret, exp })
  const room = pairRoom(secret)
  let got: PairRequest | null = null
  const request = new Promise<PairRequest>((resolve, reject) => {
    n.openRoom(room, (data) => {
      const m = data as { body: string; mac: string }
      const body = unb64(m.body)
      if (!checkPairMac(secret, 'node', body, m.mac)) return
      const h = JSON.parse(fromUtf8(body)) as PairRequest & { t: string }
      if (h.t !== 'pair.hello') return
      got = h
      resolve(h)
    }).catch(reject)
  })
  return {
    code, exp, request,
    cancel: () => n.closeRoom(room),
    confirm: async (brain, console) => {
      if (!got) throw new Error('机器还没有发来请求')
      const h = got
      if ((await nodeIdOf(unb64(h.sign_pub))) !== h.node) throw new Error('机器的 id 和密钥不符')
      await updateRoster((r) => {
        const x: RosterNode = { id: h.node, name: h.name, sign_pub: h.sign_pub, kem_pub: h.kem_pub, key_storage: h.key_storage, os: h.os, role: h.role, added: now() }
        if (brain !== null) x.brain = { priority: brain }
        if (console) x.console = true
        r.nodes.push(x)
      })
      const welcome = utf8(JSON.stringify({ t: 'pair.welcome', roster: n.rosterUpdate }))
      n.roomSend(room, { body: b64(welcome), mac: pairMac(secret, 'app', welcome) })
      if (brain !== null) await ensureDomainWraps()
      setTimeout(() => n.closeRoom(room), 5000)
    },
  }
}

const B32 = 'abcdefghijklmnopqrstuvwxyz234567'

/** Go: trust.NodeIDOf. */
async function nodeIdOf(signPub: Uint8Array): Promise<string> {
  const h = await sha256(new Uint8Array([...utf8('sous/v1/node-id'), 0, ...signPub]))
  let bits = 0, v = 0, out = ''
  for (const x of h.subarray(0, 15)) {
    v = (v << 8) | x
    bits += 8
    while (bits >= 5) {
      out += B32[(v >>> (bits - 5)) & 31]
      bits -= 5
    }
  }
  return 'n' + out
}

export function identityFingerprint(): string {
  if (!u) return ''
  const fp = b64(nsha256(u.idSign.pub)).slice(0, 24)
  return `${fp.slice(0, 6)}-${fp.slice(6, 12)}-${fp.slice(12, 18)}-${fp.slice(18, 24)}`
}
