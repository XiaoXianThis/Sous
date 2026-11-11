import { afterEach, describe, expect, it } from 'vitest'
import {
  along,
  clip,
  fit,
  floorBounds,
  floorToScreen,
  iso,
  layout,
  MAX_SCALE,
  MAX_WALK,
  MIN_WALK,
  mix,
  parseColor,
  pathLength,
  queueSpot,
  roomAt,
  route,
  screenToFloor,
  setProjection,
  simplify,
  startView,
  unIso,
  walkTime,
  withAlpha,
  zoomAt,
  type Pt,
} from './office'

describe('projection', () => {
  it('round-trips floor and screen positions', () => {
    for (const [x, y] of [[0, 0], [3.5, 7], [33, 1.25]]) {
      const p = iso(x, y)
      const b = unIso(p.x, p.y)
      expect(b.x).toBeCloseTo(x)
      expect(b.y).toBeCloseTo(y)
    }
  })
  it('round-trips through a camera', () => {
    const cam = { scale: 0.7, ox: 120, oy: -40 }
    const s = floorToScreen(cam, 5, 9)
    const g = screenToFloor(cam, s.x, s.y)
    expect(g.x).toBeCloseTo(5)
    expect(g.y).toBeCloseTo(9)
  })
})

describe('layout', () => {
  const l = layout([
    { id: 'a', seats: 2 },
    { id: 'b', seats: 5 },
    { id: 'c', seats: 1 },
    { id: 'd', seats: 3 },
  ])
  it('has your office, the dispatch room and a workshop per machine, none overlapping', () => {
    expect(l.rooms.map((r) => r.id)).toEqual(['office', 'dispatch', 'shop:a', 'shop:b', 'shop:c', 'shop:d'])
    for (const a of l.rooms) {
      expect(Number.isInteger(a.x0) && Number.isInteger(a.y0) && Number.isInteger(a.w) && Number.isInteger(a.d)).toBe(true)
      for (const b of l.rooms) {
        if (a === b) continue
        const apart = a.x0 + a.w <= b.x0 || b.x0 + b.w <= a.x0 || a.y0 + a.d <= b.y0 || b.y0 + b.d <= a.y0
        expect(apart).toBe(true)
      }
    }
  })
  it('gives each machine its seats inside its workshop', () => {
    expect(l.shops.b.seats).toHaveLength(5)
    for (const [id, s] of Object.entries(l.shops)) {
      expect(roomAt(l, s.queue)?.id).toBe(`shop:${id}`)
      for (const seat of s.seats) {
        expect(roomAt(l, seat.at)?.id).toBe(`shop:${id}`)
        expect(roomAt(l, seat.hand)?.id).toBe(`shop:${id}`)
      }
    }
    expect(queueSpot(l, 'a', 1)!.x).toBeLessThan(queueSpot(l, 'a', 0)!.x)
    expect(queueSpot(l, 'zz', 0)).toBeUndefined()
  })
  it('lays one open floor on a grid with no gaps', () => {
    const [office, dispatch, ...shops] = l.rooms
    expect(office.y0).toBe(dispatch.y0)
    expect(office.x0 + office.w).toBe(dispatch.x0)
    expect(dispatch.x0 + dispatch.w).toBe(l.w)
    // Equal cells in neat rows, covering the floor edge to edge.
    expect(new Set(shops.map((r) => r.d)).size).toBe(1)
    expect(new Set(shops.map((r) => r.w)).size).toBe(1)
    for (const r of shops) expect(r.x0 % r.w).toBe(0)
    const cells = l.rooms.reduce((n, r) => n + r.w * r.d, 0)
    expect(cells).toBe(l.w * l.d)
  })
  it('uses three workshops a row when there are many machines', () => {
    const many = layout(Array.from({ length: 9 }, (_, i) => ({ id: `m${i}`, seats: 2 })))
    expect(new Set(Object.values(many.shops).map((s) => s.room.x0)).size).toBe(3)
    expect(many.rooms[1].x0 + many.rooms[1].w).toBe(many.w)
  })
  it('keeps the people where they work', () => {
    expect(roomAt(l, l.staff.you.at)?.id).toBe('office')
    for (const k of ['sup', 'reviewer', 'approver', 'inspector'] as const) {
      expect(roomAt(l, l.staff[k].at)?.id).toBe('dispatch')
      expect(roomAt(l, l.staff[k].hand)?.id).toBe('dispatch')
    }
    for (const c of l.columns) expect(roomAt(l, c)?.id).toBe('dispatch')
  })
})

