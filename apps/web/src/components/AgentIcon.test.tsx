import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import AgentIcon from './AgentIcon'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

beforeEach(() => {
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})

afterEach(() => {
  act(() => root.unmount())
  el.remove()
})

async function render(props: ComponentProps<typeof AgentIcon>) {
  await act(async () => root.render(<AgentIcon {...props} />))
  return el.querySelector<HTMLSpanElement>('[role="img"]')!
}

describe('AgentIcon', () => {
  it.each([
    ['claude', 'Claude Code', 'm4.7144 15.9555'],
    ['cursor', 'Cursor', 'M11.503.131'],
    ['opencode', 'OpenCode', 'M22 24H2V0h20z'],
    ['gemini', 'Gemini', 'M11.04 19.32'],
    ['codex', 'Codex', 'M8.086.457'],
    ['kiro', 'Kiro', 'M4.594 6.677'],
  ])('renders the upstream %s logo with its accessible name', async (name, label, prefix) => {
    const icon = await render({ name })
    expect(icon.title).toBe(label)
    expect(icon.getAttribute('aria-label')).toBe(label)
    expect(icon.querySelector('svg')?.getAttribute('viewBox')).toBe('0 0 24 24')
    expect(icon.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    expect(icon.querySelector('path')?.getAttribute('d')).toMatch(new RegExp(`^${prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`))
    expect(icon.querySelector('path')?.getAttribute('fill')).toBe('currentColor')
    expect(icon.querySelector('image')).toBeNull()
  })

  it('keeps Claude’s brand color and the size, radius, class and title overrides', async () => {
    const icon = await render({ name: 'CLAUDE', size: 32, title: '项目助手', className: 'agent-badge' })
    expect(icon.style.background).toBe('#fff')
    expect(icon.style.color).toBe('#D97757')
    expect(icon.style.width).toBe('32px')
    expect(icon.style.height).toBe('32px')
    expect(icon.style.borderRadius).toBe('9px')
    expect(icon.classList.contains('agent-badge')).toBe(true)
    expect(icon.querySelector('svg')?.getAttribute('width')).toBe('22')
    expect(icon.title).toBe('项目助手')
    expect(icon.getAttribute('aria-label')).toBe('项目助手')
  })

  it('uses the first letter for an unknown agent with the same accessible label', async () => {
    const icon = await render({ name: 'future-agent' })
    expect(icon.textContent).toBe('F')
    expect(icon.querySelector('svg')).toBeNull()
    expect(icon.title).toBe('future-agent')
    expect(icon.getAttribute('aria-label')).toBe('future-agent')
    const custom = await render({ name: 'future-agent', title: '自定义助手' })
    expect(custom.title).toBe('自定义助手')
    expect(custom.getAttribute('aria-label')).toBe('自定义助手')
    expect(custom.textContent).toBe('F')
  })

  it('keeps the empty-name fallback', async () => {
    const icon = await render({ name: '' })
    expect(icon.textContent).toBe('?')
    expect(icon.title).toBe('agent')
    expect(icon.getAttribute('aria-label')).toBe('agent')
  })
})
