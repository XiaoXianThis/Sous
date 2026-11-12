import { BrainSocket } from '../trust/rpc'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { chat, go, onHubEvent, ui, useChat, useWb, wb } from '../lib/app'
import { COLUMNS, FEED_MAX, floorOf, focusOf, inFocus, moveOf, seatingOf, type Move, type Seating } from '../lib/dispatch'
import { DispatchScene, readPalette, type Target } from '../lib/dispatch-draw'
import { fit, layout, setProjection, startView, zoomAt, type Camera, type Projection } from '../lib/office'
import type { HubEvent } from '../lib/types'
import { ColumnDialog, FeedDialog, MachineDialog, MoveDialog, StaffDialog } from './DispatchDialogs'
import Icon from './Icon'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from './ui/dropdown-menu'

/** Stored events read back for the feed when the page opens. */
const BACKFILL = 600

/** Names a focus key ("task:<id>", "thread:<id>", "run:<id>"). */
function useFocusName() {
  const chatState = useChat()
  const wbState = useWb()
  return (key: string): string => {
    if (!key) return '全部'
    const [kind, id] = key.split(':')
    if (kind === 'task') return wbState.data?.tasks.find((t) => t.id === id)?.title ?? `任务 ${id.slice(-6)}`
    if (kind === 'thread') {
      const t = chatState.threads.find((x) => x.id === id)
      return t?.kind === 'main' ? '主线' : (t?.title ?? `对话 ${id.slice(-6)}`)
    }
    return `Run ${id.slice(-6)}`
  }
}

/** Reads the last stored events again (the /ws replay) and turns them into moves for the feed. */
function backfill(upTo: number, toMove: (ev: HubEvent) => Move | null): Promise<Move[]> {
  return new Promise((resolve) => {
    if (upTo <= 0) return resolve([])
    const out: Move[] = []
    let ws: BrainSocket
    try {
      ws = new BrainSocket(`/ws?since=${Math.max(0, upTo - BACKFILL)}`)
    } catch {
      return resolve([])
    }
    const done = () => {
      clearTimeout(timer)
      ws.close()
      resolve(out)
    }
    const timer = setTimeout(done, 3000)
    ws.onmessage = (e) => {
      try {
        const ev = JSON.parse(e.data) as HubEvent
        if (ev.seq > upTo) return done()
        const m = toMove(ev)
        if (m) out.push(m)
        if (ev.seq >= upTo) done()
      } catch {
        /* skipped */
      }
    }
    ws.onerror = done
  })
}

type Open = Target | { kind: 'feed' } | null

const PROJ_KEY = 'sous.sceneView'
/** Height the toolbar takes at the top of the panel. */
const TOOLBAR = 46

/** The remembered view; a phone starts in 2D, which fills its tall screen. */
function storedProjection(): Projection {
  try {
    const v = localStorage.getItem(PROJ_KEY)
    if (v === 'flat' || v === 'iso') return v
  } catch {
    /* fall through */
  }
  return window.innerWidth <= 760 ? 'flat' : 'iso'
}

/**
 * The workbench's "现场" view. Who hands what to whom, live: your office, the dispatch room (the
 * supervisor, its helpers and the task board) and a workshop per machine with
 * an agent at each busy seat. Every hand-off is a courier carrying a note, a
 * ticket, a request, a stamp or a parcel (work/specs/system-map).
 */
