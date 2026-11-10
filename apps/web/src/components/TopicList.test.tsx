// The topic list is an accordion: one topic (or "更早的对话") open at a time,
// and the open one fills the chat area while the rest of the list steps aside.
// Only a click (or a push link the user followed) opens a topic: a message
// sent from the main input leaves the topic it started folded, in view and
// highlighted for a moment.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../lib/api'
import { chat, ui, wb } from '../lib/app'
import type { Task, Topic } from '../lib/types'
import ChatView from './ChatView'
import TopicList, { FLASH_MS } from './TopicList'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

const topic = (id: string, extra: Partial<Topic> = {}): Topic => ({
  id, thread_id: 't1', title: `话题 ${id}`, summary: '', status: 'open', first_seq: 1, created_at: 1, updated_at: 1, count: 1, tasks: [], ...extra,
})

async function render(topics: Topic[], earlier = 0, inbox = false) {
  vi.spyOn(api, 'topics').mockResolvedValue({ topics, earlier: { count: earlier } })
  await act(async () => root.render(<TopicList threadId="t1" sendIn={async () => {}} empty={null} inbox={inbox} />))
  // The first load waits a tick.
  await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
}

const toggleOf = (id: string) => el.querySelector<HTMLButtonElement>(`[data-topic="${id}"] header button[aria-expanded]`)
const earlierToggle = () => el.querySelector<HTMLButtonElement>('section[aria-label="更早的对话"] > button')
/** Every toggle of a topic or of "更早的对话" that says it is open. */
const expanded = () => [...el.querySelectorAll('[data-topic] header button[aria-expanded="true"], section[aria-label="更早的对话"] > button[aria-expanded="true"]')]
const filled = () => el.querySelector('[data-testid="topic-list"]')!.hasAttribute('data-filled')
const shownTopics = () => [...el.querySelectorAll('[data-topic]')].map((c) => c.getAttribute('data-topic'))
const click = (b: HTMLElement | null) => act(async () => { b!.click() })

beforeEach(() => {
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
  vi.spyOn(api, 'topicMessages').mockResolvedValue({ messages: [], last_seq: 0 })
})
afterEach(() => {
  act(() => root.unmount())
  el.remove()
  vi.useRealTimers()
  vi.restoreAllMocks()
  chat.state.messages = {}
  ui.state.openTopic = ''
  ui.state.revealTopic = ''
  wb.state.inbox = null
})

