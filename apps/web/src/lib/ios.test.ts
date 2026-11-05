// The touch behavior from ios.ts, driven with synthetic touch events on a
// fake phone window (happy-dom has no layout, so sizes are stubbed).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { backButton, installTouch, isStandalone, topLayer } from './ios'
import { PHONE_QUERY } from './phone'

type Pt = { x: number; y: number }

function touch(target: EventTarget, type: string, pts: Pt[]) {
  const e = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(e, 'touches', { value: type === 'touchend' ? [] : pts.map((p) => ({ clientX: p.x, clientY: p.y })) })
  target.dispatchEvent(e)
  return e
}

/** A drag from `a` to `b` in a few steps, then the finger lifts. */
function drag(target: EventTarget, a: Pt, b: Pt, steps = 5) {
  touch(target, 'touchstart', [a])
  for (let i = 1; i <= steps; i++) touch(target, 'touchmove', [{ x: a.x + ((b.x - a.x) * i) / steps, y: a.y + ((b.y - a.y) * i) / steps }])
  touch(target, 'touchend', [])
}

function fakeWindow({ phone = true, standalone = true } = {}) {
  const vv = Object.assign(new EventTarget(), { height: 844, offsetTop: 0 })
  const win = {
    document,
    navigator: { userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 27_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148', standalone },
    matchMedia: (q: string) => ({ matches: q === PHONE_QUERY ? phone : false }),
    visualViewport: vv,
    innerHeight: 844,
    innerWidth: 390,
  }
  return { win: win as unknown as Window, vv }
}

/** A backdrop + bottom sheet, the way Sheet.tsx draws them. */
function mountSheet(html = '<p>内容</p>') {
  const closed = vi.fn()
  const backdrop = document.createElement('div')
  backdrop.className = 'backdrop'
  backdrop.addEventListener('click', () => {
    closed()
    backdrop.remove()
    sheet.remove()
  })
  const sheet = document.createElement('div')
  sheet.className = 'sheet'
  sheet.setAttribute('role', 'dialog')
  sheet.style.position = 'fixed'
  sheet.innerHTML = html
  document.body.append(backdrop, sheet)
  Object.defineProperty(sheet, 'offsetHeight', { value: 500 })
  return { sheet, backdrop, closed }
}

let uninstall: (() => void) | undefined
const rects = Element.prototype.getClientRects

beforeEach(() => {
  vi.useFakeTimers()
  // Everything attached counts as on screen unless it is display:none.
  Element.prototype.getClientRects = function (this: Element) {
    const hidden = (this as HTMLElement).style?.display === 'none' || !this.isConnected
    return (hidden ? [] : [{}]) as unknown as DOMRectList
  }
})

afterEach(() => {
  uninstall?.()
  uninstall = undefined
  document.body.innerHTML = ''
  document.documentElement.removeAttribute('style')
  document.documentElement.className = ''
  Element.prototype.getClientRects = rects
  vi.useRealTimers()
})

describe('isStandalone', () => {
  it('knows a home-screen app, a WebView and Safari apart', () => {
    const base = { matchMedia: () => ({ matches: false }) }
    const ua = (s: string, standalone = false) => ({ ...base, navigator: { userAgent: s, standalone } }) as unknown as Window
    expect(isStandalone(ua('Mozilla/5.0 (iPhone) AppleWebKit Version/27.0 Mobile/15E148 Safari/604.1'))).toBe(false)
    expect(isStandalone(ua('Mozilla/5.0 (iPhone) AppleWebKit Version/27.0 Mobile/15E148 Safari/604.1', true))).toBe(true)
    expect(isStandalone(ua('Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148'))).toBe(true)
    expect(isStandalone(ua('Mozilla/5.0 (Macintosh) AppleWebKit Chrome/140 Safari/537.36'))).toBe(false)
  })
})

describe('topLayer and backButton', () => {
  it('pick the last fixed dialog and its last visible back button', () => {
    document.body.innerHTML = `
      <div role="dialog" id="a" style="position:fixed"><button data-back id="a1"></button></div>
      <div class="embed"><div role="dialog" id="embedded" style="position:static"></div></div>
      <div role="dialog" id="b" style="position:fixed">
        <button data-back id="b1"></button>
        <button data-back id="b2" style="display:none"></button>
      </div>`
    const layer = topLayer()
    expect(layer?.id).toBe('b')
    expect(backButton(layer!)?.id).toBe('b1')
  })
  it('find nothing when no dialog is open', () => {
    document.body.innerHTML = '<main></main>'
    expect(topLayer()).toBeNull()
  })
})

describe('installTouch', () => {
  it('marks a home-screen app and cleans up', () => {
    uninstall = installTouch(fakeWindow().win)
    expect(document.documentElement.dataset.standalone).toBe('1')
    uninstall()
    uninstall = undefined
    expect(document.documentElement.dataset.standalone).toBeUndefined()
  })

  it('cancels pinch zoom', () => {
    uninstall = installTouch(fakeWindow().win)
    const g = new Event('gesturestart', { cancelable: true })
    document.dispatchEvent(g)
    expect(g.defaultPrevented).toBe(true)
    const two = touch(document.body, 'touchmove', [{ x: 10, y: 10 }, { x: 100, y: 100 }])
    expect(two.defaultPrevented).toBe(true)
  })

  it('sizes the app to the part above the keyboard', () => {
    const { win, vv } = fakeWindow()
    uninstall = installTouch(win)
    const root = document.documentElement
    expect(root.style.getPropertyValue('--vv-h')).toBe('844px')
    expect(root.classList.contains('kb-open')).toBe(false)
    vv.height = 500
    vv.dispatchEvent(new Event('resize'))
    expect(root.style.getPropertyValue('--vv-h')).toBe('500px')
    expect(root.style.getPropertyValue('--kb')).toBe('344px')
    expect(root.classList.contains('kb-open')).toBe(true)
    vv.height = 844
    vv.dispatchEvent(new Event('resize'))
    expect(root.classList.contains('kb-open')).toBe(false)
  })

  describe('sheet drag', () => {
    it('closes the sheet like a backdrop tap after a long pull down', () => {
      uninstall = installTouch(fakeWindow().win)
      const { sheet, closed } = mountSheet()
      const p = sheet.querySelector('p')!
      touch(p, 'touchstart', [{ x: 100, y: 400 }])
      touch(p, 'touchmove', [{ x: 100, y: 450 }])
      const move = touch(p, 'touchmove', [{ x: 101, y: 620 }])
      expect(move.defaultPrevented).toBe(true)
      expect(sheet.style.transform).toBe('translateY(220px)')
      touch(p, 'touchend', [])
      expect(closed).not.toHaveBeenCalled()
      vi.advanceTimersByTime(250)
      expect(closed).toHaveBeenCalledOnce()
      expect(sheet.isConnected).toBe(false)
    })

    it('springs back after a short pull', () => {
      uninstall = installTouch(fakeWindow().win)
      const { sheet, closed } = mountSheet()
      drag(sheet, { x: 100, y: 400 }, { x: 100, y: 440 }, 20)
      vi.advanceTimersByTime(1000)
      expect(closed).not.toHaveBeenCalled()
      expect(sheet.style.transform).toBe('')
    })

    it('lets the content scroll when it is not at the top', () => {
      uninstall = installTouch(fakeWindow().win)
      const { sheet, closed } = mountSheet('<div class="list" style="overflow-y:auto"><p>很多行</p></div>')
      const list = sheet.querySelector<HTMLElement>('.list')!
      Object.defineProperty(list, 'scrollHeight', { value: 1000 })
      Object.defineProperty(list, 'clientHeight', { value: 300 })
      list.scrollTop = 120
      drag(list.querySelector('p')!, { x: 100, y: 300 }, { x: 100, y: 600 })
      vi.advanceTimersByTime(1000)
      expect(closed).not.toHaveBeenCalled()
      expect(sheet.style.transform).toBe('')
    })

    it('ignores drags that start on a field, and upward drags', () => {
      uninstall = installTouch(fakeWindow().win)
      const { sheet, closed } = mountSheet('<input /><p>内容</p>')
      drag(sheet.querySelector('input')!, { x: 100, y: 300 }, { x: 100, y: 700 })
      drag(sheet.querySelector('p')!, { x: 100, y: 600 }, { x: 100, y: 200 })
      vi.advanceTimersByTime(1000)
      expect(closed).not.toHaveBeenCalled()
      expect(sheet.style.transform).toBe('')
    })

    it('does nothing outside the phone layout', () => {
      uninstall = installTouch(fakeWindow({ phone: false }).win)
      const { sheet, closed } = mountSheet()
      drag(sheet, { x: 100, y: 300 }, { x: 100, y: 700 })
      vi.advanceTimersByTime(1000)
      expect(closed).not.toHaveBeenCalled()
    })
  })

  describe('edge swipe back', () => {
    function mountLayer(html: string) {
      const layer = document.createElement('div')
      layer.setAttribute('role', 'dialog')
      layer.style.position = 'fixed'
      layer.innerHTML = html
      document.body.append(layer)
      return layer
    }

    it('presses the top layer\'s back button and moves only its page', () => {
      uninstall = installTouch(fakeWindow().win)
      const layer = mountLayer('<button data-back id="close"></button><section data-page><button data-back id="list"></button></section>')
      const pressed: string[] = []
      layer.addEventListener('click', (e) => pressed.push((e.target as HTMLElement).id))
      const page = layer.querySelector<HTMLElement>('[data-page]')!
      touch(document.body, 'touchstart', [{ x: 8, y: 400 }])
      touch(document.body, 'touchmove', [{ x: 60, y: 402 }])
      touch(document.body, 'touchmove', [{ x: 240, y: 405 }])
      expect(page.style.transform).toBe('translateX(232px)')
      expect(layer.style.transform).toBe('')
      touch(document.body, 'touchend', [])
      vi.advanceTimersByTime(250)
      expect(pressed).toEqual(['list'])
      expect(page.style.transform).toBe('')
    })

    it('sends Escape when the layer has no back button', () => {
      uninstall = installTouch(fakeWindow().win)
      const layer = mountLayer('<p>项目</p>')
      const keys: string[] = []
      const onKey = (e: KeyboardEvent) => {
        keys.push(e.key)
        layer.remove()
      }
      window.addEventListener('keydown', onKey)
      drag(document.body, { x: 5, y: 300 }, { x: 300, y: 310 })
      vi.advanceTimersByTime(1000)
      window.removeEventListener('keydown', onKey)
      expect(keys).toEqual(['Escape'])
    })

    it('springs back on a short swipe, and is off in Safari', () => {
      uninstall = installTouch(fakeWindow().win)
      const layer = mountLayer('<button data-back></button>')
      const back = vi.fn()
      layer.querySelector('button')!.addEventListener('click', back)
      drag(document.body, { x: 5, y: 300 }, { x: 40, y: 300 }, 20)
      vi.advanceTimersByTime(1000)
      expect(back).not.toHaveBeenCalled()
      expect(layer.style.transform).toBe('')

      uninstall()
      uninstall = installTouch(fakeWindow({ standalone: false }).win)
      // The fake user agent reads as a WebView; clear the mark to stand for Safari.
      delete document.documentElement.dataset.standalone
      drag(document.body, { x: 5, y: 300 }, { x: 300, y: 300 })
      vi.advanceTimersByTime(1000)
      expect(back).not.toHaveBeenCalled()
    })

    it('does nothing without an open layer', () => {
      uninstall = installTouch(fakeWindow().win)
      const e = touch(document.body, 'touchstart', [{ x: 5, y: 300 }])
      const m = touch(document.body, 'touchmove', [{ x: 200, y: 300 }])
      touch(document.body, 'touchend', [])
      expect(e.defaultPrevented || m.defaultPrevented).toBe(false)
    })
  })
})
