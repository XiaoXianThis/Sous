// Runs the reducer against the shared store the way React components read it:
// a subscriber sees a thread's first message after an event arrives.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { chat, useChat } from './app'
import { applyEvent, emptyState } from './reduce'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('reducer on the store', () => {
  let root: Root | undefined
  let container: HTMLDivElement | undefined

  afterEach(async () => {
    await act(async () => root?.unmount())
    container?.remove()
  })

  it('shows the first message of a thread', async () => {
    Object.assign(chat.state, emptyState())
    let shown: string[] = []
    function Probe() {
      const s = useChat()
      shown = (s.messages.t ?? []).map((m) => m.content)
      return null
    }
    container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    await act(async () => {
      root!.render(<Probe />)
    })
    expect(shown).toEqual([])

    await act(async () => {
      applyEvent(chat.state, { seq: 1, id: 'e1', ts: 1, type: 'message.user', actor: 'user', thread_id: 't', payload: { message_id: 'u1', content: '第一条' } })
      chat.emit()
    })
    expect(shown).toEqual(['第一条'])
  })
})
