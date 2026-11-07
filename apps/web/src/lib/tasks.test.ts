import { describe, expect, it } from 'vitest'
import { agentChoices, agentUnavailable, approvalTitle, machinesFor, nodeLabel, optionText } from './tasks'

describe('agentChoices', () => {
  it('offers the known agents before any machine reports', () => {
    expect(agentChoices([])).toContain('opencode')
    const node = { agents: [{ name: 'fake', installed: true }, { name: 'claude', installed: true }] } as any
    expect(agentChoices([node]).filter((a) => a === 'claude')).toHaveLength(1)
    expect(agentChoices([node])).toContain('fake')
  })
})

describe('availability in the task form (end-to-end review 4.10)', () => {
  const mac = { id: 'mac', name: 'Mac', online: true, role: 'dev', agents: [{ name: 'claude', installed: true }, { name: 'codex', installed: false }] } as any
  const win = { id: 'win', name: 'Win', online: false, role: 'dev', agents: [{ name: 'kiro', installed: true }] } as any
  const srv = { id: 'srv', name: 'Srv', online: true, role: 'server', agents: [] } as any
  it('says why an agent cannot run there', () => {
    expect(agentUnavailable('claude', [mac, win], '')).toBe('')
    expect(agentUnavailable('codex', [mac, win], '')).toBe('还没有机器装了这个 agent')
    expect(agentUnavailable('kiro', [mac, win], '')).toBe('装了它的机器现在都不在线')
    expect(agentUnavailable('claude', [mac, win], 'win')).toBe('Win 上没有装这个 agent')
    expect(agentUnavailable('', [mac], 'mac')).toBe('')
  })
  it('leaves out agents node.json turns off (decision 0024)', () => {
    const box = { id: 'box', name: 'Box', online: true, role: 'dev', agents: [{ name: 'claude', installed: true, disabled: true }, { name: 'gemini', installed: true, custom: true }] } as any
    expect(agentUnavailable('claude', [box], 'box')).toBe('Box 上停用了这个 agent')
    expect(agentChoices([box])).toContain('gemini')
    expect(machinesFor('claude', [mac, box]).map((n) => n.id)).toEqual(['mac'])
    expect(machinesFor('kiro', [win]).map((n) => n.id)).toEqual(['win'])
  })
  it('labels servers and offline machines', () => {
    expect(nodeLabel(mac, 'worktree').text).toBe('Mac')
    expect(nodeLabel(win, 'worktree').text).toContain('离线')
    expect(nodeLabel(srv, 'worktree').text).toContain('服务器')
  })
})

describe('approval words (end-to-end review 4.6)', () => {
  it('puts common titles and every option kind in Chinese', () => {
    expect(approvalTitle('Write hello.txt')).toBe('写入 hello.txt')
    expect(approvalTitle('Allow web search?')).toBe('允许联网搜索？')
    expect(approvalTitle('运行部署后的健康检查')).toBe('运行部署后的健康检查')
    for (const k of ['allow_once', 'allow_always', 'reject_once', 'reject_always']) expect(optionText[k].text).toMatch(/[一-鿿]/)
    expect(optionText.allow_always.tip).toContain('agent')
  })
})
