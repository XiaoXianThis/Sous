import { describe, expect, it } from 'vitest'
import { axisOf, keepAnchor, scrollAfterResize, rubber, shouldDismiss, velocityOf, viewportVars } from './gesture'

describe('axisOf', () => {
  it('waits until the touch moves past the slop', () => {
    expect(axisOf(3, 4)).toBeNull()
    expect(axisOf(0, 0)).toBeNull()
  })
  it('picks the dominant direction', () => {
    expect(axisOf(20, 5)).toBe('x')
    expect(axisOf(-3, 30)).toBe('y')
    expect(axisOf(-30, -10)).toBe('x')
  })
})

describe('rubber', () => {
  it('resists more the further it is pulled and never passes the limit', () => {
    expect(rubber(0)).toBe(0)
    expect(rubber(-10)).toBe(0)
    const a = rubber(30, 60)
    const b = rubber(300, 60)
    expect(a).toBeGreaterThan(0)
    expect(a).toBeLessThan(30)
    expect(b).toBeGreaterThan(a)
    expect(b).toBeLessThan(60)
    expect(rubber(1e9, 60)).toBeLessThanOrEqual(60)
  })
})

describe('shouldDismiss', () => {
  it('dismisses a long pull', () => {
    expect(shouldDismiss(200, 0, 500)).toBe(true)
    expect(shouldDismiss(100, 0, 500)).toBe(false)
  })
  it('caps the distance needed on tall layers', () => {
    expect(shouldDismiss(170, 0, 2000)).toBe(true)
  })
  it('dismisses a quick flick, but not a twitch', () => {
    expect(shouldDismiss(40, 0.8, 500)).toBe(true)
    expect(shouldDismiss(10, 2, 500)).toBe(false)
  })
  it('keeps the layer when flung back or pulled the wrong way', () => {
    expect(shouldDismiss(300, -0.6, 500)).toBe(false)
    expect(shouldDismiss(-50, 1, 500)).toBe(false)
  })
})

describe('velocityOf', () => {
  it('is zero without enough samples', () => {
    expect(velocityOf([])).toBe(0)
    expect(velocityOf([{ t: 0, v: 0 }])).toBe(0)
  })
  it('uses the recent samples', () => {
    const s = [{ t: 0, v: 0 }, { t: 100, v: 0 }, { t: 110, v: 10 }, { t: 120, v: 20 }, { t: 130, v: 30 }]
    expect(velocityOf(s)).toBeCloseTo(1)
  })
  it('is zero once the finger has rested', () => {
    const s = [{ t: 0, v: 0 }, { t: 10, v: 30 }, { t: 20, v: 60 }]
    expect(velocityOf(s, 40)).toBeCloseTo(3)
    expect(velocityOf(s, 200)).toBe(0)
  })
  it('ignores samples with no time between them', () => {
    expect(velocityOf([{ t: 5, v: 0 }, { t: 5, v: 40 }])).toBe(0)
  })
})

describe('scrollAfterResize', () => {
  const base = { prevHeight: 600, height: 300, scrollTop: 200, scrollHeight: 1800, stick: false }
  it('sticks to the bottom', () => { expect(scrollAfterResize({ ...base, stick: true })).toBe(1500) })
  it('moves with a shorter viewport', () => { expect(scrollAfterResize(base)).toBe(500) })
  it('moves with a taller viewport', () => { expect(scrollAfterResize({ ...base, prevHeight: 300, height: 600, scrollTop: 500 })).toBe(200) })
  it('clamps to zero', () => { expect(scrollAfterResize({ ...base, height: 900 })).toBe(0) })
  it('clamps to the maximum', () => { expect(scrollAfterResize({ ...base, scrollTop: 1700 })).toBe(1500) })
})

describe('keepAnchor', () => {
  it('scrolls so the anchor stays where it was', () => {
    // Folding back: the list starts over at 0 and the card sits at 900; it was at 240.
    expect(keepAnchor({ at: 240, now: 900, scrollTop: 0, scrollHeight: 3000, height: 700 })).toEqual({ scrollTop: 660, off: 0 })
  })
  it('scrolls up when the anchor moved up', () => {
    expect(keepAnchor({ at: 300, now: 100, scrollTop: 500, scrollHeight: 3000, height: 700 })).toEqual({ scrollTop: 300, off: 0 })
  })
  it('leaves the rest to a transform when the content is too short to scroll', () => {
    // Opening: the card fills the area, nothing scrolls; it was 400 down and now sits at 12.
    expect(keepAnchor({ at: 400, now: 12, scrollTop: 0, scrollHeight: 700, height: 700 })).toEqual({ scrollTop: 0, off: -388 })
  })
  it('clamps at the end of the content', () => {
    expect(keepAnchor({ at: 0, now: 900, scrollTop: 0, scrollHeight: 1200, height: 700 })).toEqual({ scrollTop: 500, off: 400 })
  })
  it('does nothing when the anchor did not move', () => {
    expect(keepAnchor({ at: 50, now: 50, scrollTop: 120, scrollHeight: 2000, height: 700 })).toEqual({ scrollTop: 120, off: 0 })
  })
})

describe('viewportVars', () => {
  it('has no keyboard when the visual viewport fills the screen', () => {
    expect(viewportVars({ inner: 844, height: 844, offsetTop: 0 })).toEqual({ h: 844, top: 0, kb: 0, open: false })
  })
  it('measures the keyboard under the visible part', () => {
    expect(viewportVars({ inner: 844, height: 508, offsetTop: 0 })).toEqual({ h: 508, top: 0, kb: 336, open: true })
  })
  it('accounts for iOS panning the visible part down', () => {
    expect(viewportVars({ inner: 844, height: 508, offsetTop: 120.4 })).toEqual({ h: 508, top: 120, kb: 216, open: true })
  })
  it('does not call a shrinking toolbar a keyboard', () => {
    expect(viewportVars({ inner: 844, height: 790, offsetTop: 0 }).open).toBe(false)
  })
})
