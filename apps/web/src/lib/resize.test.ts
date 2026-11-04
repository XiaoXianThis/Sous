import { describe, expect, it } from 'vitest'
import { clampWidth, dragWidth } from './resize'

describe('panel resize', () => {
  it('clamps to the range, the minimum winning when the window is too small', () => {
    expect(clampWidth(250, 300, 800)).toBe(300)
    expect(clampWidth(950, 300, 800)).toBe(800)
    expect(clampWidth(420.4, 300, 800)).toBe(420)
    expect(clampWidth(500, 300, 200)).toBe(300)
  })
  it('grows a left edge when dragged left and a right edge when dragged right', () => {
    expect(dragWidth(400, -50, 'left')).toBe(450)
    expect(dragWidth(400, -50, 'right')).toBe(350)
  })
})
