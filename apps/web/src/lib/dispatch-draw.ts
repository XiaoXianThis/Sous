// Draws the dispatch floor on a 2D canvas: your office, the dispatch room
// (the supervisor, its helpers and the task board), a workshop per machine
// with an agent at each busy seat, and a courier for every move from
// dispatch.ts walking from one spot to another. Layout and geometry are in
// office.ts. Colors come from the page's CSS variables, so it follows the
// light and dark themes; state is shown only in black, white, gray and the
// accent (wood and the cork board are decor, agent logos are identity).
import { AGENT_MARKS, agentTitle } from './agents'
import { ACTOR_NAME, COLUMNS, type Actor, type Floor, type Move, type Spot } from './dispatch'
import { oneLine, type Live } from './live'
import {
  DESK_W,
  getProjection,
  along,
  clip,
  floorToScreen,
  iso,
  mix,
  queueSpot,
  roomAt,
  route,
  screenToFloor,
  walkTime,
  withAlpha,
  MAX_WALKERS,
  BOARD_COLUMNS,
  type Camera,
  type Layout,
  type Pt,
  type Room,
} from './office'
import type { Overview, Run } from './types'

const BOARD_TOP = 56
const CARD_PITCH = 7.5
const BUBBLE_MS = 3200
const ARRIVE_MS = 700
const MAX_BUBBLES = 2

export interface Palette {
  dark: boolean
  font: string
  bg: string
  panel: string
  raised: string
  sunken: string
  line: string
  lineStrong: string
  text: string
  muted: string
  faint: string
  accent: string
  accentText: string
  wood: string
  leaf: string
  skin: string
}

/** The theme's colors, read from the CSS variables on <html>. */
export function readPalette(): Palette {
  const css = getComputedStyle(document.documentElement)
  const v = (name: string, fallback: string) => css.getPropertyValue(name).trim() || fallback
  const dark = document.documentElement.dataset.theme === 'dark'
  return {
    dark,
    font: v('--font', 'sans-serif'),
    bg: v('--bg', dark ? '#181818' : '#fcfcfc'),
    panel: v('--panel', dark ? '#1e1e1e' : '#ffffff'),
    raised: v('--raised', dark ? '#232323' : '#ffffff'),
    sunken: v('--sunken', dark ? '#141414' : '#f3f3f3'),
    line: v('--line', 'rgba(20,20,20,.08)'),
    lineStrong: v('--line-strong', 'rgba(20,20,20,.14)'),
    text: v('--text', dark ? '#e4e4e4' : '#141414'),
    muted: v('--muted', 'rgba(20,20,20,.7)'),
    faint: v('--faint', 'rgba(20,20,20,.6)'),
    accent: v('--accent', '#2f8ad4'),
    accentText: v('--accent-text', '#ffffff'),
    wood: dark ? '#5d5244' : '#dcc9ab',
    leaf: dark ? '#55704f' : '#93b28c',
    skin: dark ? '#b9a693' : '#f1dfcc',
  }
}

/** What a click lands on. */
export type Target =
  | { kind: 'staff'; actor: Actor }
  | { kind: 'run'; id: string }
  | { kind: 'column'; col: number }
  | { kind: 'machine'; node: string }
  | { kind: 'move'; move: Move }

export interface SceneInput {
  layout: Layout
  floor: Floor
  data: Overview
  live: Live
  /** The supervisor is answering someone. */
  supBusy: boolean
  /** In focus: tasks, runs and conversations (null: everything). */
  isFocused: (refs: { task_id?: string; run_id?: string; thread_id?: string }) => boolean
  focused: boolean
}

interface Walker {
  move: Move
  path: Pt[]
  start: number
  dur: number
  more: number
}

interface Bubble {
  move: Move
  at: Pt
  start: number
}

type Drawable = { depth: number; draw: () => void }
type Hit = { x: number; y: number; w: number; h: number; target: Target }

const MERGE_MS = 700

/**
 * The scene between frames: camera, couriers, bubbles, hover. React sets
 * `input` and calls add() with new moves; a requestAnimationFrame loop calls
 * draw(). Nothing here re-creates the canvas, so nothing flickers.
 */
export class DispatchScene {
  cam: Camera = { scale: 1, ox: 0, oy: 0 }
  width = 0
  height = 0
  reduced = false
  hover: Target | null = null
  input: SceneInput | null = null
  pal: Palette | null = null
  private walkers: Walker[] = []
  private bubbles: Bubble[] = []
  private arrived: { at: Pt; t: number }[] = []
  private hits: Hit[] = []
  private paths = new Map<string, Path2D>()

  /** Where a spot is on the floor: the drop-off point, and where a bubble about it floats. */
  where(s: Spot): { hand: Pt; head: Pt } | null {
    const l = this.input?.layout
    const f = this.input?.floor
    if (!l || !f) return null
    switch (s.kind) {
      case 'actor': {
        const p = l.staff[s.actor]
        return { hand: p.hand, head: p.at }
      }
      case 'board': {
        const i = COLUMNS.findIndex((c) => c.id === s.col)
        const c = l.columns[Math.max(0, i)]
        return { hand: c, head: { x: c.x, y: c.y - 1 } }
      }
      case 'run': {
        const shop = f.shops.find((x) => x.node.id === s.node)
        const i = shop?.seats.findIndex((r) => r?.id === s.run) ?? -1
        const seat = i >= 0 ? l.shops[s.node]?.seats[i] : undefined
        if (seat) return { hand: seat.hand, head: seat.at }
        return this.where({ kind: 'machine', node: s.node })
      }
      case 'machine': {
        const shop = l.shops[s.node]
        if (!shop) return null
        const r = shop.room
        return { hand: shop.door, head: { x: r.x0 + r.w / 2, y: r.y0 + r.d / 2 } }
      }
    }
  }

