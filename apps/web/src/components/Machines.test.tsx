import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../lib/api'
import type { AgentInfo, Node } from '../lib/types'
import Machines from './Machines'

let phone = false
vi.mock('../lib/phone', () => ({ usePhone: () => phone }))
vi.mock('./AgentPanel', () => ({ default: () => null }))
vi.mock('./ConfigEditor', () => ({ default: () => null }))
vi.mock('../lib/api', () => ({ api: { nodes: vi.fn(), updates: vi.fn(), setAgentDisabled: vi.fn() } }))
vi.mock('../lib/app', () => ({ useChat: () => ({ nodesRev: 0, jobs: {} }) }))

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root
let el: HTMLDivElement
let machine: Node
const agent = (name: string, values: Partial<AgentInfo> = {}): AgentInfo => ({ name, title: name, installed: true, transport: 'acp', ...values })
const click = (element: Element) => act(async () => (element as HTMLElement).click())
const entry = () => [...el.querySelectorAll('button')].find((b) => b.textContent?.includes('个未启用'))
const popup = () => document.querySelector('[aria-label="未启用的 agent"]')!
const toggle = (name: string, within: ParentNode = el) => within.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="启用 ${name}"]`)!
const render = () => act(async () => root.render(<Machines onClose={vi.fn()} />))

beforeEach(() => {
  vi.resetAllMocks()
  phone = false
  machine = { id: 'node-1', name: '工作机', online: true, admin: 'on', os: 'linux', arch: 'amd64', hostname: 'pc', version: 'dev', concurrency: 2, last_seen: 0, created_at: 0,
    agents: [agent('enabled'), agent('disabled', { disabled: true, custom: true }), agent('missing', { installed: false })] }
  vi.mocked(api.nodes).mockImplementation(async () => ({ nodes: structuredClone([machine]) }))
  vi.mocked(api.updates).mockReturnValue(new Promise(() => {}))
  vi.mocked(api.setAgentDisabled).mockImplementation(async (_, name, disabled) => {
    const a = machine.agents!.find((a) => a.name === name)!
    a.disabled = disabled
    return a
  })
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(async () => {
  await act(async () => root.unmount())
  el.remove()
})

describe('Machines agent switches', () => {
  it('shows only installed and enabled agents in the table, including custom agents', async () => {
    machine.agents!.push(agent('custom', { custom: true }), agent('logged-out', { logged_in: false }))
    await render()
    const table = el.querySelector('table')!
    expect(table.textContent).toContain('enabled')
    expect(table.textContent).toContain('custom')
    expect(table.textContent).toContain('logged-out')
    expect(table.textContent).not.toContain('disabled')
    expect(table.textContent).not.toContain('missing')
    expect(entry()?.textContent).toBe('+ 2 个未启用')
  })

  it('enables a disabled custom agent from the popover and reloads nodes', async () => {
    await render()
    await click(entry()!)
    expect(popup().textContent).toContain('已停用')
    expect(toggle('disabled', popup()).disabled).toBe(false)
    await click(toggle('disabled', popup()))
    expect(api.setAgentDisabled).toHaveBeenCalledWith('node-1', 'disabled', false)
    expect(api.nodes).toHaveBeenCalledTimes(2)
    expect(el.querySelector('table')?.textContent).toContain('disabled')
    expect(popup().textContent).not.toContain('disabled')
  })

  it('hides the entry when every agent is enabled', async () => {
    machine.agents = [agent('enabled')]
    await render()
    expect(entry()).toBeUndefined()
  })

  it('keeps uninstalled agents off and explains that installation is required', async () => {
    await render()
    await click(entry()!)
    const button = toggle('missing', popup())
    expect(button.disabled).toBe(true)
    expect(button.getAttribute('aria-checked')).toBe('false')
    expect(button.title).toBe('请先安装')
    expect(popup().textContent).toContain('未安装 · 请先安装')
    await click(button)
    expect(api.setAgentDisabled).not.toHaveBeenCalled()
  })

  it('optimistically disables an agent and rolls back with the original backend error', async () => {
    let reject!: (error: Error) => void
    vi.mocked(api.setAgentDisabled).mockImplementation(() => new Promise((_, fail) => { reject = fail }))
    await render()
    await click(toggle('enabled'))
    expect(el.querySelector('table')?.textContent).not.toContain('enabled')
    expect(api.setAgentDisabled).toHaveBeenCalledWith('node-1', 'enabled', true)
    const message = '这台电脑的默认 agent 是 enabled，先在 node.json 换掉 default_agent，再停用它'
    await act(async () => reject(new Error(message)))
    expect(toggle('enabled').getAttribute('aria-checked')).toBe('true')
    expect(el.querySelector('[role="alert"]')?.textContent).toBe(message)
  })

  it('rolls back a failed enable inside the popover', async () => {
    vi.mocked(api.setAgentDisabled).mockRejectedValue(new Error('机器不在线'))
    await render()
    await click(entry()!)
    await click(toggle('disabled', popup()))
    expect(toggle('disabled', popup()).getAttribute('aria-checked')).toBe('false')
    expect(popup().querySelector('[role="alert"]')?.textContent).toBe('机器不在线')
  })

  it('uses a nested Sheet on phones and Escape keeps the machines page open', async () => {
    phone = true
    await render()
    await click(entry()!)
    expect(popup().classList.contains('sheet')).toBe(true)
    // A dialog drawn in <body>, out of the settings panel (which clips it) and above the tab bar.
    expect(el.contains(popup())).toBe(false)
    expect(popup().parentElement).toBe(document.body)
    await act(async () => document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })))
    expect(document.querySelector('[aria-label="未启用的 agent"]')).toBeNull()
    expect(el.querySelector('[aria-label="机器"]')).not.toBeNull()
  })

  it.each([['off', true], ['on', false]] as const)('disables switches when admin=%s and online=%s', async (admin, online) => {
    machine.admin = admin
    machine.online = online
    await render()
    expect(toggle('enabled').disabled).toBe(true)
    await click(entry()!)
    expect(toggle('disabled', popup()).disabled).toBe(true)
  })
})
