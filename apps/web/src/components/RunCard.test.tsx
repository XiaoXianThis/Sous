// A run in the task's history: the latest shows its prompt and final reply with
// the steps folded behind "执行过程" and how long it ran; earlier runs fold to
// their header until clicked.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { RunView } from '../lib/runs'
import type { Run } from '../lib/types'
import RunCard from './RunCard'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

const view = (): RunView => ({
  run: {
    id: 'run1', task_id: 'k1', node_id: 'n1', agent: 'claude', purpose: 'work', status: 'succeeded',
    prompt: '写一个 hello.txt', summary: '已经写好了 hello.txt', started_at: 1_000, ended_at: 186_000, approvals: [],
  } as unknown as Run,
  seq: 1, plan: [], output: '', thinking: '', statusLine: '', approvals: [],
  tools: [{ id: 't1', title: 'Write hello.txt', status: 'completed' }] as RunView['tools'],
})

async function render(latest: boolean) {
  await act(async () => root.render(<RunCard view={view()} nodeName={() => 'mac'} latest={latest} />))
}
const process = () => el.querySelector('[aria-label=执行过程]')
const toggle = () => [...el.querySelectorAll('button')].find((b) => b.textContent?.includes('执行过程'))

beforeEach(() => {
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(() => {
  act(() => root.unmount())
  el.remove()
})

describe('RunCard', () => {
  it('shows the prompt, the final reply and the duration, with the steps folded', async () => {
    await render(true)
    expect(el.textContent).toContain('写一个 hello.txt')
    expect(el.textContent).toContain('已经写好了 hello.txt')
    expect(toggle()!.textContent).toContain('3 分 5 秒')
    expect(process()).toBeNull()
    await act(async () => toggle()!.click())
    expect(process()!.textContent).toContain('Write hello.txt')
  })

  it('folds an earlier run to its header until clicked', async () => {
    await render(false)
    expect(el.textContent).not.toContain('写一个 hello.txt')
    expect(el.textContent).toContain('3 分 5 秒')
    const head = el.querySelector<HTMLButtonElement>('header button[aria-expanded]')!
    await act(async () => head.click())
    expect(el.textContent).toContain('已经写好了 hello.txt')
  })
})