  /** Starts couriers and bubbles for moves that just happened (oldest first). */
  add(moves: Move[], now: number) {
    let spawned = 0
    for (const m of moves.slice(-MAX_WALKERS)) {
      const a = this.where(m.from)
      const b = this.where(m.to)
      if (!a || !b) continue
      const same = a.hand.x === b.hand.x && a.hand.y === b.hand.y
      if (m.item === 'bubble' || same || this.reduced) {
        this.bubbles.push({ move: m, at: b.head, start: now })
        continue
      }
      const at = now + spawned * 120
      const dup = this.walkers.find((w) => w.move.from.kind === m.from.kind && JSON.stringify(w.move.from) === JSON.stringify(m.from) && JSON.stringify(w.move.to) === JSON.stringify(m.to) && at - w.start < MERGE_MS)
      if (dup) {
        dup.more++
        dup.move = m
        continue
      }
      const path = route(this.input!.layout, a.hand, b.hand)
      this.walkers.push({ move: m, path, start: at, dur: walkTime(path) * 1000, more: 0 })
      spawned++
    }
    this.walkers = this.walkers.slice(-MAX_WALKERS)
    const per = new Map<string, Bubble[]>()
    for (const b of this.bubbles) {
      const k = `${b.at.x},${b.at.y}`
      per.set(k, [...(per.get(k) ?? []), b])
    }
    this.bubbles = [...per.values()].flatMap((l) => l.slice(-MAX_BUBBLES))
  }

  /** What is under a screen point (css px). */
  hit(x: number, y: number): Target | null {
    for (let i = this.hits.length - 1; i >= 0; i--) {
      const h = this.hits[i]
      if (x >= h.x && x <= h.x + h.w && y >= h.y && y <= h.y + h.h) return h.target
    }
    const l = this.input?.layout
    if (!l) return null
    const room = roomAt(l, screenToFloor(this.cam, x, y))
    if (room?.kind === 'shop' && room.node) return { kind: 'machine', node: room.node }
    if (room?.kind === 'office') return { kind: 'staff', actor: 'you' }
    return null
  }

  draw(ctx: CanvasRenderingContext2D, dpr: number, now: number) {
    const pal = this.pal
    const input = this.input
    if (!pal || !input) return
    this.walkers = this.walkers.filter((w) => {
      if (now < w.start + w.dur) return true
      this.arrived.push({ at: w.path[w.path.length - 1], t: w.start + w.dur })
      return false
    })
    this.arrived = this.arrived.filter((a) => now - a.t < ARRIVE_MS)
    this.bubbles = this.bubbles.filter((b) => now < b.start + BUBBLE_MS)
    this.hits = []

    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, this.width, this.height)
    const { scale, ox, oy } = this.cam
    ctx.setTransform(dpr * scale, 0, 0, dpr * scale, dpr * ox, dpr * oy)

    const d = new Draw(ctx, pal, !this.reduced, now)
    const { layout, floor, data } = input
    d.ground(layout)
    for (const r of layout.rooms) {
      const shop = r.node ? floor.shops.find((s) => s.node.id === r.node) : undefined
      const hover = this.hover?.kind === 'machine' && this.hover.node === r.node
      d.roomFloor(r, !shop || shop.node.online, hover)
    }

    const items: Drawable[] = []
    const add = (depth: number, draw: () => void) => items.push({ depth, draw })
    // The board stands at the back of the dispatch area; nothing is behind it.
    add(-0.5, () => d.board(layout, floor, -Infinity, Infinity))

    // Your office.
    const you = layout.staff.you
    const ox0 = layout.rooms[0]
    add(you.at.x + you.at.y, () => d.person(you.at, mix(pal.text, pal.panel, 0.35), { sit: true }))
    add(you.at.x + you.at.y + 0.9, () => d.desk(you.at, floor.mine.length))

    // The dispatch room.
    const clock = layout.staff.clock
    add(clock.at.x + clock.at.y, () => d.clock(clock.at, input.supBusy))
    const sup = layout.staff.sup
    add(sup.at.x + sup.at.y, () => d.person(sup.at, input.supBusy ? pal.accent : mix(pal.text, pal.panel, 0.45), { sit: true, typing: input.supBusy, idle: !input.supBusy }))
    add(sup.at.x + sup.at.y + 1, () => d.desk(sup.at, 0, layout.supDesk))
    for (const k of ['reviewer', 'approver'] as const) {
      const p = layout.staff[k]
      const busy = this.recent(k, now)
      add(p.at.x + p.at.y, () => d.person(p.at, busy ? pal.accent : mix(pal.text, pal.panel, 0.55), { sit: true, typing: busy, idle: !busy }))
      add(p.at.x + p.at.y + 0.9, () => d.desk(p.at, k === 'approver' ? floor.theirs.length : 0, 1.4))
    }
    // The inspector walks a beat along the front of the room; its tag goes with it.
    const beat = d.motion ? (Math.sin(now / 2200) + 1) / 2 : 0.5
    const inspector = { x: layout.staff.inspector.at.x + beat * layout.beat, y: layout.staff.inspector.at.y }
    add(inspector.x + inspector.y, () => d.person(inspector, mix(pal.text, pal.panel, 0.3), { walking: d.motion, phase: 1.3 }))

    // Workshops.
    for (const shop of floor.shops) {
      const ls = layout.shops[shop.node.id]
      if (!ls) continue
      shop.seats.forEach((run, i) => {
        const seat = ls.seats[i]
        if (!seat) return
        const focus = !run || input.isFocused({ run_id: run.id, task_id: run.task_id || undefined, thread_id: run.thread_id || undefined })
        const asking = !!run && data.approvals.some((a) => a.status === 'pending' && a.run_id === run.id)
        if (run) {
          add(seat.at.x + seat.at.y, () => d.fade(focus, () => d.person(seat.at, run.paused ? mix(pal.text, pal.panel, 0.55) : pal.accent, { sit: true, typing: !run.paused && !asking, hand: asking, idle: !!run.paused })))
        } else {
          add(seat.at.x + seat.at.y, () => d.chair(seat.at))
        }
        add(seat.desk.x + DESK_W / 2 + seat.desk.y + 0.35, () => d.fade(focus, () => d.workDesk(seat.desk, !!run && shop.node.online && !run.paused)))
      })
      shop.queue.slice(0, 4).forEach((run, k) => {
        const at = queueSpot(layout, shop.node.id, k)!
        const focus = input.isFocused({ run_id: run.id, task_id: run.task_id || undefined })
        add(at.x + at.y, () => d.fade(focus, () => d.person(at, mix(pal.text, pal.panel, 0.5), { carry: 'ticket', phase: k })))
      })
    }

