// The scene's floor: one open-plan office with your corner, the dispatch area
// (the supervisor, its helpers and the task board) and a workshop area per
// machine with a seat per slot, seen either at an angle (2.5D, isometric) or
// from the front and above (2D, which fills a screen of any shape). The pure
// part: the floor plan, the projection, routes, the camera and colors. What is
// where comes from dispatch.ts; drawing is in dispatch-draw.ts.

/** Width and height of one floor tile on screen, at scale 1. */
export const TW = 64
export const TH = 32

export interface Pt {
  x: number
  y: number
}

/**
 * How the floor is drawn: 'iso' at an angle (2.5D), 'flat' from the front and
 * above (2D: a floor tile is FT wide and FT × FLAT_K deep, heights go up).
 */
export type Projection = 'iso' | 'flat'
export const FT = 44
export const FLAT_K = 0.62
const FLAT_MIN = 0.5
const FLAT_MAX = 1.2
/** The 2D view starts at this share of the size that would fill the view. */
export const FLAT_START = 0.9

let projection: Projection = 'iso'
let flatK = FLAT_K

/** Sets how every function here projects (one scene draws at a time); k is the 2D depth squash. */
export function setProjection(p: Projection, k = flatK) {
  projection = p
  flatK = k
}

export function getProjection(): Projection {
  return projection
}

/** Floor position (tiles) to screen position (px, scale 1, origin at tile 0,0), in the current projection. */
export function iso(gx: number, gy: number): Pt {
  if (projection === 'flat') return { x: gx * FT, y: gy * FT * flatK }
  return { x: ((gx - gy) * TW) / 2, y: ((gx + gy) * TH) / 2 }
}

/** Screen position (px, scale 1) back to the floor position under it. */
export function unIso(x: number, y: number): Pt {
  if (projection === 'flat') return { x: x / FT, y: y / (FT * flatK) }
  const a = x / (TW / 2)
  const b = y / (TH / 2)
  return { x: (a + b) / 2, y: (b - a) / 2 }
}

export type RoomKind = 'office' | 'dispatch' | 'shop'

export interface Room {
  id: string
  kind: RoomKind
  /** Floor rectangle in tiles. */
  x0: number
  y0: number
  w: number
  d: number
  /** The machine of a workshop. */
  node?: string
}

/** Where a person is drawn and where things for them are dropped off. */
export interface Post {
  at: Pt
  hand: Pt
}

export type Staff = 'you' | 'sup' | 'reviewer' | 'approver' | 'inspector' | 'clock'

export interface Seat extends Post {
  /** The desk's back corner. */
  desk: Pt
}

export interface Layout {
  /** Floor size in tiles. */
  w: number
  d: number
  rooms: Room[]
  staff: Record<Staff, Post>
  /** The board hangs on the dispatch room's back wall from x0 to x1 (at y). */
  board: { x0: number; x1: number; y: number }
  /** Drop-off spots in front of each board column, left to right. */
  columns: Pt[]
  /** Width of the supervisor's desk (narrower when the dispatch area is). */
  supDesk: number
  /** How far the inspector walks along the front of the dispatch area. */
  beat: number
  /** Per machine: its seats, the first place in its queue (the next ones stand to the left), its entrance. */
  shops: Record<string, { room: Room; seats: Seat[]; queue: Pt; door: Pt }>
}

export const SEATS_PER_ROW = 3
/** A workshop desk's width (tiles) and the pitch between desks. */
export const DESK_W = 1.3
const DESK_PITCH = 2.3
/** Every workshop is this wide; the top row (your corner and the dispatch area) is as wide as a row of them. */
const SHOP_W = 8
const ROW_D = 2.1
/** Room between a workshop's back edge and its first row of desks. */
const TOP = 1.2
const TOP_D = 7
const OFFICE_W = 5

export const BOARD_COLUMNS = 6

/**
 * One open-plan floor, no walls and no corridors, on a grid: your corner and
 * the dispatch area across the top, then a workshop per machine in rows of
 * equal cells. Rooms are areas of the same floor. Without `aspect` a row
 * holds two workshops (three when there are many machines); with it (the 2D
 * view) the row width is the one whose floor best matches that screen shape
 * (width / height), down to one workshop a row on a phone.
 */