describe('topic accordion', () => {
  it('opens a topic alone, filling the list, and folds it back into the list', async () => {
    await render([topic('a'), topic('b'), topic('c')])
    expect(shownTopics()).toEqual(['a', 'b', 'c'])
    expect(expanded()).toHaveLength(0)

    await click(toggleOf('b'))
    expect(toggleOf('b')!.getAttribute('aria-expanded')).toBe('true')
    expect(expanded()).toHaveLength(1)
    expect(filled()).toBe(true)
    expect(shownTopics()).toEqual(['b'])
    // Title row on top, the reply box in the card's body under the messages.
    expect(el.querySelector('[data-topic="b"] [data-testid="topic-body"] textarea[aria-label="在这个话题里回复"]')).not.toBeNull()

    await click(toggleOf('b'))
    expect(expanded()).toHaveLength(0)
    expect(filled()).toBe(false)
    expect(shownTopics()).toEqual(['a', 'b', 'c'])
  })

  it('folds the open topic when a second one opens', async () => {
    await render([topic('a'), topic('b')])
    await click(toggleOf('a'))
    expect(toggleOf('a')!.getAttribute('aria-expanded')).toBe('true')

    // A push link the user followed opens the second topic.
    await act(async () => { ui.state.openTopic = 'b'; ui.emit() })
    expect(toggleOf('b')!.getAttribute('aria-expanded')).toBe('true')
    expect(toggleOf('a')).toBeNull()
    expect(expanded()).toHaveLength(1)

    // Folding it shows the first one folded too.
    await click(toggleOf('b'))
    expect(toggleOf('a')!.getAttribute('aria-expanded')).toBe('false')
    expect(expanded()).toHaveLength(0)

    // And opening the first again folds the second.
    await click(toggleOf('a'))
    expect(toggleOf('a')!.getAttribute('aria-expanded')).toBe('true')
    expect(toggleOf('b')).toBeNull()
    expect(expanded()).toHaveLength(1)
  })

  it('has only one aria-expanded=true at any time, "更早的对话" included', async () => {
    await render([topic('a'), topic('b')], 3)
    expect(expanded()).toHaveLength(0)

    await click(earlierToggle())
    expect(earlierToggle()!.getAttribute('aria-expanded')).toBe('true')
    expect(shownTopics()).toEqual([])
    expect(expanded()).toHaveLength(1)

    await act(async () => { ui.state.openTopic = 'a'; ui.emit() })
    expect(earlierToggle()).toBeNull()
    expect(toggleOf('a')!.getAttribute('aria-expanded')).toBe('true')
    expect(expanded()).toHaveLength(1)

    await click(toggleOf('a'))
    expect(earlierToggle()!.getAttribute('aria-expanded')).toBe('false')
    await click(earlierToggle())
    expect(expanded()).toHaveLength(1)
    expect(toggleOf('a')).toBeNull()
  })

  it('clears openTopic and still shows a linked topic of the folded closed group after it folds', async () => {
    await render([topic('a'), topic('z', { status: 'closed' })])
    // The closed group starts folded.
    expect(shownTopics()).toEqual(['a'])

    await act(async () => { ui.state.openTopic = 'z'; ui.emit() })
    expect(ui.state.openTopic).toBe('')
    expect(toggleOf('z')!.getAttribute('aria-expanded')).toBe('true')

    await click(toggleOf('z'))
    expect(shownTopics()).toEqual(['a', 'z'])
    expect(expanded()).toHaveLength(0)
  })

  it('opens nothing for a topic that is not in the list', async () => {
    await render([topic('a')])
    await act(async () => { ui.state.openTopic = 'gone'; ui.emit() })
    expect(filled()).toBe(false)
    expect(shownTopics()).toEqual(['a'])
  })

  it('switches openId and data-filled together, cycle after cycle, without scrolling into view', async () => {
    const scrolled = vi.fn()
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { value: scrolled, configurable: true, writable: true })
    wb.state.inbox = { approvals: [], tasks: [{ id: 'k', title: '等你看看', status: 'blocked', status_reason: '' } as unknown as Task] }
    await render([topic('a'), topic('b')], 2, true)
    const needs = () => el.querySelector('[data-testid="needs-you"] button[aria-expanded]')
    // Open "需要你处理": it steps aside while a topic is open and comes back as it was.
    await click(needs() as HTMLButtonElement)
    expect(needs()!.getAttribute('aria-expanded')).toBe('true')

    const bodies = () => el.querySelectorAll('[data-testid="topic-body"]').length
    const state = () => ({ filled: filled(), open: expanded().map((b) => b.closest('[data-fold]')!.getAttribute('data-fold')), bodies: bodies(), shown: shownTopics() })
    for (let round = 0; round < 3; round++) {
      for (const [key, toggle] of [['a', () => toggleOf('a')], ['b', () => toggleOf('b')], [':earlier', earlierToggle]] as const) {
        await click(toggle())
        expect(state()).toEqual({ filled: true, open: [key], bodies: key === ':earlier' ? 0 : 1, shown: key === ':earlier' ? [] : [key] })
        expect(needs()).toBeNull()

        await click(toggle())
        // Folded: nothing open, no body left behind, the whole list back with "需要你处理" still open.
        expect(state()).toEqual({ filled: false, open: [], bodies: 0, shown: ['a', 'b'] })
        expect(needs()!.getAttribute('aria-expanded')).toBe('true')
      }
    }
    expect(scrolled).not.toHaveBeenCalled()
    delete (HTMLElement.prototype as { scrollIntoView?: unknown }).scrollIntoView
  })
})

