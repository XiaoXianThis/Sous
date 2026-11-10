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

describe('supervisor tools', () => {
  const tools = [
    { name: 'task_list', args: 'status=running' },
    { name: 'machine_look', args: 'command=ls', error: '机器离线' },
  ]
  const line = () => el.querySelector('[data-testid=supervisor-tools] button[aria-expanded]') as HTMLButtonElement

  it('is one folded line that lists each call when opened', async () => {
    await render({ content: '先看看', kind: 'step', tools })
    expect(line().textContent).toContain('调用了 2 个工具')
    expect(line().textContent).toContain('1 个失败')
    expect(line().getAttribute('aria-expanded')).toBe('false')
    expect(el.querySelectorAll('[data-testid=supervisor-tools] li')).toHaveLength(0)
    await act(async () => line().click())
    const items = [...el.querySelectorAll('[data-testid=supervisor-tools] li')].map((li) => li.textContent)
    expect(items).toEqual(['task_list · status=running', 'machine_look · command=ls · 失败：机器离线'])
    expect(el.querySelector('[data-testid=supervisor-tools] .bg-danger')).not.toBeNull()
  })

  it('is not shown without tool calls', async () => {
    await render({ content: '好了' })
    expect(el.querySelector('[data-testid=supervisor-tools]')).toBeNull()
  })
})

describe('system lines', () => {
  const row = (id: string) => el.querySelector(`[data-testid=${id}] > button[aria-expanded]`) as HTMLButtonElement

  it('folds a task-news trigger to one line, not a user bubble, and opens on click', async () => {
    const content = '【任务进展】任务「运维：把 inbox 里已完成且有 PR 的旧任务标记为已处理（只改 dismissed）」有了新汇报，请看看。\n第二行细节'
    await render({ role: 'user', kind: 'trigger', content })
    expect(el.querySelector('.bg-user-bubble')).toBeNull()
    const b = row('message-trigger')
    expect(b.tagName).toBe('BUTTON')
    expect(b.getAttribute('aria-expanded')).toBe('false')
    expect(b.textContent).toContain('任务进展')
    expect(b.querySelector('.text-ellipsis')?.textContent).toContain('运维：把 inbox')
    expect(el.textContent).not.toContain('第二行细节')
    await act(async () => b.click())
    expect(b.getAttribute('aria-expanded')).toBe('true')
    expect(el.textContent).toContain('第二行细节')
    await act(async () => b.click())
    expect(el.textContent).not.toContain('第二行细节')
  })

  it.each([
    ['notice', '提醒'],
    ['report', '任务汇报'],
    ['inspector', '巡检'],
  ])('folds a %s to one line with its first line as the title', async (kind, label) => {
    await render({ kind: kind as Message['kind'], content: '构建失败了\n\n详细日志在这里' })
    const b = row(`message-${kind}`)
    expect(b.textContent).toContain(label)
    expect(b.textContent).toContain('构建失败了')
    expect(el.textContent).not.toContain('详细日志在这里')
    await act(async () => b.click())
    expect(el.textContent).toContain('详细日志在这里')
  })

  it('keeps the title shrinkable so a long one cannot widen the line', async () => {
    await render({ kind: 'report', content: '很长'.repeat(200) })
    const b = row('message-report')
    expect(b.className).toContain('max-w-full')
    const title = b.querySelector('.text-ellipsis')!
    expect(title.className).toContain('min-w-0')
    expect(title.className).toContain('overflow-hidden')
    expect(title.className).not.toContain('flex-none')
  })
})