export function layout(machines: { id: string; seats: number }[], aspect?: number): Layout {
  if (!aspect) return build(machines, machines.length > 8 ? 3 : 2)
  let best = build(machines, 1)
  let miss = Infinity
  for (let k = 1; k <= Math.max(1, Math.min(6, machines.length)); k++) {
    const l = build(machines, k)
    // The depth squash that would fill the view exactly; startView can bend it from FLAT_MIN to FLAT_MAX.
    const need = ((l.w * FT) / aspect - 64) / (l.d * FT)
    const got = Math.max(FLAT_MIN, Math.min(FLAT_MAX, need))
    const m = Math.abs(Math.log(need / got)) + 0.1 * Math.abs(Math.log(got / FLAT_K))
    if (m < miss - 0.01) {
      best = l
      miss = m
    }
  }
  return best
}

/** The floor with `perRow` workshops a row. Your corner sits beside the dispatch area when the row is wide enough, else above it. */
function build(machines: { id: string; seats: number }[], perRow: number): Layout {
  const rowW = perRow * SHOP_W
  const side = rowW >= OFFICE_W + 11
  const office: Room = side ? { id: 'office', kind: 'office', x0: 0, y0: 0, w: OFFICE_W, d: TOP_D } : { id: 'office', kind: 'office', x0: 0, y0: 0, w: rowW, d: 4 }
  const dispatch: Room = side
    ? { id: 'dispatch', kind: 'dispatch', x0: OFFICE_W, y0: 0, w: rowW - OFFICE_W, d: TOP_D }
    : { id: 'dispatch', kind: 'dispatch', x0: 0, y0: office.d, w: rowW, d: TOP_D }
  const topD = dispatch.y0 + dispatch.d
  const X = dispatch.x0
  const Y = dispatch.y0
  const W = dispatch.w
  const ox = office.x0 + office.w / 2
  const staff: Record<Staff, Post> = {
    you: { at: { x: ox, y: office.y0 + (side ? 2.4 : 1.3) }, hand: { x: ox, y: office.y0 + (side ? 4.4 : 3.2) } },
    sup: { at: { x: X + W / 2, y: Y + 3 }, hand: { x: X + W / 2, y: Y + 4.7 } },
    reviewer: { at: { x: X + 2.2, y: Y + 3.2 }, hand: { x: X + 2.2, y: Y + 4.8 } },
    approver: { at: { x: X + W - 2.2, y: Y + 3.2 }, hand: { x: X + W - 2.2, y: Y + 4.8 } },
    inspector: { at: { x: X + 1.5, y: Y + 6 }, hand: { x: X + 3, y: Y + 6 } },
    clock: { at: { x: X + 0.5, y: Y + 1 }, hand: { x: X + 1, y: Y + 2 } },
  }
  const board = { x0: X + 1.2, x1: X + W - 0.6, y: Y }
  const colW = (board.x1 - board.x0) / BOARD_COLUMNS
  const columns = Array.from({ length: BOARD_COLUMNS }, (_, i) => ({ x: board.x0 + (i + 0.5) * colW, y: Y + 1.1 }))

  // Every workshop gets the same depth, so the rows line up.
  const rows = Math.max(1, ...machines.map((m) => Math.ceil(Math.max(1, m.seats) / SEATS_PER_ROW)))
  const d = Math.ceil(TOP + rows * ROW_D + 1)
  const rooms: Room[] = [office, dispatch]
  const shops: Layout['shops'] = {}
  machines.forEach((m, k) => {
    const sx = (k % perRow) * SHOP_W
    const sy = topD + Math.floor(k / perRow) * d
    const room: Room = { id: `shop:${m.id}`, kind: 'shop', x0: sx, y0: sy, w: SHOP_W, d, node: m.id }
    const seats: Seat[] = Array.from({ length: Math.max(1, m.seats) }, (_, i) => {
      const desk = { x: sx + 0.8 + (i % SEATS_PER_ROW) * DESK_PITCH, y: sy + TOP + Math.floor(i / SEATS_PER_ROW) * ROW_D + 0.4 }
      return { desk, at: { x: desk.x + DESK_W / 2, y: desk.y - 0.45 }, hand: { x: desk.x + DESK_W / 2, y: desk.y + 1.1 } }
    })
    shops[m.id] = { room, seats, queue: { x: sx + SHOP_W - 0.9, y: sy + d - 0.6 }, door: { x: sx + SHOP_W / 2, y: sy + d - 0.6 } }
    rooms.push(room)
  })
  const shopRows = Math.ceil(machines.length / perRow)
  return { w: rowW, d: topD + shopRows * d, rooms, staff, board, columns, shops, supDesk: W >= 11 ? 2.4 : 1.6, beat: W - 3 }
}