    for (const a of this.arrived) add(a.at.x + a.at.y - 0.01, () => d.ring(a.at, (now - a.t) / ARRIVE_MS))
    for (const w of this.walkers) {
      const t = (now - w.start) / w.dur
      if (t < 0) continue
      const p = along(w.path, t)
      const fade = Math.min(1, t / 0.08, (1 - t) / 0.1)
      add(-1, () => d.trail(w.path, t))
      add(p.x + p.y, () => d.person(p, pal.accent, { walking: true, phase: p.x, fade, carry: w.move.item, ok: w.move.ok !== false }))
    }
    items.sort((a, b) => a.depth - b.depth)
    for (const it of items) it.draw()

    // Words and marks at a fixed size.
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    const t = new Words(ctx, pal, this.cam, this.hits)
    t.columns(layout, floor)
    for (const shop of floor.shops) {
      const ls = layout.shops[shop.node.id]
      if (!ls) continue
      const busy = shop.seats.filter(Boolean).length
      t.plate(ls.room, shop.node.name, shop.node.online ? `${busy}/${shop.seats.length} 个工位在干活${shop.queue.length ? ` · ${shop.queue.length} 个排队` : ''}` : '离线', shop.node.online ? (busy ? 'busy' : 'ok') : 'down', this.hover?.kind === 'machine' && this.hover.node === shop.node.id, { kind: 'machine', node: shop.node.id })
    }
    // Zoomed far out (a phone), only tags that ask for something stay.
    const tiny = this.cam.scale < 0.32
    const tag = (a: Actor, extra = '', lit = false) => (tiny && !lit && a !== 'sup') ? undefined : t.tag(a === 'inspector' ? inspector : layout.staff[a].at, ACTOR_NAME[a], extra, lit, { kind: 'staff', actor: a }, this.hover?.kind === 'staff' && this.hover.actor === a)
    tag('you', floor.mine.length ? `${floor.mine.length} 件待你批` : '', floor.mine.length > 0)
    tag('sup', input.supBusy ? '忙着回复' : '')
    tag('reviewer')
    tag('approver', floor.theirs.length ? `${floor.theirs.length} 件在看` : '', floor.theirs.length > 0)
    tag('inspector')
    const detail = this.cam.scale >= 0.85
    for (const shop of floor.shops) {
      const ls = layout.shops[shop.node.id]
      if (!ls) continue
      shop.seats.forEach((run, i) => {
        if (!run || !ls.seats[i]) return
        const focus = input.isFocused({ run_id: run.id, task_id: run.task_id || undefined, thread_id: run.thread_id || undefined })
        const hovered = this.hover?.kind === 'run' && this.hover.id === run.id
        const acts = input.live[run.id]
        const line = acts?.length ? oneLine(acts[acts.length - 1], 40) : ''
        t.agent(ls.seats[i].at, run, this.path(run.agent), (detail || hovered) && focus ? headline(run, line, data) : '', focus, hovered, false, tiny)
      })
      shop.queue.slice(0, 4).forEach((run, k) => {
        const at = queueSpot(layout, shop.node.id, k)!
        t.agent(at, run, this.path(run.agent), '', input.isFocused({ run_id: run.id, task_id: run.task_id || undefined }), false, true, tiny)
      })
    }
    for (const b of this.bubbles) t.bubble(b.at, b.move, (now - b.start) / BUBBLE_MS, this.bubbles.filter((x) => x.at === b.at && x.start > b.start).length)
    if (input.supBusy && !this.bubbles.some((b) => b.at === layout.staff.sup.at)) t.thinking(layout.staff.sup.at, now)
    for (const w of this.walkers) {
      const k = (now - w.start) / w.dur
      if (k < 0) continue
      const p = along(w.path, k)
      const text = w.more ? `${clip(w.move.text, 10)} 等 ${w.more + 1} 件` : w.move.text
      t.label(p, text, Math.min(1, k / 0.08, (1 - k) / 0.1), w.move)
    }
  }

  /** Whether a helper handled something in the last few seconds (it looks busy). */
  private recent(a: Actor, now: number): boolean {
    const hit = (s: Spot) => s.kind === 'actor' && s.actor === a
    return this.walkers.some((w) => (hit(w.move.from) && now - w.start < 1500) || (hit(w.move.to) && now - (w.start + w.dur) > -400)) || this.bubbles.some((b) => hit(b.move.to))
  }

  private path(agent: string): { mark: (typeof AGENT_MARKS)[string] | undefined; path: Path2D | undefined } {
    const key = (agent || '').toLowerCase()
    const mark = AGENT_MARKS[key]
    if (!mark) return { mark, path: undefined }
    let p = this.paths.get(key)
    if (!p && typeof Path2D !== 'undefined') {
      p = new Path2D(mark.d)
      this.paths.set(key, p)
    }
    return { mark, path: p }
  }
}

/** What an agent is on, in a line: its task, then the latest thing it did. */
function headline(run: Run, line: string, data: Overview): string {
  const task = run.task_id ? data.tasks.find((t) => t.id === run.task_id) : undefined
  const what = clip(task?.title || run.title || '', 14)
  return line ? `${what}${what ? '：' : ''}${clip(line, 18)}` : what
}

/** World-space drawing (the camera transform is on the context). */
class Draw {
  constructor(
    private ctx: CanvasRenderingContext2D,
    private pal: Palette,
    readonly motion: boolean,
    private now: number,
  ) {}

