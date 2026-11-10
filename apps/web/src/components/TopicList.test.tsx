// The topic list is an accordion: one topic (or "更早的对话") open at a time,
// and the open one fills the chat area while the rest of the list steps aside.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../lib/api'
import { chat, ui } from '../lib/app'
import type { Topic } from '../lib/types'
import TopicList from './TopicList'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

const topic = (id: string, extra: Partial<Topic> = {}): Topic => ({
  id, thread_id: 't1', title: `话题 ${id}`, summary: '', status: 'open', first_seq: 1, created_at: 1, updated_at: 1, count: 1, tasks: [], ...extra,
})

async function render(topics: Topic[], earlier = 0) {
  vi.spyOn(api, 'topics').mockResolvedValue({ topics, earlier: { count: earlier } })
  await act(async () => root.render(<TopicList threadId="t1" sendIn={async () => {}} empty={null} />))
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
  vi.restoreAllMocks()
  chat.state.messages = {}
  ui.state.openTopic = ''
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

    // A push link or a message just sent opens the second topic.
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
})
