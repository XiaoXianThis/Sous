import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import Sheet, { SheetPlaceContext } from './Sheet'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

async function mount(node: React.ReactNode) {
  const el = document.createElement('div')
  document.body.append(el)
  const root = createRoot(el)
  await act(async () => root.render(node))
  return { el, done: () => act(async () => { root.unmount(); el.remove() }) }
}

describe('Sheet placement', () => {
  it('stays in place outside settings', async () => {
    const { el, done } = await mount(<Sheet title="外面" onClose={() => {}}>x</Sheet>)
    expect(el.querySelector('[role=dialog]')).not.toBeNull()
    await done()
  })

  it('flattens the settings page root but draws dialogs opened from it in <body>', async () => {
    const { el, done } = await mount(
      <SheetPlaceContext.Provider value="root">
        <Sheet title="页面" onClose={() => {}}>
          <Sheet title="二级" onClose={() => {}}>y</Sheet>
        </Sheet>
      </SheetPlaceContext.Provider>,
    )
    expect(el.querySelector('[aria-label=页面]')).not.toBeNull()
    expect(el.querySelector('[aria-label=二级]')).toBeNull()
    const nested = document.querySelector('[aria-label=二级]')
    expect(nested?.closest('[data-nested-sheet]')?.parentElement).toBe(document.body)
    await done()
  })
})