  fade(on: boolean, draw: () => void) {
    if (on) return draw()
    this.ctx.save()
    this.ctx.globalAlpha = 0.28
    draw()
    this.ctx.restore()
  }

  private shade(c: string, k: number): string {
    return mix(c, '#000000', this.pal.dark ? k * 1.6 : k)
  }

  poly(pts: Pt[], fill: string | CanvasGradient, stroke?: string, width = 1) {
    const ctx = this.ctx
    ctx.beginPath()
    pts.forEach((p, i) => (i ? ctx.lineTo(p.x, p.y) : ctx.moveTo(p.x, p.y)))
    ctx.closePath()
    ctx.fillStyle = fill
    ctx.fill()
    if (stroke) {
      ctx.strokeStyle = stroke
      ctx.lineWidth = width
      ctx.stroke()
    }
  }

  private up(p: Pt, h: number): Pt {
    return { x: p.x, y: p.y - h }
  }

  private rect(x0: number, y0: number, w: number, d: number): Pt[] {
    return [iso(x0, y0), iso(x0 + w, y0), iso(x0 + w, y0 + d), iso(x0, y0 + d)]
  }

  /** The one floor everything stands on: a slab with a tiled top. */
  ground(l: Layout) {
    const { pal } = this
    const [a, b, c, d] = this.rect(0, 0, l.w, l.d)
    const depth = 12
    const floor = pal.dark ? mix(pal.panel, '#ffffff', 0.06) : pal.panel
    this.poly([d, c, { x: c.x, y: c.y + depth }, { x: d.x, y: d.y + depth }], this.shade(floor, 0.14))
    this.poly([c, b, { x: b.x, y: b.y + depth }, { x: c.x, y: c.y + depth }], this.shade(floor, 0.24))
    this.poly([a, b, c, d], floor)
    const alt = mix(floor, pal.dark ? '#ffffff' : '#000000', 0.03)
    for (let i = 0; i < l.w; i++) for (let j = 0; j < l.d; j++) if (!((i + j) % 2)) this.poly(this.rect(i, j, 1, 1), alt)
  }

  /** An area of the floor: a faint tint and a thin line round it (dimmed when its machine is off). */
  roomFloor(r: Room, lit: boolean, hover: boolean) {
    const { pal } = this
    if (r.kind === 'office') this.poly(this.rect(r.x0, r.y0, r.w, r.d), withAlpha(pal.wood, pal.dark ? 0.18 : 0.22))
    if (!lit) this.poly(this.rect(r.x0, r.y0, r.w, r.d), withAlpha('#000000', pal.dark ? 0.35 : 0.08))
    const inset = 0.08
    this.poly(this.rect(r.x0 + inset, r.y0 + inset, r.w - inset * 2, r.d - inset * 2), 'transparent', hover ? pal.accent : withAlpha(pal.text, pal.dark ? 0.12 : 0.1), hover ? 2.5 : 1)
  }

  /** A box standing on the floor (gx, gy: back corner; w along x, d along y; h px tall). */
  box(gx: number, gy: number, w: number, d: number, h: number, color: string, lift = 0) {
    const [p00, p10, p11, p01] = this.rect(gx, gy, w, d).map((p) => this.up(p, lift))
    const edge = withAlpha(this.pal.text, this.pal.dark ? 0.1 : 0.08)
    this.poly([p01, p11, this.up(p11, h), this.up(p01, h)], this.shade(color, 0.08), edge)
    this.poly([p11, p10, this.up(p10, h), this.up(p11, h)], this.shade(color, 0.16), edge)
    this.poly([this.up(p00, h), this.up(p10, h), this.up(p11, h), this.up(p01, h)], color, edge)
  }

  /** A rectangle on a front (+y) face at gy: u0..u1 along x, z0..z1 up. */
  onY(gy: number, u0: number, u1: number, z0: number, z1: number, fill: string, stroke?: string) {
    this.poly([this.up(iso(u0, gy), z0), this.up(iso(u1, gy), z0), this.up(iso(u1, gy), z1), this.up(iso(u0, gy), z1)], fill, stroke, 0.75)
  }

  /** A rectangle on a right (+x) face at gx: v0..v1 along y. */
  onX(gx: number, v0: number, v1: number, z0: number, z1: number, fill: string) {
    this.poly([this.up(iso(gx, v0), z0), this.up(iso(gx, v1), z0), this.up(iso(gx, v1), z1), this.up(iso(gx, v0), z1)], fill)
  }

  /** A desk in front of someone at `at`, with a stack of papers on it. */
  desk(at: Pt, papers: number, w = 1.6) {
    const { pal } = this
    const x = at.x - w / 2
    const y = at.y + 0.45
    this.box(x, y, w, 0.8, 13, pal.wood)
    this.box(x + 0.15, y + 0.25, 0.5, 0.12, 11, mix(pal.text, pal.panel, 0.2), 13)
    for (let k = 0; k < Math.min(6, papers); k++) this.box(x + w - 0.75, y + 0.2, 0.5, 0.4, 1.6, k === Math.min(6, papers) - 1 ? pal.accent : mix(pal.panel, '#ffffff', 0.6), 13 + k * 1.8)
  }

  /** An agent's desk in a workshop, its screen lit while it works. */
  workDesk(desk: Pt, lit: boolean) {
    const { pal } = this
    const m = (DESK_W - 0.5) / 2
    this.box(desk.x, desk.y, DESK_W, 0.7, 13, pal.wood)
    this.box(desk.x + m, desk.y + 0.22, 0.5, 0.12, 12, mix(pal.text, pal.panel, 0.2), 13)
    this.onY(desk.y + 0.34, desk.x + m + 0.04, desk.x + m + 0.46, 15, 23, lit ? pal.accent : mix(pal.text, pal.panel, 0.35))
  }

