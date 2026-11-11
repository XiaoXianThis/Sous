import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EDGE_GAP, TIP_DELAY, TipLayer } from './tooltip'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const VW = 375
const VH = 667
const TIP = { width: 160, height: 32 }

let root: Root
let el: HTMLDivElement
let triggerRect = { left: 100, top: 100, width: 34, height: 34 }
let hoverEnabled = true

const rect = (left: number, top: number, width: number, height: number) =>
  ({ left, top, width, height, x: left, y: top, right: left + width, bottom: top + height, toJSON: () => ({}) }) as DOMRect

beforeEach(() => {
  vi.useFakeTimers()
  hoverEnabled = true
  triggerRect = { left: 100, top: 100, width: 34, height: 34 }

  vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(VW)
  vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(VH)
  vi.spyOn(document.documentElement, 'clientWidth', 'get').mockReturnValue(VW)
  vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(VH)
  vi.spyOn(window, 'matchMedia').mockImplementation((q: string) => ({
    matches: q === '(hover: hover)' ? hoverEnabled : q === '(hover: none)' ? !hoverEnabled : false,
    media: q,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }))

  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.hasAttribute('data-tip')) {
      return rect(triggerRect.left, triggerRect.top, triggerRect.width, triggerRect.height)
    }
    if (this.hasAttribute('data-tip-layer')) {
      const x = parseFloat(this.style.left) || 0
      const y = parseFloat(this.style.top) || 0
      return rect(x, y, TIP.width, TIP.height)
    }
    return rect(0, 0, VW, VH)
  })

  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})