/** The place in a machine's queue: k = 0 is at the front. */
export function queueSpot(l: Layout, node: string, k: number): Pt | undefined {
  const s = l.shops[node]
  if (!s) return undefined
  return { x: s.queue.x - k * 0.7, y: s.queue.y }
}

/** The room whose floor is under a floor position, if any. */
export function roomAt(l: Layout, p: Pt): Room | undefined {
  return l.rooms.find((r) => p.x >= r.x0 && p.x < r.x0 + r.w && p.y >= r.y0 && p.y < r.y0 + r.d)
}

/**
 * The way between two floor positions on the open floor: across, then along,
 * then across again, so a courier walks the grid like people do in an office.
 */
export function route(_l: Layout, a: Pt, b: Pt): Pt[] {
  if (a.x === b.x || a.y === b.y) return simplify([a, b])
  const mid = (a.y + b.y) / 2
  return simplify([a, { x: a.x, y: mid }, { x: b.x, y: mid }, b])
}

/** Drops corners that lie on a straight line between their neighbours. */
export function simplify(pts: Pt[]): Pt[] {
  const out: Pt[] = []
  for (const p of pts) {
    const n = out.length
    if (n && out[n - 1].x === p.x && out[n - 1].y === p.y) continue
    if (n >= 2) {
      const a = out[n - 2]
      const b = out[n - 1]
      if ((a.x === b.x && b.x === p.x) || (a.y === b.y && b.y === p.y)) out[n - 1] = p
      else out.push(p)
    } else out.push(p)
  }
  return out
}

export function pathLength(pts: Pt[]): number {
  let n = 0
  for (let i = 1; i < pts.length; i++) n += Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y)
  return n
}

/** The point a fraction t (0..1) of the way along a path, and which way it is heading there. */
export function along(pts: Pt[], t: number): Pt & { dx: number; dy: number } {
  if (pts.length === 1) return { ...pts[0], dx: 0, dy: 1 }
  const total = pathLength(pts)
  let left = Math.max(0, Math.min(1, t)) * total
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1]
    const b = pts[i]
    const len = Math.hypot(b.x - a.x, b.y - a.y)
    const dx = len ? (b.x - a.x) / len : 0
    const dy = len ? (b.y - a.y) / len : 0
    if (left <= len || i === pts.length - 1) {
      const k = len ? Math.min(left, len) / len : 1
      return { x: a.x + (b.x - a.x) * k, y: a.y + (b.y - a.y) * k, dx, dy }
    }
    left -= len
  }
  const last = pts[pts.length - 1]
  return { ...last, dx: 0, dy: 1 }
}

/** Couriers walk this many tiles a second, but never take less than MIN_WALK or more than MAX_WALK. */
const SPEED = 10
export const MIN_WALK = 1.4
export const MAX_WALK = 3.6

/** Seconds a courier takes along a path. */
export function walkTime(pts: Pt[]): number {
  return Math.max(MIN_WALK, Math.min(MAX_WALK, pathLength(pts) / SPEED))
}

/** How recent (before the newest record) a step must be to walk. */
export const RECENT_MS = 5000

/** Most couriers on the floor at once. */
export const MAX_WALKERS = 16

/** Screen bounds of a floor (w × d tiles) at scale 1, with room above for walls and name plates. */
export function floorBounds(w: number, d: number): { left: number; top: number; right: number; bottom: number } {
  const corners = [iso(0, 0), iso(w, 0), iso(0, d), iso(w, d)]
  return {
    left: Math.min(...corners.map((p) => p.x)),
    right: Math.max(...corners.map((p) => p.x)),
    top: Math.min(...corners.map((p) => p.y)) - 64,
    bottom: Math.max(...corners.map((p) => p.y)),
  }
}

export interface Camera {
  scale: number
  /** Screen position of floor origin (tile 0,0) in css px. */
  ox: number
  oy: number
}

export const MIN_SCALE = 0.15
export const MAX_SCALE = 2.4

/**
 * The camera that shows the whole floor centred in a view of the given size,
 * leaving `right` px free on the right (for the feed panel over the canvas).
 */
export function fit(floor: { w: number; d: number }, width: number, height: number, pad = 24, right = 0): Camera {
  const b = floorBounds(floor.w, floor.d)
  const w = Math.max(200, width - right)
  const scale = clampScale(Math.min((w - pad * 2) / (b.right - b.left), (height - pad * 2) / (b.bottom - b.top)))
  return {
    scale,
    ox: w / 2 - ((b.left + b.right) / 2) * scale,
    oy: height / 2 - ((b.top + b.bottom) / 2) * scale,
  }
}

