// Passkeys: Face ID, a fingerprint or the Windows PIN. Two uses:
//  - unlocking: the PRF extension turns the passkey into a key that opens
//    the identity (no password exists anywhere);
//  - critical operations: an assertion whose challenge is the operation
//    itself, checked by the machine or the brain, never by this page alone.
import { b64, concat, labeled, sha256, unb64, utf8, type Bytes } from './bytes'
import type { Assertion, Passkey } from './docs'

export const PRF_SALT = utf8('sous/v1/prf-identity')

export function passkeysAvailable(): boolean {
  return typeof window !== 'undefined' && !!window.PublicKeyCredential && window.isSecureContext
}

export class Cancelled extends Error {
  constructor() {
    super('已取消')
  }
}

function friendly(e: unknown): Error {
  const err = e as Error
  if (err?.name === 'NotAllowedError' || err?.name === 'AbortError') return new Cancelled()
  return err instanceof Error ? err : new Error(String(e))
}

/** Creates a passkey for this site; returns its id and public key. */
export async function createPasskey(label: string): Promise<Passkey> {
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.create({
      publicKey: {
        rp: { name: 'sous', id: location.hostname },
        user: { id: crypto.getRandomValues(new Uint8Array(16)), name: 'sous', displayName: 'sous 主人' },
        challenge: crypto.getRandomValues(new Uint8Array(32)),
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }], // ES256 only: the machines check P-256
        authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
        timeout: 120000,
        extensions: { prf: {} } as AuthenticationExtensionsClientInputs,
      },
    })) as PublicKeyCredential | null
  } catch (e) {
    throw friendly(e)
  }
  if (!cred) throw new Cancelled()
  const res = cred.response as AuthenticatorAttestationResponse
  if (res.getPublicKeyAlgorithm?.() !== -7) throw new Error('这个设备的 Passkey 不是 P-256，sous 无法使用')
  const spki = res.getPublicKey?.()
  if (!spki) throw new Error('浏览器没有给出 Passkey 的公钥，请换用较新的浏览器')
  const k = await crypto.subtle.importKey('spki', spki, { name: 'ECDSA', namedCurve: 'P-256' }, true, ['verify'])
  const raw = new Uint8Array(await crypto.subtle.exportKey('raw', k))
  return { cred_id: b64(new Uint8Array(cred.rawId)), pub: b64(raw), label, added: Math.floor(Date.now() / 1000) }
}

/** Asks for the passkey and returns the PRF output that unlocks the identity. */
export async function prfUnlock(allow?: string[]): Promise<{ credId: string; prf: Bytes }> {
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({
      publicKey: {
        challenge: crypto.getRandomValues(new Uint8Array(32)),
        rpId: location.hostname,
        allowCredentials: allow?.map((id) => ({ type: 'public-key' as const, id: unb64(id) })),
        userVerification: 'required',
        timeout: 120000,
        extensions: { prf: { eval: { first: PRF_SALT } } } as AuthenticationExtensionsClientInputs,
      },
    })) as PublicKeyCredential | null
  } catch (e) {
    throw friendly(e)
  }
  if (!cred) throw new Cancelled()
  const ext = cred.getClientExtensionResults() as { prf?: { results?: { first?: ArrayBuffer } } }
  const first = ext.prf?.results?.first
  if (!first) {
    throw new Error('这个设备或浏览器不支持 Passkey 的 PRF 扩展，无法解锁。可以在这里用手机扫码，用手机上的 Passkey 解锁。')
  }
  return { credId: b64(new Uint8Array(cred.rawId)), prf: new Uint8Array(first) }
}

/** The challenge of a critical operation: the operation itself. */
export async function criticalChallenge(payload: Uint8Array): Promise<Bytes> {
  return sha256(labeled('sous/v1/critical', payload))
}

/** Signs a critical operation with the user's passkey (user verification required). */
export async function assertCritical(payload: Uint8Array, passkeys: Passkey[]): Promise<Assertion> {
  const challenge = await criticalChallenge(payload)
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({
      publicKey: {
        challenge,
        rpId: location.hostname,
        allowCredentials: passkeys.map((p) => ({ type: 'public-key' as const, id: unb64(p.cred_id) })),
        userVerification: 'required',
        timeout: 120000,
      },
    })) as PublicKeyCredential | null
  } catch (e) {
    throw friendly(e)
  }
  if (!cred) throw new Cancelled()
  const r = cred.response as AuthenticatorAssertionResponse
  return {
    cred_id: b64(new Uint8Array(cred.rawId)),
    authenticator_data: b64(new Uint8Array(r.authenticatorData)),
    client_data_json: b64(new Uint8Array(r.clientDataJSON)),
    signature: b64(new Uint8Array(r.signature)),
  }
}

export const _concat = concat