  chair(at: Pt) {
    this.box(at.x - 0.25, at.y - 0.2, 0.5, 0.45, 7, mix(this.pal.text, this.pal.panel, 0.6))
  }

  /** The part of the task board between xa and xb: a column per state, a card per task. */
  board(l: Layout, f: Floor, xa: number, xb: number) {
    const { pal } = this
    const { x0, x1, y } = l.board
    const gy = y + 0.03
    const part = (u0: number, u1: number, z0: number, z1: number, fill: string, stroke?: string) => {
      const a = Math.max(u0, xa)
      const b = Math.min(u1, xb)
      if (b > a) this.onY(gy, a, b, z0, z1, fill, stroke)
    }
    // A cork board (decor) with paper cards; the card color says the column.
    const paper = '#f6f2e8'
    part(x0 - 0.1, x1 + 0.1, 4, BOARD_TOP + 2, mix(pal.text, pal.panel, 0.5))
    part(x0, x1, 6, BOARD_TOP, pal.dark ? '#6b5d4a' : '#d8c3a0')
    const w = (x1 - x0) / BOARD_COLUMNS
    COLUMNS.forEach((c, i) => {
      const u0 = x0 + i * w + 0.12
      const u1 = x0 + (i + 1) * w - 0.12
      if (i) part(x0 + i * w - 0.015, x0 + i * w + 0.015, 8, BOARD_TOP - 2, withAlpha('#000000', 0.25))
      // As many cards as fit on the board (the count is in the column head).
      const fit = Math.floor((BOARD_TOP - 8 - 8) / CARD_PITCH)
      f.board[c.id].slice(0, fit).forEach((_, k) => {
        const z1 = BOARD_TOP - 8 - k * CARD_PITCH
        const fill = c.id === 'doing' ? pal.accent : c.id === 'blocked' ? '#3a3a3a' : c.id === 'done' ? mix(paper, '#808080', 0.35) : paper
        part(u0, u1, z1 - 5.5, z1, fill, withAlpha('#000000', 0.25))
      })
    })
  }

  /** A standing clock: its face shows the time. */
  clock(at: Pt, busy: boolean) {
    const ctx = this.ctx
    this.box(at.x - 0.18, at.y - 0.15, 0.36, 0.3, 30, mix(this.pal.wood, '#000000', 0.25))
    const c = this.up(iso(at.x, at.y), 38)
    ctx.fillStyle = mix(this.pal.panel, '#ffffff', 0.5)
    ctx.strokeStyle = withAlpha(this.pal.text, 0.5)
    ctx.lineWidth = 1.5
    ctx.beginPath()
    ctx.arc(c.x, c.y, 9, 0, Math.PI * 2)
    ctx.fill()
    ctx.stroke()
    const d = new Date()
    const hand = (a: number, len: number, w: number) => {
      ctx.lineWidth = w
      ctx.beginPath()
      ctx.moveTo(c.x, c.y)
      ctx.lineTo(c.x + Math.sin(a) * len, c.y - Math.cos(a) * len)
      ctx.stroke()
    }
    ctx.strokeStyle = this.pal.text
    hand(((d.getHours() % 12) + d.getMinutes() / 60) * (Math.PI / 6), 4.5, 2)
    hand(d.getMinutes() * (Math.PI / 30), 7, 1.5)
    ctx.strokeStyle = busy ? this.pal.accent : withAlpha(this.pal.text, 0.5)
    hand(d.getSeconds() * (Math.PI / 30), 7.5, 1)
  }

  /** A little person at a floor position. */
  person(g: Pt, shirt: string, o: { typing?: boolean; sit?: boolean; walking?: boolean; phase?: number; idle?: boolean; hand?: boolean; fade?: number; carry?: Move['item']; ok?: boolean } = {}) {
    const { ctx, pal, now } = this
    const p = iso(g.x, g.y)
    const ph = o.phase ?? 0
    const m = this.motion
    const bob = m ? (o.walking ? Math.abs(Math.sin(now / 110 + ph)) * -2.5 : o.typing ? Math.sin(now / 90 + ph) * 0.8 : Math.sin(now / 900 + ph) * 0.5) : 0
    ctx.save()
    ctx.globalAlpha *= o.fade ?? 1
    ctx.fillStyle = withAlpha('#000000', pal.dark ? 0.35 : 0.13)
    ctx.beginPath()
    ctx.ellipse(p.x, p.y, 9, 4.5, 0, 0, Math.PI * 2)
    ctx.fill()
    const lift = o.sit ? -4 : 0
    if (!o.sit) {
      const swing = o.walking && m ? Math.sin(now / 110 + ph) * 2.5 : 0
      ctx.fillStyle = mix(pal.text, pal.panel, 0.25)
      ctx.fillRect(p.x - 4 + swing, p.y - 9, 3, 9)
      ctx.fillRect(p.x + 1 - swing, p.y - 9, 3, 9)
    }
    const top = p.y - 25 + bob - lift
    ctx.fillStyle = shirt
    ctx.beginPath()
    ctx.roundRect(p.x - 6.5, top, 13, 17, 5)
    ctx.fill()
    if (o.typing && m) {
      const a = Math.sin(now / 70 + ph) * 1.5
      ctx.fillRect(p.x - 9, top + 6 + a, 3, 7)
      ctx.fillRect(p.x + 6, top + 6 - a, 3, 7)
    }
    if (o.hand) {
      // One arm up: asking for something.
      const wave = m ? Math.sin(now / 160) * 1.5 : 0
      ctx.fillRect(p.x + 6, top - 9 + wave, 3, 13)
      ctx.beginPath()
      ctx.arc(p.x + 7.5, top - 10 + wave, 2.6, 0, Math.PI * 2)
      ctx.fillStyle = pal.skin
      ctx.fill()
    }
    const hy = top - 6
    ctx.fillStyle = pal.skin
    ctx.strokeStyle = withAlpha(pal.text, 0.25)
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.arc(p.x, hy, 6.5, 0, Math.PI * 2)
    ctx.fill()
    ctx.stroke()
    ctx.fillStyle = mix(pal.text, pal.panel, 0.15)
    ctx.beginPath()
    ctx.arc(p.x, hy - 1, 6.6, Math.PI * 1.05, Math.PI * 1.95)
    ctx.fill()
    if (o.carry) this.item(p.x, top + 3 + (o.walking ? 0 : 2), o.carry, o.ok !== false)
    if (o.idle && m) {
      const k = (((now / 1600 + ph) % 1) + 1) % 1
      ctx.globalAlpha *= 1 - k
      ctx.fillStyle = pal.faint
      ctx.font = `600 ${8 + k * 4}px ${pal.font}`
      ctx.fillText('z', p.x + 7 + k * 6, hy - 6 - k * 14)
    }
    ctx.restore()
  }

