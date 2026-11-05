// Native-app behavior for phones and in-app WebViews, iOS first:
//
// - no pinch zoom (Safari ignores user-scalable=no, so its gesture events are cancelled);
// - the app follows the visual viewport, so the keyboard never pushes the page
//   up or hides the composer (--vv-h, --vv-top, --kb on <html>, and .kb-open);
// - bottom sheets can be dragged down to close (they close like a backdrop tap);
// - in a home-screen app or WebView, a swipe from the left edge goes back:
//   it presses the top layer's visible [data-back] button, or sends Escape.
//
// The math lives in gesture.ts. Everything here is wired by installTouch().
import { axisOf, EDGE, rubber, shouldDismiss, velocityOf, viewportVars, type Axis } from './gesture'
import { PHONE_QUERY } from './phone'

/** A home-screen app or an in-app WebView: no browser back gesture of its own. */
export function isStandalone(win: Window = window): boolean {
  const nav = win.navigator as Navigator & { standalone?: boolean }
  if (nav.standalone) return true
  if (win.matchMedia?.('(display-mode: standalone)').matches) return true
  // WKWebView's user agent has no "Safari/" token; Safari and in-app Safari views do.
  return /iP(hone|ad|od)/.test(nav.userAgent) && !/Safari\//.test(nav.userAgent)
}

const visible = (el: Element) => el.getClientRects().length > 0
const fixed = (el: Element) => getComputedStyle(el).position === 'fixed'

/** The topmost open dialog layer: the last fixed, visible [role=dialog] in document order. */
export function topLayer(doc: Document = document): HTMLElement | null {
  const all = [...doc.querySelectorAll<HTMLElement>('[role="dialog"]')].filter((el) => visible(el) && fixed(el))
  return all.pop() ?? null
}

/** The back button a swipe presses in a layer: the last visible [data-back], drawn on top. */
export function backButton(layer: HTMLElement): HTMLElement | null {
  return [...layer.querySelectorAll<HTMLElement>('[data-back]')].filter(visible).pop() ?? null
}

/** The scroll container a touch on `el` would scroll, up to (not including) `stop`. */
function scrollerOf(el: Element | null, stop: Element): HTMLElement | null {
  for (let n = el; n && n !== stop; n = n.parentElement) {
    const s = getComputedStyle(n)
    if ((s.overflowY === 'auto' || s.overflowY === 'scroll') && n.scrollHeight > n.clientHeight) return n as HTMLElement
  }
  return null
}

const noDrag = 'input, textarea, select, [contenteditable="true"], .xterm, [data-no-drag]'
const spring = 'transform .34s cubic-bezier(.2,.9,.25,1), opacity .34s'

function settle(el: HTMLElement, done?: () => void) {
  el.style.transition = spring
  el.style.transform = ''
  const end = () => {
    el.style.transition = ''
    el.style.willChange = ''
    done?.()
  }
  setTimeout(end, 360)
}

interface Drag {
  kind: 'sheet' | 'edge'
  x0: number
  y0: number
  axis: Axis
  active: boolean
  /** The element that moves with the finger. */
  el: HTMLElement
  /** The layer being dismissed (edge) or the sheet itself. */
  layer: HTMLElement
  backdrop: HTMLElement | null
  scroller: HTMLElement | null
  samples: { t: number; v: number }[]
}

