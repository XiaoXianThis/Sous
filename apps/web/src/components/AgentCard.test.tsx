// A running task's card on the workbench shows a pending approval in place of
// its output summary, at the same height; the review agent's are not drawn.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { wb } from '../lib/app'
import type { Approval, Inbox, Run } from '../lib/types'
import type { CardAgent } from '../lib/workbench'
import AgentCard from './AgentCard'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let el: HTMLDivElement

const approval = (review: 'ai' | 'user'): Approval => ({
  id: 'ap1', run_id: 'run1', task_id: 'k1', title: 'Write hello.txt', tool_kind: 'edit', detail: '',
  options: [{ option_id: 'a1', name: 'Allow', kind: 'allow_once' }, { option_id: 'r1', name: 'Reject', kind: 'reject_once' }],
  status: 'pending', option_id: '', created_at: 0, category: 'edit-worktree', review, escalation: '',
} as Approval)
const agent: CardAgent = {
  run: { id: 'run1', task_id: 'k1', node_id: 'n1', agent: 'claude', purpose: 'work', status: 'running', title: '写个文件' } as Run,
  look: 'waiting', title: '写个文件',
} as CardAgent

async function render(approvals: Approval[]) {
  wb.state.inbox = { approvals, tasks: [] } as unknown as Inbox
  await act(async () => root.render(<AgentCard agent={agent} onOpen={() => {}} />))
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
  wb.state.inbox = null
})

describe('AgentCard approvals', () => {
  it('puts the approval where the output summary was', async () => {
    await render([approval('user')])
    expect(strip()).not.toBeNull()
    expect(strip()!.className).toContain('h-[36px]')
    expect(strip()!.textContent).toContain('允许')
    // No button inside another button.
    expect(el.querySelector('button button')).toBeNull()
  })

  it('keeps the output summary while the review agent judges', async () => {
    await render([approval('ai')])
    expect(strip()).toBeNull()
  })
})
