// A Run chip in the chat turns into the approval when one waits for a person,
// at the same place, and is a plain chip again otherwise; approvals the review
// agent is still judging are not drawn.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { RunView } from '../lib/runs'
import type { Approval, Run } from '../lib/types'
import { quickOptions } from './ApprovalCard'
import RunChip from './RunChip'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

const options = [
  { option_id: 'a1', name: 'Allow', kind: 'allow_once' },
  { option_id: 'a2', name: 'Always', kind: 'allow_always' },
  { option_id: 'r1', name: 'Reject', kind: 'reject_once' },
]
const approval = (review: 'ai' | 'user', status = 'pending'): Approval => ({
  id: 'ap1', run_id: 'run1', task_id: 'k1', title: 'Write hello.txt', tool_kind: 'edit', detail: '', options,
  status, option_id: '', created_at: 0, category: 'edit-worktree', review, escalation: '',
} as Approval)
const view = (approvals: Approval[]): RunView => ({
  run: { id: 'run1', task_id: 'k1', node_id: 'n1', agent: 'claude', purpose: 'work', status: 'running', title: '写个文件' } as Run,
  seq: 1, tools: [], plan: [], output: '', thinking: '', statusLine: '', approvals,
})

async function render(v: RunView) {
  await act(async () => root.render(<RunChip view={v} nodeName={() => 'mac'} />))
}
const strip = () => el.querySelector('[data-testid=approval-strip]')

beforeEach(() => {
  el = document.createElement('div')
  document.body.append(el)
  root = createRoot(el)
})
afterEach(() => {
  act(() => root.unmount())
  el.remove()
})

describe('RunChip approvals', () => {
  it('turns into the approval while a person must answer', async () => {
    await render(view([approval('user')]))
    expect(strip()).not.toBeNull()
    expect(strip()!.textContent).toContain('写入 hello.txt')
    expect(strip()!.textContent).toContain('允许')
  })

  it('stays a plain chip while the review agent judges, and once answered', async () => {
    await render(view([approval('ai')]))
    expect(strip()).toBeNull()
    await render(view([approval('user', 'decided')]))
    expect(strip()).toBeNull()
    expect(el.textContent).toContain('写个文件')
  })

  it('offers allow once and reject once as the quick answers', () => {
    const q = quickOptions({ options })
    expect(q.allow?.option_id).toBe('a1')
    expect(q.reject?.option_id).toBe('r1')
    expect(quickOptions({ options: [{ option_id: 'x', name: 'Always', kind: 'allow_always' }] }).allow?.option_id).toBe('x')
  })
})