describe('routes', () => {
  const l = layout([
    { id: 'a', seats: 3 },
    { id: 'b', seats: 4 },
  ])
  const spots: Pt[] = [l.staff.you.hand, l.staff.sup.hand, l.staff.approver.hand, l.columns[0], l.columns[5], l.shops.a.seats[2].hand, l.shops.b.seats[3].hand, l.shops.b.queue]
  it('walks along the floor axes from one spot to the other', () => {
    for (const a of spots) {
      for (const b of spots) {
        if (a === b) continue
        const pts = route(l, a, b)
        expect(pts[0]).toEqual(a)
        expect(pts[pts.length - 1]).toEqual(b)
        for (let i = 1; i < pts.length; i++) expect(pts[i - 1].x === pts[i].x || pts[i - 1].y === pts[i].y).toBe(true)
        for (const p of pts) {
          expect(p.x).toBeGreaterThanOrEqual(0)
          expect(p.y).toBeGreaterThanOrEqual(0)
          expect(p.x).toBeLessThanOrEqual(l.w)
          expect(p.y).toBeLessThanOrEqual(l.d)
        }
      }
    }
  })
  it('drops corners on a straight line', () => {
    expect(simplify([{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }, { x: 2, y: 1 }, { x: 2, y: 1 }])).toEqual([{ x: 0, y: 0 }, { x: 2, y: 0 }, { x: 2, y: 1 }])
  })
  it('finds points along a path', () => {
    const pts = [{ x: 0, y: 0 }, { x: 4, y: 0 }, { x: 4, y: 4 }]
    expect(pathLength(pts)).toBe(8)
    expect(along(pts, 0.25)).toMatchObject({ x: 2, y: 0 })
    expect(along(pts, 0.75)).toMatchObject({ x: 4, y: 2, dx: 0, dy: 1 })
    expect(along(pts, 2)).toMatchObject({ x: 4, y: 4 })
  })
  it('keeps walks between bounds', () => {
    const near = route(l, l.staff.sup.hand, l.staff.approver.hand)
    const far = route(l, l.staff.you.hand, l.shops.b.seats[3].hand)
    expect(walkTime(near)).toBeGreaterThanOrEqual(MIN_WALK)
    expect(walkTime(far)).toBeLessThanOrEqual(MAX_WALK)
    expect(walkTime(far)).toBeGreaterThan(walkTime(near))
  })
})

describe('camera', () => {
  const floor = { w: 40, d: 18 }
  it('fits the floor inside the view, leaving room on the right', () => {
    const cam = fit(floor, 1200, 700, 24, 300)
    const b = floorBounds(floor.w, floor.d)
    expect(cam.ox + b.left * cam.scale).toBeGreaterThanOrEqual(23.9)
    expect(cam.ox + b.right * cam.scale).toBeLessThanOrEqual(1200 - 300 - 23.9)
    expect(cam.oy + b.top * cam.scale).toBeGreaterThanOrEqual(23.9)
    expect(cam.oy + b.bottom * cam.scale).toBeLessThanOrEqual(676.1)
  })
  it('starts closer on a tall view, on the office and the dispatch room', () => {
    const l = layout([{ id: 'a', seats: 2 }, { id: 'b', seats: 2 }, { id: 'c', seats: 2 }])
    expect(startView(l, 1200, 700)).toEqual(fit(l, 1200, 700, 16))
    const tall = startView(l, 360, 640)
    expect(tall.scale).toBeGreaterThan(fit(l, 360, 640, 16).scale)
    const d = l.rooms[1]
    const c = floorToScreen(tall, d.x0 + d.w / 2, d.y0 + d.d / 2)
    expect(c.x).toBeGreaterThan(0)
    expect(c.x).toBeLessThan(360)
  })
  it('zooms about the pointer and clamps', () => {
    const cam = { scale: 1, ox: 0, oy: 0 }
    const before = screenToFloor(cam, 300, 200)
    const after = screenToFloor(zoomAt(cam, 1.5, 300, 200), 300, 200)
    expect(after.x).toBeCloseTo(before.x)
    expect(after.y).toBeCloseTo(before.y)
    expect(zoomAt(cam, 100, 0, 0).scale).toBe(MAX_SCALE)
  })
})

