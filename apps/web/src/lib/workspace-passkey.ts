import { b64, unb64 } from '../trust/bytes'
import { workspaceJSON } from './workspace-transport'

type EncodedDescriptor = Omit<PublicKeyCredentialDescriptor, 'id'> & { id: string }
type Creation = Omit<PublicKeyCredentialCreationOptions, 'challenge' | 'user' | 'excludeCredentials'> & {
  challenge: string
  user: Omit<PublicKeyCredentialUserEntity, 'id'> & { id: string }
  excludeCredentials?: EncodedDescriptor[]
}
type Assertion = Omit<PublicKeyCredentialRequestOptions, 'challenge' | 'allowCredentials'> & {
  challenge: string
  allowCredentials?: EncodedDescriptor[]
}

async function authPost<T>(action: string, body?: unknown): Promise<T> {
  const res = await workspaceJSON<T & { error?: { message?: string } }>('POST', `/api/v1/auth/${action}`, body)
  if (res.status >= 300 || !res.json) throw new Error(res.json?.error?.message ?? `身份验证失败（HTTP ${res.status}）`)
  return res.json
}

const decode = (items?: EncodedDescriptor[]) => items?.map((item) => ({ ...item, id: unb64(item.id) }))

function credentialJSON(cred: PublicKeyCredential) {
  const r = cred.response
  const response: Record<string, string> = { clientDataJSON: b64(new Uint8Array(r.clientDataJSON)) }
  if ('attestationObject' in r) {
    response.attestationObject = b64(new Uint8Array((r as AuthenticatorAttestationResponse).attestationObject))
  } else {
    const a = r as AuthenticatorAssertionResponse
    response.authenticatorData = b64(new Uint8Array(a.authenticatorData))
    response.signature = b64(new Uint8Array(a.signature))
    if (a.userHandle) response.userHandle = b64(new Uint8Array(a.userHandle))
  }
  return { id: cred.id, rawId: b64(new Uint8Array(cred.rawId)), type: cred.type, response }
}

export async function workspacePasskey(setupToken?: string) {
  const action = setupToken ? 'register' : 'login'
  let cred: Credential | null
  if (setupToken) {
    const { publicKey: o } = await authPost<{ publicKey: Creation }>('passkey/register/begin', { setup_token: setupToken })
    cred = await navigator.credentials.create({ publicKey: {
      ...o, challenge: unb64(o.challenge), user: { ...o.user, id: unb64(o.user.id) }, excludeCredentials: decode(o.excludeCredentials),
    } })
  } else {
    const { publicKey: o } = await authPost<{ publicKey: Assertion }>('passkey/login/begin')
    cred = await navigator.credentials.get({ publicKey: { ...o, challenge: unb64(o.challenge), allowCredentials: decode(o.allowCredentials) } })
  }
  if (!(cred instanceof PublicKeyCredential)) throw new Error('身份验证已取消')
  await authPost(`passkey/${action}/finish`, credentialJSON(cred))
}
