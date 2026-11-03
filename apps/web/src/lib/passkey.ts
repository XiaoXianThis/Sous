// Passkey (WebAuthn) ceremonies against the Hub.
import { request } from './api'
import { fromBytes, toBytes } from './base64url'

function credentialJSON(cred: PublicKeyCredential) {
  const r = cred.response as AuthenticatorAttestationResponse & AuthenticatorAssertionResponse
  const response: Record<string, string> = { clientDataJSON: fromBytes(r.clientDataJSON) }
  if ('attestationObject' in r && r.attestationObject) response.attestationObject = fromBytes(r.attestationObject)
  if ('authenticatorData' in r && r.authenticatorData) response.authenticatorData = fromBytes(r.authenticatorData)
  if ('signature' in r && r.signature) response.signature = fromBytes(r.signature)
  if ('userHandle' in r && r.userHandle) response.userHandle = fromBytes(r.userHandle)
  return { id: cred.id, rawId: fromBytes(cred.rawId), type: cred.type, response }
}

export function passkeySupported(): boolean {
  return typeof window !== 'undefined' && !!window.PublicKeyCredential && window.isSecureContext
}

export async function registerPasskey(setupToken?: string) {
  const opts = await request<any>('POST', '/api/v1/auth/passkey/register/begin', { setup_token: setupToken ?? '' })
  const pk = opts.publicKey
  pk.challenge = toBytes(pk.challenge)
  pk.user.id = toBytes(pk.user.id)
  pk.excludeCredentials = (pk.excludeCredentials ?? []).map((c: any) => ({ ...c, id: toBytes(c.id) }))
  const cred = (await navigator.credentials.create({ publicKey: pk })) as PublicKeyCredential | null
  if (!cred) throw new Error('已取消')
  await request('POST', '/api/v1/auth/passkey/register/finish', credentialJSON(cred))
}

export async function loginWithPasskey() {
  const opts = await request<any>('POST', '/api/v1/auth/passkey/login/begin')
  const pk = opts.publicKey
  pk.challenge = toBytes(pk.challenge)
  pk.allowCredentials = (pk.allowCredentials ?? []).map((c: any) => ({ ...c, id: toBytes(c.id) }))
  const cred = (await navigator.credentials.get({ publicKey: pk })) as PublicKeyCredential | null
  if (!cred) throw new Error('已取消')
  await request('POST', '/api/v1/auth/passkey/login/finish', credentialJSON(cred))
}