describe('colors and text', () => {
  it('parses the forms the theme uses', () => {
    expect(parseColor('#2f8ad4')).toEqual([47, 138, 212, 1])
    expect(parseColor('#fff')).toEqual([255, 255, 255, 1])
    expect(parseColor('rgb(20 20 20 / .92)')).toEqual([20, 20, 20, 0.92])
    expect(parseColor('rgba(1, 2, 3, 0.5)')).toEqual([1, 2, 3, 0.5])
    expect(parseColor('rgb(1 2 3 / 50%)')).toEqual([1, 2, 3, 0.5])
    expect(parseColor('var(--x)')).toBeNull()
  })
  it('changes alpha and mixes', () => {
    expect(withAlpha('#000', 0.3)).toBe('rgba(0, 0, 0, 0.3)')
    expect(withAlpha('rgb(20 20 20 / .5)', 0.5, true)).toBe('rgba(20, 20, 20, 0.25)')
    expect(mix('#000000', '#ffffff', 0.5)).toBe('rgb(128, 128, 128)')
  })
  it('clips long text', () => {
    expect(clip('abc', 5)).toBe('abc')
    expect(clip('主管调用很长很长的模型名字', 6)).toBe('主管调用很…')
  })
})

describe('2D', () => {
  afterEach(() => setProjection('iso', 0.62))
  it('projects straight from the front, and back', () => {
    setProjection('flat')
    const p = iso(3, 5)
    expect(p.x).toBeCloseTo(3 * 44)
    const b = unIso(p.x, p.y)
    expect(b.x).toBeCloseTo(3)
    expect(b.y).toBeCloseTo(5)
  })
  it('plans the floor for the screen shape: one workshop a row when tall, several when wide', () => {
    const ms = Array.from({ length: 5 }, (_, i) => ({ id: `m${i}`, seats: 2 }))
    const tall = layout(ms, 0.3)
    const wide = layout(ms, 2.4)
    const perRow = (l: ReturnType<typeof layout>) => new Set(Object.values(l.shops).map((s) => s.room.x0)).size
    expect(perRow(tall)).toBe(1)
    expect(perRow(wide)).toBeGreaterThan(2)
    // On a narrow floor your corner sits above the dispatch area, still edge to edge.
    const [office, dispatch] = tall.rooms
    expect(office.w).toBe(tall.w)
    expect(dispatch.y0).toBe(office.y0 + office.d)
    for (const k of ['sup', 'reviewer', 'approver'] as const) expect(roomAt(tall, tall.staff[k].at)?.id).toBe('dispatch')
    expect(roomAt(tall, tall.staff.you.at)?.id).toBe('office')
    expect(tall.supDesk).toBeLessThan(2.4)
  })
  it('fills the view: the whole floor shows, edge to edge', () => {
    setProjection('flat', 0.62)
    const l = layout([{ id: 'a', seats: 2 }, { id: 'b', seats: 2 }, { id: 'c', seats: 2 }], 0.6)
    const cam = startView(l, 360, 620)
    const b = floorBounds(l.w, l.d)
    const width = (b.right - b.left) * cam.scale
    const height = (b.bottom - b.top) * cam.scale
    // Fills the view both ways, then starts at 90% of that, centred.
    expect(width).toBeCloseTo(328 * 0.9, -1)
    expect(height).toBeGreaterThan(580 * 0.9)
    expect(height).toBeLessThanOrEqual(588.1 * 0.9)
    const left = cam.ox + b.left * cam.scale
    expect(left).toBeCloseTo(360 - (left + width), 0)
  })
})
