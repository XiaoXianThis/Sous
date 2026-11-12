// @vitest-environment node
import { gzipSync } from 'node:zlib'
import { afterEach, expect, it, vi } from 'vitest'
import { b64, fromUtf8, utf8 } from './bytes'
import { rpc } from './rpc'

const transport = vi.hoisted(() => ({ ask: vi.fn() }))
vi.mock('./vault', () => ({
  ask: transport.ask,
  isUnlocked: () => true,
  Locked: class extends Error {},
  net: () => ({ ready: async () => {}, brain: () => 'brain' }),
  onMachineMessage: () => () => {},
}))

afterEach(() => vi.clearAllMocks())

it('negotiates and decodes gzip replies without changing their JSON', async () => {
  const body = JSON.stringify({ runs: Array.from({ length: 1000 }, () => ({ status: 'succeeded' })) })
  transport.ask.mockResolvedValue({ status: 200, body: b64(gzipSync(body)), header: { 'X-Sous-Content-Encoding': 'gzip', 'Content-Type': 'application/json' } })
  const res = await rpc('GET', '/api/v1/runs')
  expect(fromUtf8(res.body)).toBe(body)
  expect(transport.ask.mock.calls[0][1].header['X-Sous-Accept-Encoding']).toBe('gzip')
})

it('accepts plain replies from older brains', async () => {
  transport.ask.mockResolvedValue({ status: 200, body: b64(utf8('{"ok":true}')), header: {} })
  expect(fromUtf8((await rpc('GET', '/api/v1/me')).body)).toBe('{"ok":true}')
})
