// Runs the reducer against Svelte's $state proxy the way the Chat component
// uses it: a derived value reads the thread's messages before any arrive.
import { flushSync } from 'svelte'
import { describe, expect, it } from 'vitest'
import { applyEvent, emptyState } from './reduce'

describe('reducer on $state', () => {
  it('shows the first message of a thread', () => {
    const s = $state(emptyState())
    let shown: string[] = []
    const stop = $effect.root(() => {
      const msgs = $derived(s.messages.t ?? [])
      $effect(() => {
        shown = msgs.map((m) => m.content)
      })
    })
    flushSync()
    applyEvent(s, { seq: 1, id: 'e1', ts: 1, type: 'message.user', actor: 'user', thread_id: 't', payload: { message_id: 'u1', content: '第一条' } })
    flushSync()
    expect(shown).toEqual(['第一条'])
    stop()
  })
})
