// @vitest-environment node
// Cross-checks with apps/core/internal/trust through the shared vectors in
// work/tests/fixtures. SOUS_WRITE_VECTORS=1 rewrites the page's vectors for
// the Go test TestWebVectors.
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { b64, fromUtf8, unb64, utf8, now, random } from './bytes'
import { kemFromSeed, kemGenerate, openFrom, sealTo } from './hpke'
import { openDoc, sign, signDoc, signGenerate, verify } from './sign'
import { InChannel, OutChannel, GapError } from './channel'
import { checkPairMac, encodePair, pairMac, parsePair, relayHash } from './pair'

const dir = join(__dirname, '..', '..', '..', '..', 'work', 'tests', 'fixtures')
const goFile = join(dir, 'trust-vectors.json')
const go = existsSync(goFile) ? JSON.parse(readFileSync(goFile, 'utf8')) : null

describe.skipIf(!go)('Go vectors', () => {
  it('verifies a Go signature', async () => {
    expect(await verify(unb64(go.sign.Pub), go.sign.Ctx, utf8(go.sign.Msg), unb64(go.sign.Sig))).toBe(true)
    expect(await verify(unb64(go.sign.Pub), go.sign.Ctx, utf8(go.sign.Msg + '!'), unb64(go.sign.Sig))).toBe(false)
  })
  it('opens a Go document', async () => {
    const r = await openDoc<{ v: number; rp_id: string }>(unb64(go.doc.pub), 'roster', go.doc.doc)
    expect(r.v).toBe(1)
    await expect(openDoc(unb64(go.doc.pub), 'scopes', go.doc.doc)).rejects.toThrow()
  })
  it('derives the same X-Wing key from a seed', () => {
    expect(b64(kemFromSeed(unb64(go.seal.Seed)).pub)).toBe(go.seal.Pub)
  })
  it('opens a Go HPKE seal', async () => {
    expect(fromUtf8(await openFrom(unb64(go.seal.Seed), go.seal.Info, unb64(go.seal.CT)))).toBe(go.seal.PT)
    await expect(openFrom(unb64(go.seal.Seed), 'other info', unb64(go.seal.CT))).rejects.toThrow()
  })
  it('opens a Go channel', async () => {
    const k = kemFromSeed(unb64(go.chan.recipient_seed))
    const o = InChannel.peek(go.chan.wires[0].open)
    const ch = await InChannel.accept(go.chan.wires[0].open, unb64(go.chan.sender_pub), go.chan.me, k.seed, k.pub, o.iat)
    for (let i = 0; i < go.chan.wires.length; i++) {
      expect(fromUtf8(await ch.open(go.chan.wires[i]))).toBe(go.chan.plain[i])
    }
    await expect(ch.open(go.chan.wires[0])).rejects.toThrow('replayed')
  })
  it('parses a Go pairing string', () => {
    const p = parsePair(go.pair.Code)
    expect(b64(p.secret)).toBe(go.pair.Secret)
    expect(b64(p.identityFp)).toBe(go.pair.FP)
    expect(b64(p.relayHash)).toBe(go.pair.RelayHash)
    expect(b64(relayHash('wss://relay.example/'))).toBe(go.pair.RelayHash)
    expect(p.exp).toBe(go.pair.Exp)
  })
  it('opens a Go domain key wrap', async () => {
    const info = `sous/v1/domain\x00${go.domain.Epoch}\x00${go.domain.Member}`
    expect(b64(await openFrom(unb64(go.domain.Seed), info, unb64(go.domain.Wrap)))).toBe(go.domain.Key)
  })
})

describe('trust in the page', () => {
  it('round-trips a channel and refuses tampering', async () => {
    const s = await signGenerate()
    const k = kemGenerate()
    const out = await OutChannel.open('sess:a', 'node:b', s, k.pub)
    const w1 = await out.seal(utf8('one'))
    const w2 = await out.seal(utf8('two'))
    const w3 = await out.seal(utf8('three'))
    await expect(InChannel.accept(w1.open!, s.pub, 'node:c', k.seed, k.pub)).rejects.toThrow()
    const other = await signGenerate()
    await expect(InChannel.accept(w1.open!, other.pub, 'node:b', k.seed, k.pub)).rejects.toThrow()
    const ch = await InChannel.accept(w1.open!, s.pub, 'node:b', k.seed, k.pub)
    expect(fromUtf8(await ch.open(w1))).toBe('one')
    await expect(ch.open({ ...w2, ct: w2.ct.slice(0, -2) + 'AA' })).rejects.toThrow()
    await expect(ch.open(w3)).rejects.toBeInstanceOf(GapError)
    await expect(ch.open(w2)).rejects.toThrow()
  })
  it('binds pairing MACs to a side', () => {
    const sec = random(16)
    const m = pairMac(sec, 'node', utf8('x'))
    expect(checkPairMac(sec, 'node', utf8('x'), m)).toBe(true)
    expect(checkPairMac(sec, 'app', utf8('x'), m)).toBe(false)
  })
})

describe.runIf(process.env.SOUS_WRITE_VECTORS === '1' && go)('write web vectors', () => {
  it('writes', async () => {
    const s = await signGenerate()
    const msg = 'web 签名'
    const sig = await sign(s, 'sous/v1/test', utf8(msg))
    const doc = await signDoc(s, { t: 'roster', v: 1, iat: now(), user: { sign_pub: b64(s.pub) } } as { t: string })
    const k = kemFromSeed(unb64(go.seal.Seed))
    const ct = await sealTo(k.pub, 'sous/v1/web-seal', utf8('网页密文'))
    const out = await OutChannel.open('sess:w', 'node:g', s, k.pub)
    const plain = ['{"k":"a"}', '{"k":"b","x":"中文"}']
    const wires = []
    for (const p of plain) wires.push(await out.seal(utf8(p)))
    const secret = random(16)
    const code = encodePair({ relayHash: relayHash('wss://r'), identityFp: random(32), secret, exp: now() + 600 })
    writeFileSync(join(dir, 'trust-vectors-web.json'), JSON.stringify({
      sign: { Pub: b64(s.pub), Ctx: 'sous/v1/test', Msg: msg, Sig: b64(sig) },
      doc: { pub: b64(s.pub), doc },
      seal: { Info: 'sous/v1/web-seal', CT: b64(ct), PT: '网页密文' },
      chan: { sender_pub: b64(s.pub), me: 'node:g', wires, plain },
      pair: { Code: code, Secret: b64(secret) },
    }, null, 2) + '\n')
  })
})
