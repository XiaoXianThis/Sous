// Settings on a phone: a grouped list that pushes each page in, with a back
// button the edge swipe can press, and Escape going back before closing.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ui } from '../lib/app'

let phone = true
vi.mock('../lib/phone', () => ({ PHONE_QUERY: '(max-width: 760px)', usePhone: () => phone }))

const { default: SettingsModal } = await import('./SettingsModal')

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

async function render() {
  await act(async () => root.render(<SettingsModal onLogout={() => {}} />))
}
async function set(settings: string) {
  await act(async () => {
    ui.state.settings = settings
    ui.emit()
  })
}
const click = (b: Element) => act(async () => (b as HTMLElement).click())
const press = (key: string) => act(async () => { window.dispatchEvent(new KeyboardEvent('keydown', { key })) })

beforeEach(() => {
  // The machines page loads its list; keep it pending.
  vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
  phone = true
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(async () => {
  await act(async () => root.unmount())
  el.remove()
  ui.state.settings = ''
  vi.unstubAllGlobals()
})

describe('SettingsModal on a phone', () => {
  it('lists the pages in groups and pushes one in', async () => {
    await set('home')
    await render()
    const rows = [...el.querySelectorAll('.ios-row')].map((r) => r.textContent)
    expect(rows).toEqual(['机器', '模型', '岗位', 'MCP', '定时任务', '记忆', '用量', '通知与默认', '外观', '账号'])
    expect(el.querySelectorAll('.ios-group').length).toBe(5)
    expect(el.querySelector('[data-back]')).toBeNull()

    await click([...el.querySelectorAll('.ios-row')].find((r) => r.textContent === '外观')!)
    expect(ui.state.settings).toBe('appearance')
    const page = el.querySelector('[data-page]')
    expect(page?.classList.contains('push-in')).toBe(true)
    expect(page?.querySelector('h1')?.textContent).toBe('外观')
    expect(el.querySelector('[role="radiogroup"]')).not.toBeNull()

    await click(el.querySelector('[data-back]')!)
    expect(ui.state.settings).toBe('home')
  })

  it('goes back to the list on Escape, then closes', async () => {
    await set('appearance')
    await render()
    await press('Escape')
    expect(ui.state.settings).toBe('home')
    await press('Escape')
    expect(ui.state.settings).toBe('')
  })

  it('opens the first page for "home" on a desktop', async () => {
    phone = false
    await set('home')
    await render()
    expect(el.querySelector('.ios-row')).toBeNull()
    expect(el.querySelector('[aria-current="page"]')?.textContent).toBe('机器')
  })
})
