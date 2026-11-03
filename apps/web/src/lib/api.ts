// REST client for docs/api/core-hub-http.md.
import type { Me, Message, Thread } from './types'

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

function csrf(): string {
  const m = document.cookie.match(/(?:^|;\s*)sous_csrf=([^;]+)/)
  return m ? decodeURIComponent(m[1]) : ''
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') headers['X-CSRF-Token'] = csrf()
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, data?.error?.code ?? 'http', data?.error?.message ?? `HTTP ${res.status}`)
  }
  return data as T
}

export const api = {
  me: () => request<Me>('GET', '/api/v1/me'),
  devLogin: () => request<{ ok: boolean }>('POST', '/api/v1/auth/dev-login'),
  logout: () => request<{ ok: boolean }>('POST', '/api/v1/auth/logout'),
  threads: () => request<{ threads: Thread[] }>('GET', '/api/v1/threads'),
  createThread: (title: string) => request<Thread>('POST', '/api/v1/threads', { title }),
  messages: (threadId: string, before = 0) =>
    request<{ messages: Message[]; last_seq: number }>(
      'GET',
      `/api/v1/threads/${threadId}/messages?limit=50${before ? `&before=${before}` : ''}`,
    ),
  send: (threadId: string, content: string) =>
    request<{ message_id: string; seq: number }>('POST', `/api/v1/threads/${threadId}/messages`, { content }),
}