  /** What a courier carries, held in front of it. */
  private item(cx: number, cy: number, kind: Move['item'], ok: boolean) {
    const { ctx, pal } = this
    const paper = mix(pal.panel, '#ffffff', 0.7)
    const ink = withAlpha(pal.text, 0.55)
    ctx.lineWidth = 1
    ctx.strokeStyle = ink
    switch (kind) {
      case 'parcel': {
        ctx.fillStyle = ok ? pal.wood : mix(pal.text, pal.panel, 0.45)
        ctx.fillRect(cx - 7, cy - 4, 14, 10)
        ctx.strokeRect(cx - 7, cy - 4, 14, 10)
        ctx.beginPath()
        ctx.moveTo(cx, cy - 4)
        ctx.lineTo(cx, cy + 6)
        ctx.stroke()
        break
      }
      case 'stamp': {
        ctx.fillStyle = ok ? pal.accent : pal.text
        ctx.beginPath()
        ctx.arc(cx, cy, 6, 0, Math.PI * 2)
        ctx.fill()
        ctx.strokeStyle = paper
        ctx.lineWidth = 1.5
        ctx.beginPath()
        if (ok) {
          ctx.moveTo(cx - 3, cy)
          ctx.lineTo(cx - 0.5, cy + 2.5)
          ctx.lineTo(cx + 3.5, cy - 2.5)
        } else {
          ctx.moveTo(cx - 2.5, cy - 2.5)
          ctx.lineTo(cx + 2.5, cy + 2.5)
          ctx.moveTo(cx + 2.5, cy - 2.5)
          ctx.lineTo(cx - 2.5, cy + 2.5)
        }
        ctx.stroke()
        break
      }
      case 'letter':
      case 'note': {
        ctx.fillStyle = paper
        ctx.fillRect(cx - 6, cy - 4, 12, 8)
        ctx.strokeRect(cx - 6, cy - 4, 12, 8)
        ctx.beginPath()
        ctx.moveTo(cx - 6, cy - 4)
        ctx.lineTo(cx, cy + 0.5)
        ctx.lineTo(cx + 6, cy - 4)
        ctx.stroke()
        break
      }
      case 'request': {
        ctx.fillStyle = paper
        ctx.fillRect(cx - 5, cy - 6, 10, 12)
        ctx.strokeRect(cx - 5, cy - 6, 10, 12)
        ctx.fillStyle = pal.text
        ctx.font = `700 9px ${pal.font}`
        ctx.textAlign = 'center'
        ctx.fillText('?', cx, cy + 3)
        break
      }
      default: {
        // A ticket or a memo: a card with lines.
        ctx.fillStyle = kind === 'ticket' ? paper : mix(paper, pal.wood, 0.4)
        ctx.fillRect(cx - 6, cy - 5, 12, 10)
        ctx.strokeRect(cx - 6, cy - 5, 12, 10)
        ctx.fillStyle = ink
        ctx.fillRect(cx - 4, cy - 2.5, 8, 1)
        ctx.fillRect(cx - 4, cy, 6, 1)
        ctx.fillRect(cx - 4, cy + 2.5, 7, 1)
      }
    }
  }

  trail(path: Pt[], t: number) {
    const { ctx, pal } = this
    ctx.save()
    ctx.setLineDash([2, 6])
    ctx.lineCap = 'round'
    ctx.lineWidth = 2.5
    ctx.strokeStyle = withAlpha(pal.accent, 0.35)
    ctx.beginPath()
    for (let i = 0; i <= 40; i++) {
      const q = along(path, t + ((1 - t) * i) / 40)
      const s = iso(q.x, q.y)
      if (i) ctx.lineTo(s.x, s.y)
      else ctx.moveTo(s.x, s.y)
    }
    ctx.stroke()
    ctx.restore()
  }

  ring(g: Pt, k: number) {
    const { ctx, pal } = this
    const p = iso(g.x, g.y)
    ctx.save()
    ctx.strokeStyle = withAlpha(pal.accent, 1 - k)
    ctx.lineWidth = 2
    ctx.beginPath()
    ctx.ellipse(p.x, p.y, 10 + k * 22, (10 + k * 22) * (getProjection() === 'iso' ? 0.5 : 0.62), 0, 0, Math.PI * 2)
    ctx.stroke()
    ctx.restore()
  }
}

type Mark = { mark: (typeof AGENT_MARKS)[string] | undefined; path: Path2D | undefined }

const SHORT: Record<string, string> = { todo: '待办', doing: '进行', approval: '审批', review: '验收', blocked: '受阻', done: '完成' }

/** Screen-space words: plates, tags, labels and bubbles at a fixed size. Records click targets. */
class Words {
  constructor(
    private ctx: CanvasRenderingContext2D,
    private pal: Palette,
    private cam: Camera,
    private hits: Hit[],
  ) {}

  private pill(x: number, y: number, w: number, h: number, fill: string, stroke: string, r = h / 2) {
    const ctx = this.ctx
    ctx.beginPath()
    ctx.roundRect(x, y, w, h, r)
    ctx.fillStyle = fill
    ctx.fill()
    ctx.strokeStyle = stroke
    ctx.lineWidth = 1
    ctx.stroke()
  }

