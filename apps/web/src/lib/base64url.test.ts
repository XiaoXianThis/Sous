import { describe, expect, it } from 'vitest'
import { fromBytes, toBytes } from './base64url'

describe('base64url', () => {
  it('round-trips bytes of every padding length', () => {
    for (let n = 0; n < 8; n++) {
      const bytes = new Uint8Array(n).map((_, i) => (i * 97 + 251) % 256)
      const s = fromBytes(bytes)
      expect(s).not.toMatch(/[+/=]/)
      expect(Array.from(toBytes(s))).toEqual(Array.from(bytes))
    }
  })
})
