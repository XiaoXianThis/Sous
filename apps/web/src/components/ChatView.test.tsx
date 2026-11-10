// @ references in the composer: the list of active projects, keyboard and
// mouse picks, and the project ids that go out with the message.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { wb } from '../lib/app'
import type { Mention, Overview, Project } from '../lib/types'
import ChatView from './ChatView'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement
let sent: { text: string; mentions: Mention[] }[]

const project = (id: string, name: string, archived_at = 0) => ({ id, name, archived_at }) as Project

function setProjects(projects: Project[]) {
  wb.state.data = { projects, nodes: [], personas: [], tasks: [], runs: [], approvals: [], discovered: [] } as Overview
  wb.emit()
}

async function render() {
  await act(async () =>
    root.render(
      <ChatView
        label="对话"
        threadId=""
        threads={[]}
        title="sous"
        avatar={null}
        threadLabel={(t) => t.title}
        threadIcon={() => 'chat'}
        onPick={() => {}}
        onNew={() => {}}
        onSend={async (text, _ids, mentions) => { sent.push({ text, mentions }) }}
        placeholder="说点什么"
        empty={null}
      />,
    ),
  )
}

const box = () => el.querySelector('textarea') as HTMLTextAreaElement
// The @ list is a Radix popover drawn in <body>, so these look in the whole document.
const options = () => Array.from(document.querySelectorAll('[role=option]')).map((o) => o.textContent)
const menu = () => document.querySelector('.mention-menu')

/** Types text at the caret like a user would: value, caret, then the input event. */
async function type(text: string) {
  const t = box()
  const at = t.selectionStart ?? t.value.length
  const value = t.value.slice(0, at) + text + t.value.slice(at)
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(t, value)
    t.setSelectionRange(at + text.length, at + text.length)
    t.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function key(k: string) {
  await act(async () => {
    box().dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))
  })
}

beforeEach(() => {
  sent = []
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(() => {
  act(() => root.unmount())
  el.remove()
  wb.state.data = null
})

describe('@ references', () => {
  it('lists active projects only and filters as you type', async () => {
    setProjects([project('p1', 'sous'), project('p2', 'blog'), project('p3', 'old-site', 1)])
    await render()
    act(() => box().focus())
    await type('看看 @')
    expect(options()).toEqual(['sous', 'blog'])
    await type('bl')
    expect(options()).toEqual(['blog'])
  })

  it('picks with the keyboard and sends the project id', async () => {
    setProjects([project('p1', 'sous'), project('p2', 'blog')])
    await render()
    act(() => box().focus())
    await type('@')
    await key('ArrowDown')
    expect(document.querySelector('[aria-selected=true]')?.textContent).toBe('blog')
    await key('Enter')
    expect(box().value).toBe('@blog ')
    expect(box().selectionStart).toBe(6)
    expect(menu()).toBeNull()
    expect(el.querySelector('.mention-layer mark')?.textContent).toBe('@blog')
    await type('要发版')
    await key('Enter')
    expect(sent).toEqual([{ text: '@blog 要发版', mentions: [{ project_id: 'p2', name: 'blog' }] }])
  })

  it('picks with the mouse, keeping the text after the caret', async () => {
    setProjects([project('p1', 'sous'), project('p2', 'blog')])
    await render()
    act(() => box().focus())
    await type('改一下')
    box().setSelectionRange(0, 0)
    await type('@so')
    await act(async () => (document.querySelector('[role=option]') as HTMLButtonElement).click())
    expect(box().value).toBe('@sous 改一下')
    await key('Enter')
    expect(sent[0].mentions).toEqual([{ project_id: 'p1', name: 'sous' }])
  })

  it('Tab picks, Escape closes without picking', async () => {
    setProjects([project('p1', 'sous')])
    await render()
    act(() => box().focus())
    await type('@s')
    await key('Escape')
    expect(menu()).toBeNull()
    await type('x')
    expect(menu()).toBeNull()
    await type(' @')
    await key('Tab')
    expect(box().value).toBe('@sx @sous ')
  })

  it('drops a reference whose tag was deleted', async () => {
    setProjects([project('p1', 'sous')])
    await render()
    act(() => box().focus())
    await type('@')
    await key('Enter')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(box(), 'sous 好的')
      box().dispatchEvent(new Event('input', { bubbles: true }))
    })
    await key('Enter')
    expect(sent).toEqual([{ text: 'sous 好的', mentions: [] }])
  })

  it('says so when there is nothing to reference', async () => {
    setProjects([project('p3', 'old-site', 1)])
    await render()
    act(() => box().focus())
    await type('@')
    expect(options()).toEqual([])
    expect(menu()?.textContent).toContain('没有可引用的项目')
    await key('Enter')
    expect(sent).toEqual([{ text: '@', mentions: [] }])
  })
})

vi.mock('./ModelSwitch', () => ({ default: ({ placement = 'composer' }: { placement?: string }) => <button data-model-placement={placement}>模型</button> }))
vi.mock('../lib/phone', () => ({ usePhone: () => phone.value }))
const phone = vi.hoisted(() => ({ value: false }))
const ctx = vi.hoisted(() => ({ view: null as null | { segment: number } }))
vi.mock('./ContextMeter', () => ({
  ContextPanel: () => <div data-context-panel />,
  ContextRing: ({ onOpen }: { onOpen: () => void }) => <button aria-label="上下文" onClick={onOpen}>环</button>,
  useThreadContext: () => [ctx.view, () => {}],
}))


describe('phone composer', () => {
  it('puts the model in the header and dismisses the keyboard', async () => {
    phone.value = true
    try {
      await render()
      expect(el.querySelector('.chat-head [data-model-placement=header]')).not.toBeNull()
      expect(el.querySelector('.composer-tools [data-model-placement]')).toBeNull()
      act(() => box().focus())
      expect(el.querySelector('[data-typing]')).not.toBeNull()
      const hide = el.querySelector('[aria-label=收起键盘]') as HTMLButtonElement
      expect(hide).not.toBeNull()
      act(() => hide.click())
      expect(document.activeElement).not.toBe(box())
      expect(el.querySelector('.kb-hide')).toBeNull()
    } finally { phone.value = false }
  })
  it('keeps the desktop model in the composer', async () => {
    await render()
    expect(el.querySelector('.composer-tools [data-model-placement=composer]')).not.toBeNull()
    expect(el.querySelector('.chat-head [data-model-placement]')).toBeNull()
  })
})

describe('context ring', () => {
  const ring = () => el.querySelector('[aria-label=上下文]') as HTMLButtonElement | null
  for (const isPhone of [false, true]) {
    it(`sits in the composer right before send (${isPhone ? 'phone' : 'desktop'})`, async () => {
      phone.value = isPhone
      ctx.view = { segment: 1200 }
      try {
        await render()
        expect(el.querySelector('.chat-head [aria-label=上下文]')).toBeNull()
        const r = ring()!
        const tools = el.querySelector('.composer-tools')!
        expect(r.closest('.composer-tools')).toBe(tools)
        expect(tools.lastElementChild!.contains(r)).toBe(true)
        expect(tools.nextElementSibling!.classList.contains('composer-send')).toBe(true)
        act(() => r.click())
        expect(sent).toEqual([])
        expect(el.querySelector('[data-context-panel]')).not.toBeNull()
      } finally { phone.value = false; ctx.view = null }
    })
  }
  it('takes no space without context data', async () => {
    ctx.view = { segment: 0 }
    try {
      await render()
      expect(ring()).toBeNull()
      expect(el.querySelector('.composer-ring')).toBeNull()
    } finally { ctx.view = null }
  })
})