  private shadow(on: boolean) {
    const ctx = this.ctx
    ctx.shadowColor = on ? withAlpha('#000000', this.pal.dark ? 0.4 : 0.12) : 'transparent'
    ctx.shadowBlur = on ? 8 : 0
    ctx.shadowOffsetY = on ? 2 : 0
  }

  /** A workshop's sign: state mark, machine name, how many seats are busy. */
  plate(r: Room, name: string, summary: string, state: 'busy' | 'ok' | 'down', hover: boolean, target: Target) {
    const { ctx, pal } = this
    // 2.5D: lying on the floor at the front of the area. 2D: a slim one-line
    // sign along the area's front edge, below the desks.
    const flat = getProjection() === 'flat'
    const spot = flat ? floorToScreen(this.cam, r.x0 + r.w / 2, r.y0 + r.d - 0.1) : floorToScreen(this.cam, r.x0 + r.w * 0.42, r.y0 + r.d - 0.45)
    const x = spot.x
    const small0 = flat || this.cam.scale < 0.45
    const y = flat ? spot.y - 2 : spot.y + (small0 ? 12 : 19)
    if (flat) name = `${name} · ${summary.replace(/ 个工位在干活.*$/, '')}`
    const small = small0
    ctx.save()
    ctx.textBaseline = 'middle'
    ctx.font = `600 12.5px ${pal.font}`
    const nameW = ctx.measureText(name).width
    ctx.font = `11px ${pal.font}`
    const sumW = small ? 0 : ctx.measureText(summary).width
    const w = Math.max(nameW + 22, sumW) + 20
    const h = small ? 24 : 38
    ctx.globalAlpha = state === 'down' ? 0.7 : 1
    this.shadow(true)
    this.pill(x - w / 2, y - h, w, h, pal.raised, hover ? pal.accent : pal.lineStrong, 10)
    this.shadow(false)
    const ny = y - h + 12
    const nx = x - (nameW + 14) / 2
    mark(ctx, pal, nx + 4, ny, state)
    ctx.fillStyle = pal.text
    ctx.font = `600 12.5px ${pal.font}`
    ctx.textAlign = 'left'
    ctx.fillText(name, nx + 14, ny + 0.5)
    if (!small) {
      ctx.fillStyle = pal.muted
      ctx.font = `11px ${pal.font}`
      ctx.textAlign = 'center'
      ctx.fillText(summary, x, ny + 15)
    }
    ctx.restore()
    this.hits.push({ x: x - w / 2, y: y - h, w, h, target })
  }