afterEach(async () => {
  await act(async () => root.unmount())
  el.remove()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function makePointerEvent(type: string, init: PointerEventInit & { relatedTarget?: EventTarget | null } = {}) {
  if (typeof PointerEvent === 'function') return new PointerEvent(type, init)
  return Object.assign(new MouseEvent(type, init), { pointerType: init.pointerType ?? 'mouse' })
}

async function hoverIn(target: Element, pointerType = 'mouse') {
  await act(async () => {
    target.dispatchEvent(makePointerEvent('pointerover', { bubbles: true, pointerType }))
    vi.advanceTimersByTime(TIP_DELAY)
  })
}

async function hoverOut(target: Element) {
  await act(async () => {
    target.dispatchEvent(makePointerEvent('pointerout', { bubbles: true, relatedTarget: document.body }))
  })
}

describe('TipLayer global portal tooltip', () => {
  it('① flips above a bottom button so top + height stays inside the viewport', async () => {
    // ContextRing at the bottom of the screen (top: 625, height: 34 -> bottom: 659 on a 667px screen).
    triggerRect = { left: 320, top: 625, width: 34, height: 34 }
    await act(async () =>
      root.render(
        <div>
          <button data-tip="上下文 42% · 缓存命中 88%" data-tip-pos="bottom">
            ring
          </button>
          <TipLayer />
        </div>,
      ),
    )

    const btn = el.querySelector('button')!
    await hoverIn(btn)

    const tip = document.body.querySelector<HTMLElement>('[role="tooltip"]')!
    expect(tip).not.toBeNull()
    expect(tip.textContent).toBe('上下文 42% · 缓存命中 88%')
    expect(tip.dataset.side).toBe('top')

    const box = tip.getBoundingClientRect()
    expect(box.top).toBeGreaterThanOrEqual(EDGE_GAP)
    expect(box.top + box.height).toBeLessThanOrEqual(VH - EDGE_GAP)
    expect(box.top + box.height).toBeLessThanOrEqual(triggerRect.top)
    expect(box.left + box.width).toBeLessThanOrEqual(VW - EDGE_GAP)
  })

  it('② flips left or shifts left for buttons on the right edge', async () => {
    // Default right-positioned tooltip on a button near the right edge (e.g. Models / Mcp card action buttons).
    triggerRect = { left: 330, top: 200, width: 34, height: 34 }
    await act(async () =>
      root.render(
        <div>
          <button data-tip="有角色在用，不能删除">删除</button>
          <TipLayer />
        </div>,
      ),
    )

    const btn = el.querySelector('button')!
    await hoverIn(btn)

    const tip = document.body.querySelector<HTMLElement>('[role="tooltip"]')!
    expect(tip).not.toBeNull()
    expect(tip.dataset.side).toBe('left')
    const box = tip.getBoundingClientRect()
    expect(box.left).toBeGreaterThanOrEqual(EDGE_GAP)
    expect(box.left + box.width).toBeLessThanOrEqual(triggerRect.left - 10)
    expect(box.left + box.width).toBeLessThanOrEqual(VW - EDGE_GAP)

    // Bottom-positioned tooltip on a button near the right edge shifts left to stay 8px inside the viewport.
    await hoverOut(btn)
    btn.setAttribute('data-tip-pos', 'bottom')
    await hoverIn(btn)

    const tipBottom = document.body.querySelector<HTMLElement>('[role="tooltip"]')!
    const boxBottom = tipBottom.getBoundingClientRect()
    expect(boxBottom.left).toBeGreaterThanOrEqual(EDGE_GAP)
    expect(boxBottom.left + boxBottom.width).toBeLessThanOrEqual(VW - EDGE_GAP)
  })

  it('③ portals into document.body outside overflow:hidden/auto containers without changing scrollWidth/scrollHeight', async () => {
    triggerRect = { left: 330, top: 625, width: 34, height: 34 }
    await act(async () =>
      root.render(
        <div
          data-scroll-box
          style={{ width: '200px', height: '60px', overflow: 'auto', position: 'relative' }}
        >
          <div style={{ overflow: 'hidden' }}>
            <button data-tip="看改动 · 12 files changed, 340 insertions(+), 89 deletions(-)" data-tip-pos="bottom">
              diff
            </button>
          </div>
          <TipLayer />
        </div>,
      ),
    )

    const scrollBox = el.querySelector<HTMLElement>('[data-scroll-box]')!
    const btn = el.querySelector('button')!

    const docWBefore = document.documentElement.scrollWidth
    const docHBefore = document.documentElement.scrollHeight
    const boxWBefore = scrollBox.scrollWidth
    const boxHBefore = scrollBox.scrollHeight

    await hoverIn(btn)

    const tip = document.body.querySelector<HTMLElement>('[role="tooltip"]')!
    expect(tip).not.toBeNull()
    // Rendered directly under document.body, NOT inside the button or the scroll/overflow:hidden wrapper.
    expect(tip.parentElement).toBe(document.body)
    expect(btn.contains(tip)).toBe(false)
    expect(scrollBox.contains(tip)).toBe(false)

    // Neither documentElement nor the overflow:auto container grows a scrollbar when the tooltip appears.
    expect(document.documentElement.scrollWidth).toBe(docWBefore)
    expect(document.documentElement.scrollHeight).toBe(docHBefore)
    expect(scrollBox.scrollWidth).toBe(boxWBefore)
    expect(scrollBox.scrollHeight).toBe(boxHBefore)

    // Long text wraps instead of forcing nowrap overflow, and z-index sits above Dialog/Sheet (31/41) and Popover (60).
    expect(tip.className).toContain('max-w-[min(280px,calc(100vw-16px))]')
    expect(tip.className).toContain('whitespace-normal')
    expect(tip.className).toContain('z-[80]')
    expect(tip.className).toContain('pointer-events-none')
  })

  it('④ does not render a tooltip on hover in a touch environment (hover: none), but still supports keyboard focus', async () => {
    hoverEnabled = false
    await act(async () =>
      root.render(
        <div>
          <button data-tip="设置">设置</button>
          <TipLayer />
        </div>,
      ),
    )

    const btn = el.querySelector('button')!
    await hoverIn(btn, 'mouse')
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()

    await hoverIn(btn, 'touch')
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()

    // Keyboard focus still shows the tooltip for accessibility.
    await act(async () => {
      btn.dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
    })
    const tip = document.body.querySelector<HTMLElement>('[role="tooltip"]')!
    expect(tip).not.toBeNull()
    expect(tip.textContent).toBe('设置')

    await act(async () => {
      btn.dispatchEvent(new FocusEvent('focusout', { bubbles: true, relatedTarget: document.body }))
    })
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()
  })

  it('waits ~300ms before showing and hides immediately on pointerout', async () => {
    await act(async () =>
      root.render(
        <div>
          <button data-tip="复制">复制</button>
          <TipLayer />
        </div>,
      ),
    )

    const btn = el.querySelector('button')!
    await act(async () => {
      btn.dispatchEvent(makePointerEvent('pointerover', { bubbles: true, pointerType: 'mouse' }))
      vi.advanceTimersByTime(TIP_DELAY - 50)
    })
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()

    // Sweeping away before 300ms cancels the pending tooltip.
    await hoverOut(btn)
    await act(async () => {
      vi.advanceTimersByTime(100)
    })
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()

    // Resting for 300ms shows it, and pointerout hides it immediately.
    await hoverIn(btn)
    expect(document.body.querySelector('[role="tooltip"]')?.textContent).toBe('复制')
    await hoverOut(btn)
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull()
  })
})
