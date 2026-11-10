// Popovers near the screen edges: Radix (Floating UI) slides them sideways to stay
// 8px inside a 375px-wide phone screen and flips them above when there is no room
// below. happy-dom has no layout, so the trigger's and the panel's boxes are
// stubbed and Floating UI's own math does the rest.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EDGE_GAP, Popover, PopoverContent, PopoverTrigger } from './popover'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const VW = 375
const VH = 667
const PANEL = { width: 300, height: 160 }

let root: Root
let el: HTMLDivElement
/** Where the trigger sits on screen. */
let trigger = { left: 0, top: 0, width: 16, height: 20 }

const rect = (left: number, top: number, width: number, height: number) =>
  ({ left, top, width, height, x: left, y: top, right: left + width, bottom: top + height, toJSON: () => ({}) }) as DOMRect

beforeEach(() => {
  vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(VW)
  vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(VH)
  vi.spyOn(document.documentElement, 'clientWidth', 'get').mockReturnValue(VW)
  vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(VH)
  const isPanel = (n: Element) => n.matches('[data-test-panel]')
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.matches('[data-test-trigger]')) return rect(trigger.left, trigger.top, trigger.width, trigger.height)
    if (isPanel(this)) return rect(0, 0, PANEL.width, PANEL.height)
    return rect(0, 0, VW, VH)
  })
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return isPanel(this) || this.querySelector('[data-test-panel]') ? PANEL.width : 0
  })
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return isPanel(this) || this.querySelector('[data-test-panel]') ? PANEL.height : 0
  })
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(async () => {
  await act(async () => root.unmount())
  el.remove()
  vi.restoreAllMocks()
})

/** Opens a popover on a trigger at `at` and returns where Floating UI put the panel. */
async function place(at: typeof trigger, side: 'top' | 'bottom' = 'bottom', align: 'start' | 'end' = 'start') {
  trigger = at
  await act(async () =>
    root.render(
      <Popover open>
        <PopoverTrigger data-test-trigger>位置</PopoverTrigger>
        <PopoverContent side={side} align={align} data-test-panel style={{ width: PANEL.width, height: PANEL.height }}>
          内容
        </PopoverContent>
      </Popover>,
    ),
  )
  // Floating UI positions asynchronously.
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
  const panel = document.querySelector<HTMLElement>('[data-test-panel]')!
  const wrapper = panel.parentElement!
  const m = /translate\((-?[\d.]+)px, (-?[\d.]+)px\)/.exec(wrapper.style.transform)
  expect(m, `positioned: ${wrapper.style.transform}`).not.toBeNull()
  return { x: Number(m![1]), y: Number(m![2]), side: panel.getAttribute('data-side'), panel }
}

describe('PopoverContent near the screen edges (375px wide)', () => {
  it('slides left to stay inside the right edge', async () => {
    // A chip at the right edge: aligned to its start, the panel would run 280px off screen.
    const p = await place({ left: 355, top: 100, width: 16, height: 20 })
    expect(p.x + PANEL.width).toBeLessThanOrEqual(VW - EDGE_GAP)
    expect(p.x).toBeGreaterThanOrEqual(EDGE_GAP)
    expect(p.side).toBe('bottom')
  })

  it('slides right to stay inside the left edge', async () => {
    const p = await place({ left: 4, top: 100, width: 16, height: 20 }, 'bottom', 'end')
    expect(p.x).toBeGreaterThanOrEqual(EDGE_GAP)
    expect(p.x + PANEL.width).toBeLessThanOrEqual(VW - EDGE_GAP)
  })

  it('flips above when there is no room below', async () => {
    const p = await place({ left: 100, top: 620, width: 16, height: 20 })
    expect(p.side).toBe('top')
    expect(p.y + PANEL.height).toBeLessThanOrEqual(620)
    expect(p.y).toBeGreaterThanOrEqual(EDGE_GAP)
  })

  it('caps its size to the screen and scrolls inside', async () => {
    const { panel } = await place({ left: 100, top: 100, width: 16, height: 20 })
    expect(panel.className).toContain('max-w-[calc(100vw-16px)]')
    expect(panel.className).toContain('max-h-[var(--radix-popover-content-available-height)]')
    expect(panel.className).toContain('overflow-y-auto')
    // Floating UI hands it the room it has on its side, minus the edge gap.
    const room = panel.parentElement!.style.getPropertyValue('--radix-popper-available-height')
    expect(parseFloat(room)).toBeLessThanOrEqual(VH - 120 - 6 - EDGE_GAP)
  })
})