/** Installs everything above on the page; returns a function that removes it. */
export function installTouch(win: Window = window): () => void {
  const doc = win.document
  const root = doc.documentElement
  const phone = win.matchMedia?.(PHONE_QUERY)
  const offs: (() => void)[] = []
  const on = <K extends string>(t: EventTarget, type: K, fn: (e: Event) => void, opts?: AddEventListenerOptions) => {
    t.addEventListener(type, fn, opts)
    offs.push(() => t.removeEventListener(type, fn, opts))
  }

  if (isStandalone(win)) root.dataset.standalone = '1'

  // Pinch zoom: WebKit's gesture events, plus any two-finger move.
  for (const type of ['gesturestart', 'gesturechange', 'gestureend']) on(doc, type, (e) => e.preventDefault(), { passive: false })

  // The visible part of the screen, for the keyboard.
  const vv = win.visualViewport
  if (vv) {
    const apply = () => {
      const v = viewportVars({ inner: win.innerHeight, height: vv.height, offsetTop: vv.offsetTop })
      root.style.setProperty('--vv-h', `${v.h}px`)
      root.style.setProperty('--vv-top', `${v.top}px`)
      root.style.setProperty('--kb', `${v.kb}px`)
      root.classList.toggle('kb-open', v.open)
    }
    apply()
    on(vv, 'resize', apply)
    on(vv, 'scroll', apply)
  }

  let drag: Drag | null = null

  on(doc, 'touchstart', (ev) => {
    const e = ev as TouchEvent
    drag = null
    if (e.touches.length !== 1 || !phone?.matches) return
    const t = e.touches[0]
    const target = e.target as Element | null
    if (!target?.closest) return

    // A swipe from the left edge, in a home-screen app or WebView.
    if (t.clientX <= EDGE && root.dataset.standalone) {
      const layer = topLayer(doc)
      if (!layer) return
      const back = backButton(layer)
      // A back button inside a page of the layer moves just that page.
      const page = back?.closest<HTMLElement>('[data-page]')
      const el = page && layer.contains(page) ? page : layer
      drag = { kind: 'edge', x0: t.clientX, y0: t.clientY, axis: null, active: false, el, layer, backdrop: null, scroller: null, samples: [] }
      return
    }

    // A drag on a bottom sheet.
    const sheet = target.closest<HTMLElement>('.sheet')
    if (!sheet || !fixed(sheet) || target.closest(noDrag)) return
    const prev = sheet.previousElementSibling
    drag = {
      kind: 'sheet', x0: t.clientX, y0: t.clientY, axis: null, active: false, el: sheet, layer: sheet,
      backdrop: prev?.classList.contains('backdrop') ? (prev as HTMLElement) : null,
      scroller: scrollerOf(target, sheet), samples: [],
    }
  }, { passive: true })

  on(doc, 'touchmove', (ev) => {
    const e = ev as TouchEvent
    if (e.touches.length > 1) {
      e.preventDefault()
      return
    }
    const d = drag
    if (!d) return
    const t = e.touches[0]
    const dx = t.clientX - d.x0
    const dy = t.clientY - d.y0
    if (!d.active) {
      d.axis = d.axis ?? axisOf(dx, dy)
      if (!d.axis) return
      const go = d.kind === 'edge' ? d.axis === 'x' && dx > 0 : d.axis === 'y' && dy > 0 && (!d.scroller || d.scroller.scrollTop <= 0)
      if (!go) {
        drag = null
        return
      }
      d.active = true
      d.el.style.transition = 'none'
      d.el.style.willChange = 'transform'
    }
    e.preventDefault()
    const v = d.kind === 'edge' ? dx : dy
    d.samples.push({ t: performance.now(), v })
    if (d.samples.length > 6) d.samples.shift()
    const shown = v >= 0 ? v : -rubber(-v, 40)
    d.el.style.transform = d.kind === 'edge' ? `translateX(${shown}px)` : `translateY(${shown}px)`
    if (d.backdrop) d.backdrop.style.opacity = String(Math.max(0, 1 - v / Math.max(1, d.el.offsetHeight)))
  }, { passive: false })

  const end = () => {
    const d = drag
    drag = null
    if (!d?.active) return
    const last = d.samples[d.samples.length - 1]?.v ?? 0
    const size = d.kind === 'edge' ? win.innerWidth : d.el.offsetHeight
    const reset = () => {
      if (d.backdrop) {
        d.backdrop.style.transition = 'opacity .34s'
        d.backdrop.style.opacity = ''
      }
      settle(d.el)
    }
    if (!shouldDismiss(last, velocityOf(d.samples, performance.now()), size)) return reset()

    d.el.style.transition = 'transform .24s cubic-bezier(.3,.7,.4,1)'
    d.el.style.transform = d.kind === 'edge' ? `translateX(${size}px)` : `translateY(${size + 40}px)`
    if (d.backdrop) {
      d.backdrop.style.transition = 'opacity .24s'
      d.backdrop.style.opacity = '0'
    }
    setTimeout(() => {
      if (d.kind === 'sheet') {
        d.backdrop?.click()
      } else {
        const back = backButton(d.layer)
        if (back) {
          back.click()
          // The same layer shows the previous page now: put it back in place at once.
          d.el.style.transition = 'none'
          d.el.style.transform = ''
          d.el.style.willChange = ''
          return
        }
        doc.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      }
      // Still here after its close animation: it does not close this way.
      setTimeout(() => { if (d.layer.isConnected) reset() }, 400)
    }, 230)
  }
  on(doc, 'touchend', end)
  on(doc, 'touchcancel', end)

  return () => {
    for (const off of offs) off()
    delete root.dataset.standalone
  }
}