export default function Dispatch({ trace, lead }: {
  trace: string
  /** Drawn at the left of the toolbar (the phone's summary, merged into the scene's top bar). */
  lead?: ReactNode
}) {
  const wbState = useWb()
  const chatState = useChat()
  const data = wbState.data
  const name = useFocusName()
  const [focus, setFocus] = useState(trace)
  const [paused, setPaused] = useState(0) // when paused (ms), 0 when live
  const [feed, setFeed] = useState<Move[]>([])
  const [feedSeen, setFeedSeen] = useState(() => Date.now()) // the feed's dialog was last looked at
  const [open, setOpen] = useState<Open>(null)
  const [pointer, setPointer] = useState(false)
  const [proj, setProj] = useState<Projection>(storedProjection)
  // The panel's shape (width / height), rounded so a resize only re-plans the floor when it matters.
  const [aspect, setAspect] = useState(0)
  useEffect(() => setFocus(trace), [trace])
  setProjection(proj)

  const seating = useRef<Seating>({})
  const floor = useMemo(() => {
    if (!data) return null
    const f = floorOf(data, Date.now(), seating.current)
    seating.current = seatingOf(f)
    return f
  }, [data])
  const shape = floor ? floor.shops.map((s) => `${s.node.id}:${s.seats.length}`).join(',') : ''
  // 2D plans the floor for the panel's shape; 2.5D keeps its fixed plan.
  const plan = useMemo(
    () => layout(floor ? floor.shops.map((s) => ({ id: s.node.id, seats: s.seats.length })) : [], proj === 'flat' && aspect ? aspect : undefined),
    [shape, proj, aspect], // eslint-disable-line react-hooks/exhaustive-deps
  )
  const focusSet = useMemo(() => focusOf(focus, data), [focus, data])
  const supBusy = Object.keys(chatState.answering).length > 0

  const scene = useRef(new DispatchScene()).current
  const box = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const userCam = useRef(false)
  const live = useRef({ focusSet, paused })
  live.current = { focusSet, paused }
  /** A camera for the panel below the toolbar (which floats over its top). */
  const below = (make: (w: number, h: number) => Camera): Camera => {
    const c = make(scene.width, scene.height - TOOLBAR)
    return { ...c, oy: c.oy + TOOLBAR }
  }
  const refit = () => {
    const l = scene.input?.layout
    if (l) scene.cam = below((w, h) => startView(l, w, h))
  }

  // What the scene draws; read every frame, so changing it never restarts the loop.
  useEffect(() => {
    if (!data || !floor) return
    const first = !scene.input
    const moved = scene.input?.layout !== plan
    scene.input = {
      layout: plan,
      floor,
      data,
      live: wb.state.live,
      supBusy,
      focused: !!focusSet,
      isFocused: (refs) => inFocus(focusSet, refs),
    }
    if ((first || moved) && !userCam.current && scene.width) refit()
  })

  // Live events become couriers; the feed keeps them in words.
  useEffect(
    () =>
      onHubEvent((ev) => {
        const m = moveOf(ev, wb.state.data)
        if (!m) return
        setFeed((f) => [...f, m].slice(-FEED_MAX))
        const { focusSet: fs, paused: p } = live.current
        if (!p && inFocus(fs, m)) scene.add([m], performance.now())
      }),
    [scene],
  )

  // The feed starts with what happened just before the page opened.
  const ready = !!data
  const seqKnown = chatState.lastSeq > 0
  useEffect(() => {
    if (!ready || !seqKnown) return
    let stop = false
    void backfill(chat.state.lastSeq, (ev) => moveOf(ev, wb.state.data)).then((old) => {
      if (stop || !old.length) return
      setFeed((f) => {
        const seen = new Set(f.map((m) => m.id))
        return [...old.filter((m) => !seen.has(m.id)), ...f].slice(-FEED_MAX)
      })
    })
    return () => {
      stop = true
    }
  }, [ready, seqKnown])

  // Size, theme, motion preference and the drawing loop: set up once.
  useEffect(() => {
    const el = box.current
    const cv = canvas.current
    if (!ready || !el || !cv) return
    const ctx = cv.getContext('2d')
    if (!ctx) return
    let dpr = window.devicePixelRatio || 1
    const resize = () => {
      dpr = window.devicePixelRatio || 1
      const r = el.getBoundingClientRect()
      const w = Math.round(r.width * dpr)
      const h = Math.round(r.height * dpr)
      scene.width = r.width
      scene.height = r.height
      if (!userCam.current) refit()
      // Setting the size clears the canvas: only when it really changed, and
      // then draw again at once, before the browser paints, so a resize
      // never shows an empty frame.
      if (cv.width !== w || cv.height !== h) {
        cv.width = w
        cv.height = h
        scene.draw(ctx, dpr, performance.now())
      }
      // The shape the floor gets: the panel less its padding and the toolbar.
      const fw = r.width - 32
      const fh = r.height - TOOLBAR - 32
      if (fw > 0 && fh > 0) setAspect(Math.round(Math.exp(Math.round(Math.log(fw / fh) * 6) / 6) * 100) / 100)
    }
    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(el)
    scene.pal = readPalette()
    const mo = new MutationObserver(() => (scene.pal = readPalette()))
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style', 'class'] })
    const rm = window.matchMedia('(prefers-reduced-motion: reduce)')
    const motion = () => (scene.reduced = rm.matches)
    motion()
    rm.addEventListener('change', motion)
    let raf = 0
    const frame = () => {
      scene.draw(ctx, dpr, performance.now())
      raf = requestAnimationFrame(frame)
    }
    raf = requestAnimationFrame(frame)
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      mo.disconnect()
      rm.removeEventListener('change', motion)
    }
  }, [ready, scene]) // eslint-disable-line react-hooks/exhaustive-deps

  // Drag to move, wheel or pinch to zoom, click someone or something.
  useEffect(() => {
    const cv = canvas.current
    if (!ready || !cv) return
    const pointers = new Map<number, { x: number; y: number }>()
    let start: { x: number; y: number; cam: Camera } | null = null
    let moved = false
    let pinch = 0
    const local = (e: PointerEvent | WheelEvent) => {
      const r = cv.getBoundingClientRect()
      return { x: e.clientX - r.left, y: e.clientY - r.top }
    }
    const down = (e: PointerEvent) => {
      cv.setPointerCapture(e.pointerId)
      pointers.set(e.pointerId, local(e))
      moved = false
      start = { ...local(e), cam: scene.cam }
      if (pointers.size === 2) {
        const [a, b] = [...pointers.values()]
        pinch = Math.hypot(a.x - b.x, a.y - b.y)
      }
    }
    const move = (e: PointerEvent) => {
      const p = local(e)
      if (!pointers.has(e.pointerId)) {
        const h = scene.hit(p.x, p.y)
        scene.hover = h
        setPointer(!!h)
        return
      }
      pointers.set(e.pointerId, p)
      if (pointers.size === 2) {
        const [a, b] = [...pointers.values()]
        const dist = Math.hypot(a.x - b.x, a.y - b.y)
        if (pinch > 0) scene.cam = zoomAt(scene.cam, dist / pinch, (a.x + b.x) / 2, (a.y + b.y) / 2)
        pinch = dist
        moved = true
        userCam.current = true
        return
      }
      if (!start) return
      const dx = p.x - start.x
      const dy = p.y - start.y
      if (!moved && Math.hypot(dx, dy) < 4) return
      moved = true
      userCam.current = true
      scene.cam = { ...start.cam, ox: start.cam.ox + dx, oy: start.cam.oy + dy }
    }
    const up = (e: PointerEvent) => {
      const p = local(e)
      pointers.delete(e.pointerId)
      if (pointers.size > 0) return
      pinch = 0
      start = null
      if (moved) return
      const h = scene.hit(p.x, p.y)
      if (h?.kind === 'run') {
        ui.state.runSheet = h.id
        ui.emit()
      } else if (h) setOpen(h)
    }
    const leave = () => {
      scene.hover = null
      setPointer(false)
    }
    const wheel = (e: WheelEvent) => {
      e.preventDefault()
      const p = local(e)
      if (e.ctrlKey || e.metaKey || Math.abs(e.deltaY) > Math.abs(e.deltaX)) {
        scene.cam = zoomAt(scene.cam, Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0015)), p.x, p.y)
      } else {
        scene.cam = { ...scene.cam, ox: scene.cam.ox - e.deltaX, oy: scene.cam.oy - e.deltaY }
      }
      userCam.current = true
    }
    cv.addEventListener('pointerdown', down)
    cv.addEventListener('pointermove', move)
    cv.addEventListener('pointerup', up)
    cv.addEventListener('pointercancel', up)
    cv.addEventListener('pointerleave', leave)
    cv.addEventListener('wheel', wheel, { passive: false })
    return () => {
      cv.removeEventListener('pointerdown', down)
      cv.removeEventListener('pointermove', move)
      cv.removeEventListener('pointerup', up)
      cv.removeEventListener('pointercancel', up)
      cv.removeEventListener('pointerleave', leave)
      cv.removeEventListener('wheel', wheel)
    }
  }, [ready, scene])

  if (!data || !floor) {
    return <div className="h-full grid place-items-center text-[14px] text-muted">{wbState.error ? `读不到工作台：${wbState.error}` : '正在读取…'}</div>
  }

  const shown = feed.filter((m) => inFocus(focusSet, m))
  const waiting = paused ? feed.filter((m) => m.ts >= paused).length : 0
  const choices = COLUMNS.filter((c) => c.id !== 'done').flatMap((c) => floor.board[c.id])
  const recenter = () => {
    userCam.current = false
    const l = scene.input?.layout
    if (l) scene.cam = below((w, h) => (proj === 'flat' ? startView(l, w, h) : fit(l, w, h, 16)))
  }
  const showMove = (m: Move) => setOpen({ kind: 'move', move: m })

  const unread = feed.filter((m) => m.ts > feedSeen && inFocus(focusSet, m)).length

  return (
    <div ref={box} className="relative h-full overflow-hidden">
      <canvas
        ref={canvas}
        className={`absolute inset-0 w-full h-full touch-none select-none ${pointer ? 'cursor-pointer' : 'cursor-grab active:cursor-grabbing'}`}
        role="img"
        aria-label="现场：你的办公室、主管的调度室和每台机器的工位；送东西的小人是一次交接"
      />

      {/* The same places as buttons, for keyboards and screen readers. */}
      <div className="sr-only">
        <button onClick={() => setOpen({ kind: 'staff', actor: 'you' })}>你的桌面</button>
        <button onClick={() => setOpen({ kind: 'staff', actor: 'sup' })}>主管</button>
        {COLUMNS.map((c, i) => (
          <button key={c.id} onClick={() => setOpen({ kind: 'column', col: i })}>
            看板：{c.name}
          </button>
        ))}
        {floor.shops.map((s) => (
          <button key={s.node.id} onClick={() => setOpen({ kind: 'machine', node: s.node.id })}>
            机器 {s.node.name}
          </button>
        ))}
      </div>

      {/* Controls over the scene, so it keeps the whole panel. */}
      <div
        className={`absolute top-2.5 right-2.5 flex items-center p-1 rounded-[12px] bg-raised/90 shadow-soft backdrop-blur ${lead ? 'left-2.5 gap-0' : 'gap-1'}`}
      >
        {lead && <div className="flex items-center min-w-0 flex-1 overflow-hidden">{lead}</div>}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className={`btn ghost small max-w-[220px] ${lead ? '!px-2' : 'max-[760px]:max-w-[120px]'}`} aria-label={`只看一件事：${name(focus)}`} data-tip={name(focus)} data-tip-pos="bottom">
              {lead ? (
                <span className="relative grid">
                  <Icon name="filter" size={15} />
                  {focus && <span className="absolute -top-1 -right-1 w-[7px] h-[7px] rounded-full bg-accent" />}
                </span>
              ) : (
                <>
                  <span className="truncate">{name(focus)}</span>
                  <Icon name="chevron" size={12} className="shrink-0" />
                </>
              )}
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-h-[60dvh] overflow-y-auto">
            <DropdownMenuItem onSelect={() => setFocus('')}>全部</DropdownMenuItem>
            {choices.map((t) => (
              <DropdownMenuItem key={t.id} onSelect={() => setFocus(`task:${t.id}`)}>
                <span className="truncate max-w-[280px]">{t.title}</span>
              </DropdownMenuItem>
            ))}
            {focus && !choices.some((t) => `task:${t.id}` === focus) && <DropdownMenuItem disabled>{name(focus)}</DropdownMenuItem>}
          </DropdownMenuContent>
        </DropdownMenu>
        <button
          className={`btn ghost small ${lead ? '!px-2' : ''}`}
          aria-label={`动态${unread ? `，${unread} 条新的` : ''}`}
          data-tip="谁把什么交给了谁"
          data-tip-pos="bottom"
          onClick={() => setOpen({ kind: 'feed' })}
        >
          <Icon name="pulse" size={15} />
          <span className="max-[760px]:hidden">动态</span>
          {unread > 0 && <span className="min-w-[18px] h-[18px] px-1 rounded-full bg-accent text-accent-text text-[11px] font-semibold tabular-nums grid place-items-center">{unread > 99 ? '99+' : unread}</span>}
        </button>
        <button
          className={`btn ghost small tabular-nums ${lead ? '!px-2' : ''}`}
          aria-label={proj === 'flat' ? '换成 2.5D' : '换成 2D'}
          data-tip={proj === 'flat' ? '换成 2.5D：斜着看' : '换成 2D：正面俯视，铺满画面'}
          data-tip-pos="bottom"
          onClick={() => {
            const next: Projection = proj === 'flat' ? 'iso' : 'flat'
            try {
              localStorage.setItem(PROJ_KEY, next)
            } catch {
              /* not remembered */
            }
            userCam.current = false
            setProj(next)
          }}
        >
          {proj === 'flat' ? '2D' : '2.5D'}
        </button>
        <button className="icon-btn" aria-label="看全图" data-tip="看全图" data-tip-pos="bottom" onClick={recenter}>
          <Icon name="expand" size={16} />
        </button>
        {/* Phones leave out pause: the bar there also holds the summary. */}
        <button className={`icon-btn ${lead ? 'hidden' : ''}`} aria-label={paused ? '继续' : '暂停'} data-tip={paused ? '回到实时' : '暂停动画'} data-tip-pos="bottom" onClick={() => setPaused(paused ? 0 : Date.now())}>
          <Icon name={paused ? 'play' : 'pause'} size={16} />
        </button>
      </div>

      {floor.shops.length === 0 && <div className="absolute top-3 left-3 pill pointer-events-none">还没有配对机器，agent 没有地方干活</div>}

      {paused > 0 && (
        <button className="absolute bottom-3 left-1/2 -translate-x-1/2 pill shadow-soft" onClick={() => setPaused(0)}>
          已暂停动画{waiting ? ` · 期间 ${waiting} 次交接（见动态）` : ''}，回到实时
        </button>
      )}

      {open?.kind === 'feed' && (
        <FeedDialog
          list={shown}
          data={data}
          sub={focus ? name(focus) : '谁把什么交给了谁'}
          onClose={() => {
            setFeedSeen(Date.now())
            setOpen(null)
          }}
          onMove={(m) => {
            setFeedSeen(Date.now())
            showMove(m)
          }}
        />
      )}
      {open?.kind === 'staff' && <StaffDialog actor={open.actor} floor={floor} data={data} feed={feed} onClose={() => setOpen(null)} onMove={showMove} />}
      {open?.kind === 'column' && <ColumnDialog col={open.col} floor={floor} onClose={() => setOpen(null)} />}
      {open?.kind === 'machine' && <MachineDialog node={open.node} floor={floor} data={data} feed={feed} onClose={() => setOpen(null)} onMove={showMove} />}
      {open?.kind === 'move' && (
        <MoveDialog
          move={open.move}
          data={data}
          onClose={() => setOpen(null)}
          onFocus={(key) => {
            setOpen(null)
            setFocus(key)
            if (trace) go({ view: 'system', id: '' })
          }}
        />
      )}
    </div>
  )
}
