// Thinking opens while the model is still reasoning and folds once the answer
// starts; a click overrides that.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { Message } from '../lib/types'
import MessageView from './Message'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

const base: Message = { id: 'm1', thread_id: 't1', seq: 1, role: 'assistant', content: '', created_at: 0 } as Message

async function render(m: Partial<Message>) {
  await act(async () => root.render(<MessageView message={{ ...base, ...m }} />))
}
const box = () => el.querySelector('[data-testid=thinking]')
const toggle = () => el.querySelector('button[aria-expanded]') as HTMLButtonElement

beforeEach(() => {
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(() => {
  act(() => root.unmount())
  el.remove()
})

describe('thinking', () => {
  it('is open while thinking and folds when the answer starts', async () => {
    await render({ streaming: true, thinking: '先看看' })
    expect(box()).not.toBeNull()
    expect(toggle().textContent).toContain('正在思考')
    await render({ streaming: true, thinking: '先看看', content: '好的' })
    expect(box()).toBeNull()
    expect(toggle().textContent).toContain('思考过程')
  })

  it('starts folded for a finished message and opens on click', async () => {
    await render({ thinking: '先看看', content: '好的' })
    expect(box()).toBeNull()
    await act(async () => toggle().click())
    expect(box()?.textContent).toContain('先看看')
  })

  it('stays shut when folded by hand during thinking', async () => {
    await render({ streaming: true, thinking: '先看看' })
    await act(async () => toggle().click())
    expect(box()).toBeNull()
    await render({ streaming: true, thinking: '先看看，再想想' })
    expect(box()).toBeNull()
  })
})

describe('@ references in a user message', () => {
  it('shows each referenced project as a tag with its name', async () => {
    await render({
      role: 'user',
      content: '@blog 和 @web shop 都要发',
      context: { mentions: [{ project_id: 'p2', name: 'blog' }, { project_id: 'p3', name: 'web shop' }] },
    })
    const tags = Array.from(el.querySelectorAll('.mention-tag'))
    expect(tags.map((t) => t.textContent)).toEqual(['@blog', '@web shop'])
    expect(tags[1].getAttribute('title')).toBe('引用的项目：web shop')
    expect(tags[1].getAttribute('data-project-id')).toBe('p3')
    expect(el.textContent).toContain('@blog 和 @web shop 都要发')
  })

  it('leaves an @ without a reference as plain text', async () => {
    await render({ role: 'user', content: '发邮件到 a@b.com' })
    expect(el.querySelector('.mention-tag')).toBeNull()
  })
})