/**
 * The first view of a floor. A wide view shows all of it; a tall one (a
 * phone) would make it tiny, so it starts closer, on your office and the
 * dispatch room, and the rest is a drag away.
 */
export function startView(l: Layout, width: number, height: number, pad = 16): Camera {
  if (projection === 'flat') {
    // 2D fills the view: the floor's depth squash bends (within limits) to the view's shape.
    const scale = (width - pad * 2) / (l.w * FT)
    const k = ((height - pad * 2) / scale - 64) / (l.d * FT)
    flatK = Math.max(FLAT_MIN, Math.min(FLAT_MAX, k))
    // Shown at 90% of the full fit, so the floor has a little air around it.
    return zoomAt(fit(l, width, height, pad), FLAT_START, width / 2, height / 2)
  }
  const all = fit(l, width, height, pad)
  if (height <= width * 1.1) return all
  const office = l.rooms[0]
  const dispatch = l.rooms[1]
  const x0 = Math.min(office.x0, dispatch.x0)
  const x1 = Math.max(office.x0 + office.w, dispatch.x0 + dispatch.w)
  const y0 = Math.min(office.y0, dispatch.y0)
  const y1 = Math.max(office.y0 + office.d, dispatch.y0 + dispatch.d)
  const corners = [iso(x0, y0), iso(x1, y0), iso(x0, y1), iso(x1, y1)]
  const left = Math.min(...corners.map((p) => p.x))
  const right = Math.max(...corners.map((p) => p.x))
  const top = Math.min(...corners.map((p) => p.y)) - 64
  const bottom = Math.max(...corners.map((p) => p.y))
  const scale = clampScale(Math.max(all.scale, (width - pad * 2) / (right - left)))
  return { scale, ox: width / 2 - ((left + right) / 2) * scale, oy: height * 0.42 - ((top + bottom) / 2) * scale }
}

export function clampScale(s: number): number {
  return Math.max(MIN_SCALE, Math.min(MAX_SCALE, s))
}

/** Zooms by a factor while keeping the screen point (x, y) over the same floor position. */
export function zoomAt(cam: Camera, factor: number, x: number, y: number): Camera {
  const scale = clampScale(cam.scale * factor)
  const k = scale / cam.scale
  return { scale, ox: x - (x - cam.ox) * k, oy: y - (y - cam.oy) * k }
}

/** Screen (css px) to floor position under the camera. */
export function screenToFloor(cam: Camera, x: number, y: number): Pt {
  return unIso((x - cam.ox) / cam.scale, (y - cam.oy) / cam.scale)
}

/** Floor position to screen (css px) under the camera, lifted by h px of height at scale 1. */
export function floorToScreen(cam: Camera, gx: number, gy: number, h = 0): Pt {
  const p = iso(gx, gy)
  return { x: cam.ox + p.x * cam.scale, y: cam.oy + (p.y - h) * cam.scale }
}

/** A CSS color ("#2f8ad4", "#fff", "rgb(20 20 20 / .92)", "rgba(1,2,3,0.5)") as [r, g, b, a]. */
export function parseColor(s: string): [number, number, number, number] | null {
  const t = s.trim()
  let m = t.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i)
  if (m) {
    const h = m[1].length === 3 ? [...m[1]].map((c) => c + c).join('') : m[1]
    return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16), 1]
  }
  m = t.match(/^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[,/]\s*([\d.]+%?)\s*)?\)$/i)
  if (m) {
    let a = 1
    if (m[4] !== undefined) a = m[4].endsWith('%') ? parseFloat(m[4]) / 100 : parseFloat(m[4])
    return [Number(m[1]), Number(m[2]), Number(m[3]), a]
  }
  return null
}

/** A color with its alpha replaced (multiplied by the original alpha when keep is true). */
export function withAlpha(s: string, alpha: number, keep = false): string {
  const c = parseColor(s)
  if (!c) return s
  return `rgba(${c[0]}, ${c[1]}, ${c[2]}, ${keep ? c[3] * alpha : alpha})`
}

/** Mixes two colors (alpha ignored, result opaque): t = 0 is a, 1 is b. */
export function mix(a: string, b: string, t: number): string {
  const x = parseColor(a)
  const y = parseColor(b)
  if (!x || !y) return a
  const v = (i: number) => Math.round(x[i] + (y[i] - x[i]) * t)
  return `rgb(${v(0)}, ${v(1)}, ${v(2)})`
}

/** Shortens text to n characters with an ellipsis. */
export function clip(s: string, n: number): string {
  const chars = [...s]
  return chars.length <= n ? s : chars.slice(0, n - 1).join('') + '…'
}