const flashed = () => [...el.querySelectorAll('[data-topic][data-flash]')].map((c) => c.getAttribute('data-topic'))

describe('a topic a message just started', () => {
  it('stays folded, highlighted for about a second, and opens on a click', async () => {
    await render([topic('a'), topic('b')])
    vi.useFakeTimers()
    await act(async () => { ui.state.revealTopic = 'b'; ui.emit() })
    expect(ui.state.revealTopic).toBe('')
    expect(toggleOf('b')!.getAttribute('aria-expanded')).toBe('false')
    expect(expanded()).toHaveLength(0)
    expect(filled()).toBe(false)
    expect(shownTopics()).toEqual(['a', 'b'])
    expect(flashed()).toEqual(['b'])

    await act(async () => { vi.advanceTimersByTime(FLASH_MS) })
    expect(flashed()).toEqual([])
    vi.useRealTimers()

    await click(toggleOf('b'))
    expect(toggleOf('b')!.getAttribute('aria-expanded')).toBe('true')
    expect(expanded()).toHaveLength(1)
  })

  it('folds an open topic so the new one shows, folded, and the accordion keeps one open', async () => {
    await render([topic('a'), topic('b')])
    await click(toggleOf('a'))
    expect(expanded()).toHaveLength(1)

    await act(async () => { ui.state.revealTopic = 'b'; ui.emit() })
    expect(filled()).toBe(false)
    expect(shownTopics()).toEqual(['a', 'b'])
    expect(toggleOf('a')!.getAttribute('aria-expanded')).toBe('false')
    expect(toggleOf('b')!.getAttribute('aria-expanded')).toBe('false')
    expect(expanded()).toHaveLength(0)

    await click(toggleOf('a'))
    expect(expanded()).toHaveLength(1)
    await click(toggleOf('a'))
    await click(toggleOf('b'))
    expect(expanded()).toHaveLength(1)
    expect(toggleOf('a')).toBeNull()
  })

  it('shows a new topic of the folded closed group without opening it', async () => {
    await render([topic('a'), topic('z', { status: 'closed' })])
    expect(shownTopics()).toEqual(['a'])
    await act(async () => { ui.state.revealTopic = 'z'; ui.emit() })
    expect(shownTopics()).toEqual(['a', 'z'])
    expect(toggleOf('z')!.getAttribute('aria-expanded')).toBe('false')
    expect(expanded()).toHaveLength(0)
  })

  it('sent from the main input, leaves its topic folded with only one topic ever expanded', async () => {
    localStorage.removeItem('sous.chatView')
    // The rest of the chat (context meter…) reads from the Hub: nothing answers here.
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
    vi.spyOn(api, 'topics').mockResolvedValue({ topics: [topic('a'), topic('b')], earlier: { count: 0 } })
    await act(async () => root.render(
      <ChatView label="对话" threadId="t1" threads={[]} title="sous" avatar={null} threadLabel={(t) => t.title} threadIcon={() => 'chat'}
        onPick={() => {}} onNew={() => {}} onSend={async () => 'b'} onSendIn={async () => {}} placeholder="说点什么" empty={null} />,
    ))
    await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    // One topic open before sending.
    await click(toggleOf('a'))
    expect(expanded()).toHaveLength(1)

    const box = el.querySelector<HTMLTextAreaElement>('textarea[placeholder*="开一个新话题"]')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(box, '新的事')
      box.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    })

    expect(ui.state.openTopic).toBe('')
    expect(toggleOf('b')!.getAttribute('aria-expanded')).toBe('false')
    expect(expanded().length).toBeLessThanOrEqual(1)
    expect(expanded()).toHaveLength(0)
    expect(flashed()).toEqual(['b'])
  })
})