  /** The board's column heads (name and count), and a click target per column. */
  columns(l: Layout, f: Floor) {
    const { ctx, pal } = this
    const { x0, x1, y } = l.board
    const w = (x1 - x0) / BOARD_COLUMNS
    ctx.save()
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    COLUMNS.forEach((c, i) => {
      const a = floorToScreen(this.cam, x0 + i * w, y, BOARD_TOP)
      const b = floorToScreen(this.cam, x0 + (i + 1) * w, y, 6)
      const head = floorToScreen(this.cam, x0 + (i + 0.5) * w, y, BOARD_TOP + 9)
      const n = f.board[c.id].length
      ctx.font = `600 ${this.cam.scale < 0.5 ? 10 : 11}px ${pal.font}`
      ctx.fillStyle = c.id === 'doing' && n ? pal.accent : pal.text
      // As much of the name as the column has room for: full, two characters, or just the count.
      const room = Math.abs(b.x - a.x) + (getProjection() === 'iso' ? 40 : 0)
      const full = `${c.name} ${n}`
      const text = ctx.measureText(full).width < room ? full : room > 34 ? `${SHORT[c.id]}${n}` : String(n)
      ctx.fillText(text, head.x, head.y)
      this.hits.push({ x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), w: Math.abs(b.x - a.x), h: Math.abs(b.y - a.y), target: { kind: 'column', col: i } })
    })
    ctx.restore()
  }

  /** A person's name tag over the head, with a short note under the name. */
  tag(at: Pt, name: string, note: string, lit: boolean, target: Target, hover: boolean) {
    const { ctx, pal } = this
    const p = floorToScreen(this.cam, at.x, at.y, 38)
    ctx.save()
    ctx.font = `600 11.5px ${pal.font}`
    const text = note ? `${name} · ${note}` : name
    const w = ctx.measureText(text).width + 14
    const h = 20
    const x = p.x - w / 2
    const y = p.y - h - 6
    this.shadow(true)
    this.pill(x, y, w, h, lit ? pal.accent : pal.raised, hover ? pal.accent : lit ? pal.accent : pal.lineStrong)
    this.shadow(false)
    ctx.fillStyle = lit ? pal.accentText : pal.text
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(text, p.x, y + h / 2 + 0.5)
    ctx.restore()
    const foot = floorToScreen(this.cam, at.x, at.y)
    this.hits.push({ x: Math.min(x, p.x - 12 * this.cam.scale), y, w: Math.max(w, 24 * this.cam.scale), h: foot.y - y, target })
  }

  /** An agent's badge and name over its head, and what it is on (when there is room). */
  agent(at: Pt, run: Run, m: Mark, line: string, focus: boolean, hover: boolean, queued = false, tiny = false) {
    const { ctx, pal } = this
    const p = floorToScreen(this.cam, at.x, at.y, 38)
    const name = agentTitle(run.agent)
    ctx.save()
    ctx.globalAlpha = focus ? 1 : 0.35
    ctx.font = `600 11px ${pal.font}`
    // Tiny: just the logo.
    const label = tiny ? '' : queued ? `${name} 排队` : name
    const w = tiny ? 22 : ctx.measureText(label).width + 30
    const h = 20
    const x = p.x - w / 2
    const y = p.y - h - 6
    this.shadow(true)
    this.pill(x, y, w, h, pal.raised, hover ? pal.accent : pal.lineStrong)
    this.shadow(false)
    // The agent's logo in a small rounded square.
    const s = 14
    const bx = x + 4
    const by = y + 3
    ctx.beginPath()
    ctx.roundRect(bx, by, s, s, 4)
    ctx.fillStyle = m.mark?.bg ?? mix(pal.text, pal.panel, 0.4)
    ctx.fill()
    ctx.strokeStyle = withAlpha(pal.text, 0.12)
    ctx.stroke()
    if (m.mark && m.path) {
      ctx.save()
      ctx.translate(bx + 2, by + 2)
      ctx.scale(10 / 24, 10 / 24)
      ctx.fillStyle = m.mark.fg
      ctx.fill(m.path, m.mark.evenodd ? 'evenodd' : 'nonzero')
      ctx.restore()
    } else {
      ctx.fillStyle = '#ffffff'
      ctx.font = `700 9px ${pal.font}`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText((run.agent || '?').slice(0, 1).toUpperCase(), bx + s / 2, by + s / 2 + 0.5)
    }
    ctx.fillStyle = pal.text
    ctx.font = `600 11px ${pal.font}`
    ctx.textAlign = 'left'
    ctx.textBaseline = 'middle'
    ctx.fillText(label, bx + s + 5, y + h / 2 + 0.5)
    let top = y
    if (line) {
      ctx.font = `11px ${pal.font}`
      const lw = Math.min(260, ctx.measureText(line).width + 14)
      const ly = y - 22
      this.pill(p.x - lw / 2, ly, lw, 19, withAlpha(pal.raised, 0.92), pal.line, 6)
      ctx.fillStyle = pal.muted
      ctx.textAlign = 'center'
      ctx.fillText(line, p.x, ly + 10)
      top = ly
    }
    ctx.restore()
    const foot = floorToScreen(this.cam, at.x, at.y)
    this.hits.push({ x: Math.min(x, p.x - 12), y: top, w: Math.max(w, 24), h: foot.y - top, target: { kind: 'run', id: run.id } })
  }

  /** A tag over a courier's head: what it carries. */
  label(g: Pt, text: string, fade: number, move: Move) {
    const { ctx, pal } = this
    const p = floorToScreen(this.cam, g.x, g.y, 40 + 8 / Math.max(0.3, this.cam.scale))
    const ok = move.ok !== false
    ctx.save()
    ctx.globalAlpha = Math.max(0, fade)
    ctx.font = `600 11.5px ${pal.font}`
    const w = ctx.measureText(text).width + 14
    const h = 20
    const x = p.x - w / 2
    const y = p.y - h - 4
    this.pill(x, y, w, h, ok ? pal.accent : pal.text, ok ? pal.accent : pal.text)
    ctx.fillStyle = ok ? pal.accentText : pal.panel
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(text, p.x, y + h / 2 + 0.5)
    ctx.restore()
    this.hits.push({ x, y, w, h, target: { kind: 'move', move } })
  }

  /** A speech bubble over someone; idx stacks older ones upward. */
  bubble(g: Pt, move: Move, k: number, idx: number) {
    const { ctx, pal } = this
    const p = floorToScreen(this.cam, g.x, g.y, 66)
    const pop = Math.min(1, k / 0.06)
    const fade = Math.min(1, (1 - k) / 0.15)
    ctx.save()
    ctx.globalAlpha = Math.max(0, fade) * (idx ? 0.75 : 1)
    ctx.font = `11.5px ${pal.font}`
    const w = ctx.measureText(move.text).width + 16
    const h = 22
    const x = p.x - w / 2
    const y = p.y - h - 8 - idx * (h + 4) - (1 - pop) * 6
    const edge = k < 0.25 ? pal.accent : pal.lineStrong
    this.pill(x, y, w, h, pal.raised, edge, 8)
    if (idx === 0) {
      ctx.fillStyle = pal.raised
      ctx.strokeStyle = edge
      ctx.beginPath()
      ctx.moveTo(p.x - 5, y + h - 0.5)
      ctx.lineTo(p.x, y + h + 6)
      ctx.lineTo(p.x + 5, y + h - 0.5)
      ctx.fill()
      ctx.stroke()
    }
    ctx.fillStyle = pal.text
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillText(move.text, p.x, y + h / 2 + 0.5)
    ctx.restore()
    this.hits.push({ x, y, w, h, target: { kind: 'move', move } })
  }

  /** Three dots over the supervisor while it is answering. */
  thinking(g: Pt, now: number) {
    const { ctx, pal } = this
    const p = floorToScreen(this.cam, g.x, g.y, 66)
    ctx.save()
    this.pill(p.x - 18, p.y - 30, 36, 20, pal.raised, pal.lineStrong, 10)
    for (let i = 0; i < 3; i++) {
      const k = (Math.sin(now / 200 - i * 0.8) + 1) / 2
      ctx.fillStyle = withAlpha(pal.accent, 0.35 + k * 0.65)
      ctx.beginPath()
      ctx.arc(p.x - 8 + i * 8, p.y - 20, 2.5, 0, Math.PI * 2)
      ctx.fill()
    }
    ctx.restore()
  }
}

/** The state mark the rest of the page uses (StateDot), drawn. */
function mark(ctx: CanvasRenderingContext2D, pal: Palette, x: number, y: number, state: 'busy' | 'ok' | 'down') {
  ctx.save()
  ctx.lineWidth = 1.5
  if (state === 'down') {
    ctx.strokeStyle = pal.faint
    ctx.beginPath()
    ctx.moveTo(x - 3.5, y - 3.5)
    ctx.lineTo(x + 3.5, y + 3.5)
    ctx.moveTo(x + 3.5, y - 3.5)
    ctx.lineTo(x - 3.5, y + 3.5)
    ctx.stroke()
  } else {
    ctx.fillStyle = state === 'busy' ? pal.accent : pal.text
    ctx.beginPath()
    ctx.arc(x, y, 4, 0, Math.PI * 2)
    ctx.fill()
  }
  ctx.restore()
}
