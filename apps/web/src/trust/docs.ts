// Signed documents shared with apps/core/internal/trust (docs.go).
import type { Signed } from './sign'

export interface Passkey {
  cred_id: string
  pub: string
  label?: string
  added?: number
}

export interface RosterNode {
  id: string
  name: string
  sign_pub: string
  kem_pub: string
  key_storage?: string
  os?: string
  role?: string
  brain?: { priority: number }
  frozen?: boolean
  added?: number
}

export interface Roster {
  t: 'roster'
  v: number
  iat: number
  prev?: string
  user: { sign_pub: string; kem_pub: string; passkeys: Passkey[] }
  rp_id: string
  origins: string[]
  relay: string
  nodes: RosterNode[]
}

export interface Assertion {
  cred_id: string
  authenticator_data: string
  client_data_json: string
  signature: string
}

export interface RosterUpdate {
  doc: Signed
  assertion?: Assertion
}

export interface Scope {
  nodes?: string[]
  max_mode?: 'default' | 'auto' | ''
  allow_merge?: boolean
  allow_ops?: boolean
  allow_open?: boolean
}

export interface Scopes {
  t: 'scopes'
  v: number
  iat: number
  default: Scope
  projects?: Record<string, Scope>
}

export const defaultScopes = (): Scopes => ({ t: 'scopes', v: 0, iat: 0, default: { max_mode: 'auto' } })

export interface Pause {
  t: 'pause'
  v: number
  iat: number
  paused: boolean
}

export interface Lease {
  t: 'lease'
  term: number
  holder: string
  iat: number
  exp: number
}

export interface DomainKeys {
  t: 'domain'
  v: number
  iat: number
  epochs: { epoch: number; wraps: Record<string, string> }[]
}

export interface SessionCert {
  t: 'session'
  sid: string
  sign_pub: string
  kem_pub: string
  label?: string
  iat: number
  exp: number
}

export interface Permit {
  t: 'permit'
  id: string
  sid: string
  node: string
  action: 'admin' | 'update' | 'run' | 'unfreeze'
  match?: Record<string, string>
  critical?: boolean
  iat: number
  exp: number
}

export interface Alert {
  t: 'audit'
  node: string
  frozen: boolean
  reason: string
  iat: number
}

/** Mirrors trust.RosterNeedsAssertion: changes that widen who belongs. */
export function rosterNeedsAssertion(a: Roster, b: Roster): boolean {
  if (a.rp_id !== b.rp_id || a.relay !== b.relay || a.user.kem_pub !== b.user.kem_pub) return true
  if (a.origins.length !== b.origins.length || a.origins.some((o, i) => o !== b.origins[i])) return true
  if (a.user.passkeys.length !== b.user.passkeys.length) return true
  for (let i = 0; i < a.user.passkeys.length; i++) {
    if (a.user.passkeys[i].cred_id !== b.user.passkeys[i].cred_id || a.user.passkeys[i].pub !== b.user.passkeys[i].pub) return true
  }
  if (a.nodes.length !== b.nodes.length) return true
  for (const nb of b.nodes) {
    const na = a.nodes.find((n) => n.id === nb.id)
    if (!na || na.sign_pub !== nb.sign_pub || na.kem_pub !== nb.kem_pub) return true
    if (!!na.brain !== !!nb.brain || (na.brain && nb.brain && na.brain.priority !== nb.brain.priority)) return true
    if (na.frozen && !nb.frozen) return true
  }
  return false
}

export const nodeAddr = (id: string) => 'node:' + id
export const sessionAddr = (sid: string) => 'sess:' + sid
