// The main chat (ChatPanel -> send) and project chats both put @ references
// into the message context next to what the user is looking at.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import { send, sendToProject, ui } from './app'

describe('send with mentions', () => {
  afterEach(() => vi.restoreAllMocks())

  it('main chat keeps the page context and adds mentions', async () => {
    const spy = vi.spyOn(api, 'send').mockResolvedValue({ message_id: 'm', seq: 1, topic_id: 'm' } as never)
    ui.state.current = 't1'
    ui.state.dropContext = false
    ui.state.route = { view: 'project', id: 'p1' } as typeof ui.state.route
    await send('@blog 发版', undefined, [{ project_id: 'p2', name: 'blog' }])
    expect(spy).toHaveBeenCalledWith('t1', '@blog 发版', { project_id: 'p1', mentions: [{ project_id: 'p2', name: 'blog' }] }, undefined, '')

    ui.state.route = { view: 'overview', id: '' } as typeof ui.state.route
    await send('@blog', undefined, [{ project_id: 'p2', name: 'blog' }])
    expect(spy).toHaveBeenLastCalledWith('t1', '@blog', { mentions: [{ project_id: 'p2', name: 'blog' }] }, undefined, '')

    await send('没有引用', undefined, [])
    expect(spy).toHaveBeenLastCalledWith('t1', '没有引用', undefined, undefined, '')
  })

  it('project chat sends mentions too', async () => {
    const spy = vi.spyOn(api, 'send').mockResolvedValue({ message_id: 'm', seq: 1, topic_id: 'm' } as never)
    ui.state.projectThread.p1 = 'pt'
    await sendToProject('p1', 'x', '@blog', { task_id: 'k1' }, undefined, [{ project_id: 'p2', name: 'blog' }])
    expect(spy.mock.calls.at(-1)?.[0]).toBe('pt')
    expect(spy.mock.calls.at(-1)?.[2]).toEqual({ task_id: 'k1', mentions: [{ project_id: 'p2', name: 'blog' }] })
  })
})
