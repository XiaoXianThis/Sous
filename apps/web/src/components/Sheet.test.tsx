// Sheets are Radix dialogs: drawn in <body>, Escape closes only the topmost one,
// and focus goes back to what opened it. Inside a settings page (SheetPageContext)
// the page's own sheet is laid flat into it instead.
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import Sheet, { SheetPageContext } from './Sheet'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let cleanup: (() => Promise<void>) | null = null
async function mount(node: React.ReactNode) {
  const el = document.createElement('div')
  document.body.append(el)
  const root = createRoot(el)
  await act(async () => root.render(node))
  cleanup = () => act(async () => { root.unmount(); el.remove() })
  return el
}
afterEach(async () => {
  await cleanup?.()
  cleanup = null
})

/** Escape the way a keyboard (or the phone's edge swipe, lib/ios.ts) sends it. */
const escape = () => act(async () => { document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })) })
/** Radix gives focus back on a timer after a dialog unmounts. */
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 0)) })
const dialog = (label: string) => document.querySelector<HTMLElement>(`[role=dialog][aria-label="${label}"]`)

describe('Sheet placement', () => {
  it('opens as a modal dialog drawn in <body>, after its backdrop', async () => {
    const el = await mount(<Sheet title="外面" onClose={() => {}}>x</Sheet>)
    const d = dialog('外面')!
    expect(d).not.toBeNull()
    expect(d.getAttribute('aria-modal')).toBe('true')
    expect(el.contains(d)).toBe(false)
    expect(d.parentElement).toBe(document.body)
    // The phone's drag-to-close finds the backdrop right before the sheet.
    expect(d.previousElementSibling?.classList.contains('backdrop')).toBe(true)
  })

  it('flattens a settings page root but draws dialogs opened from it in <body>', async () => {
    const el = await mount(
      <SheetPageContext.Provider value>
        <Sheet title="页面" onClose={() => {}}>
          <Sheet title="二级" onClose={() => {}}>y</Sheet>
        </Sheet>
      </SheetPageContext.Provider>,
    )
    expect(el.querySelector('[aria-label=页面]')).not.toBeNull()
    expect(el.querySelector('[aria-label=二级]')).toBeNull()
    expect(dialog('二级')?.parentElement).toBe(document.body)
  })

  it('closes from the backdrop and the close button', async () => {
    const onClose = vi.fn()
    await mount(<Sheet title="外面" onClose={onClose}>x</Sheet>)
    await act(async () => (dialog('外面')!.previousElementSibling as HTMLElement).click())
    expect(onClose).toHaveBeenCalledTimes(1)
    await act(async () => dialog('外面')!.querySelector<HTMLButtonElement>('[aria-label=关闭]')!.click())
    expect(onClose).toHaveBeenCalledTimes(2)
  })
})

/** Two stacked sheets, each opened by a button, the way the app opens them. */
function Stack({ log }: { log: string[] }) {
  const [outer, setOuter] = useState(false)
  const [inner, setInner] = useState(false)
  return (
    <>
      <button id="open-outer" onClick={() => setOuter(true)}>打开</button>
      {outer && (
        <Sheet title="外层" onClose={() => { log.push('外层'); setOuter(false) }}>
          <button id="open-inner" onClick={() => setInner(true)}>再打开</button>
          {inner && <Sheet title="内层" onClose={() => { log.push('内层'); setInner(false) }}>z</Sheet>}
        </Sheet>
      )}
    </>
  )
}

/** Clicks a button the way a user does: it takes focus first. */
async function press(id: string) {
  const b = document.getElementById(id) as HTMLButtonElement
  await act(async () => { b.focus(); b.click() })
  await settle()
}

describe('Sheet stack', () => {
  it('Escape closes only the topmost sheet, then the next', async () => {
    const log: string[] = []
    await mount(<Stack log={log} />)
    await press('open-outer')
    await press('open-inner')
    expect(dialog('内层')).not.toBeNull()

    await escape()
    expect(log).toEqual(['内层'])
    expect(dialog('内层')).toBeNull()
    expect(dialog('外层')).not.toBeNull()

    await escape()
    expect(log).toEqual(['内层', '外层'])
    expect(dialog('外层')).toBeNull()
  })

  it('gives focus back to the button that opened each sheet', async () => {
    await mount(<Stack log={[]} />)
    await press('open-outer')
    // Focus moved into the dialog (the trap holds it there).
    expect(dialog('外层')!.contains(document.activeElement)).toBe(true)
    await press('open-inner')
    expect(dialog('内层')!.contains(document.activeElement)).toBe(true)

    await escape()
    await settle()
    expect(document.activeElement?.id).toBe('open-inner')

    await escape()
    await settle()
    expect(document.activeElement?.id).toBe('open-outer')
  })
})

describe('Popover inside a sheet', () => {
  function Both({ log }: { log: string[] }) {
    const [pop, setPop] = useState(false)
    return (
      <Sheet title="外层" onClose={() => log.push('外层')}>
        <Popover open={pop} onOpenChange={(o) => { if (!o) log.push('浮层'); setPop(o) }}>
          <PopoverTrigger id="pop">位置</PopoverTrigger>
          <PopoverContent aria-label="浮层">内容</PopoverContent>
        </Popover>
      </Sheet>
    )
  }

  it('Escape closes the popover first and gives focus back to its trigger', async () => {
    const log: string[] = []
    await mount(<Both log={log} />)
    await press('pop')
    expect(dialog('浮层')).not.toBeNull()
    await escape()
    await settle()
    expect(log).toEqual(['浮层'])
    expect(dialog('浮层')).toBeNull()
    expect(dialog('外层')).not.toBeNull()
    expect(document.activeElement?.id).toBe('pop')
    await escape()
    expect(log).toEqual(['浮层', '外层'])
  })
})
