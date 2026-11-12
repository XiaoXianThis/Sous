// Passkeys: Face ID, a fingerprint, Touch ID or the Windows PIN. Two uses:
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
        // This device's own authenticator (Face ID, Touch ID, Windows Hello),
        // so the browser does not offer a phone instead.
        authenticatorSelection: { authenticatorAttachment: 'platform', residentKey: 'required', userVerification: 'required' },
        hints: ['client-device'],
        timeout: 120000,
        extensions: { prf: {} } as AuthenticationExtensionsClientInputs,
      } as PublicKeyCredentialCreationOptions,
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

function hintsFor(allow?: string[]): string[] | undefined {
  // One credential means this device's own: keep the browser from offering a phone.
  return allow?.length === 1 ? ['client-device'] : undefined
}

export interface PrfResult {
  credId: string
  /** null when this authenticator has no PRF (Windows Hello on most builds) */
  prf: Bytes | null
  /** 'platform' for this device's own authenticator, 'cross-platform' for a phone or key */
  attachment: string | null
}

/** Asks for the passkey and returns its PRF output, when it has one. */
export async function prfTry(allow?: string[]): Promise<PrfResult> {
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({
      publicKey: {
        challenge: crypto.getRandomValues(new Uint8Array(32)),
        rpId: location.hostname,
        allowCredentials: allow?.map((id) => ({ type: 'public-key' as const, id: unb64(id) })),
        userVerification: 'required',
        hints: hintsFor(allow),
        timeout: 120000,
        extensions: { prf: { eval: { first: PRF_SALT } } } as AuthenticationExtensionsClientInputs,
      } as PublicKeyCredentialRequestOptions,
    })) as PublicKeyCredential | null
  } catch (e) {
    throw friendly(e)
  }
  if (!cred) throw new Cancelled()
  const ext = cred.getClientExtensionResults() as { prf?: { results?: { first?: ArrayBuffer } } }
  const first = ext.prf?.results?.first
  return { credId: b64(new Uint8Array(cred.rawId)), prf: first ? new Uint8Array(first) : null, attachment: cred.authenticatorAttachment ?? null }
}

/** Asks for the passkey and returns the PRF output that unlocks the identity. */
export async function prfUnlock(allow?: string[]): Promise<{ credId: string; prf: Bytes; attachment: string | null }> {
  const r = await prfTry(allow)
  if (!r.prf) {
    throw new Error('这个 Passkey 不支持 PRF 扩展，解不开身份。请用手机或已经添加过的设备解锁。')
  }
  return { credId: r.credId, prf: r.prf, attachment: r.attachment }
}

/** The challenge of a critical operation: the operation itself. */
export async function criticalChallenge(payload: Uint8Array): Promise<Bytes> {
  return sha256(labeled('sous/v1/critical', payload))
}

/** Signs a critical operation with the user's passkey (user verification required). */
export async function assertCritical(payload: Uint8Array, passkeys: Passkey[], prefer?: string): Promise<Assertion> {
  const ids = passkeys.map((p) => p.cred_id)
  // This device's own passkey alone, when it is one of them: no phone prompt.
  return assertWith(await criticalChallenge(payload), prefer && ids.includes(prefer) ? [prefer] : ids)
}

/** A WebAuthn assertion over challenge by one of allow. */
export async function assertWith(challenge: Bytes, allow: string[]): Promise<Assertion> {
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({
      publicKey: {
        challenge,
        rpId: location.hostname,
        allowCredentials: allow.map((id) => ({ type: 'public-key' as const, id: unb64(id) })),
        userVerification: 'required',
        hints: hintsFor(allow),
        timeout: 120000,
      } as PublicKeyCredentialRequestOptions,
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
