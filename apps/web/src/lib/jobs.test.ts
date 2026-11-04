import { describe, expect, it } from 'vitest'
import { applyJobEvent, extractLinks, latestJob, mergeJobTail, stripAnsi, type JobView } from './jobs'
import { applyEvent, emptyState } from './reduce'
import type { HubEvent } from './types'

const ev = (seq: number, type: string, payload: any): HubEvent => ({ seq, id: `e${seq}`, ts: seq, type, actor: 'system', payload })

describe('jobs', () => {
  it('collects output in order and skips what it already has', () => {
    const jobs: Record<string, JobView> = {}
    applyJobEvent(jobs, ev(1, 'agent.job.started', { node_id: 'n', job_id: 'j', kind: 'login', agent: 'codex', command: 'codex login' }))
    applyJobEvent(jobs, ev(0, 'agent.job.output', { node_id: 'n', job_id: 'j', offset: 0, data: '你好' }))
    applyJobEvent(jobs, ev(0, 'agent.job.output', { node_id: 'n', job_id: 'j', offset: 0, data: '你好' }))
    applyJobEvent(jobs, ev(0, 'agent.job.output', { node_id: 'n', job_id: 'j', offset: 6, data: ' world' }))
    expect(jobs.j.data).toBe('你好 world')
    expect(jobs.j.end).toBe(12)
    applyJobEvent(jobs, ev(2, 'agent.job.ended', { node_id: 'n', job_id: 'j', exit_code: 1, error: 'exit status 1' }))
    expect(jobs.j.running).toBe(false)
    expect(jobs.j.exit_code).toBe(1)
    expect(latestJob(jobs, 'n', 'codex')?.job_id).toBe('j')
  })

  it('takes the tail when it covers more', () => {
    const jobs: Record<string, JobView> = {}
    applyJobEvent(jobs, ev(0, 'agent.job.output', { node_id: 'n', job_id: 'j', offset: 10, data: 'later' }))
    mergeJobTail(jobs, 'n', { job_id: 'j', kind: 'install', agent: 'a', command: 'x', offset: 0, data: '0123456789later', running: true })
    expect(jobs.j.data).toBe('0123456789later')
    expect(jobs.j.epoch).toBe(1)
    applyJobEvent(jobs, ev(0, 'agent.job.output', { node_id: 'n', job_id: 'j', offset: 15, data: '!' }))
    expect(jobs.j.data).toBe('0123456789later!')
  })

  it('reloads machines when a job ends, without touching other state', () => {
    const s = emptyState()
    applyEvent(s, ev(0, 'agent.job.output', { node_id: 'n', job_id: 'j', offset: 0, data: 'x' }))
    expect(s.nodesRev).toBe(0)
    applyEvent(s, ev(1, 'agent.job.ended', { node_id: 'n', job_id: 'j' }))
    expect(s.nodesRev).toBe(1)
  })

  it('finds links broken at the terminal width, and device codes', () => {
    const url = 'https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c&state=abcdef'
    const cols = 30
    const wrapped = [url.slice(0, 30), url.slice(30, 60), url.slice(60)].join('\r\n')
    const out = `\x1b[1mBrowser didn't open? Use the url below\x1b[0m\r\n${wrapped}\r\n\r\nPaste code here if prompted >`
    expect(extractLinks(out, cols).urls).toEqual([url])
    const dev = 'Open https://auth.openai.com/codex/device\nand enter this code: ABCD-12EF.'
    expect(extractLinks(dev, 80)).toEqual({ urls: ['https://auth.openai.com/codex/device'], codes: ['ABCD-12EF'] })
    // Kiro: a code of letters only, also repeated inside the URL.
    const kiro = 'Code: WQXD-ZDRP\r\nOpen this URL: https://view.awsapps.com/start/#/device?user_code=WQXD-ZDRP\r\nWARN-INFO done'
    expect(extractLinks(kiro, 80)).toEqual({ urls: ['https://view.awsapps.com/start/#/device?user_code=WQXD-ZDRP'], codes: ['WQXD-ZDRP'] })
  })

  it('strips terminal escapes', () => {
    expect(stripAnsi('\x1b[32mok\x1b[0m\x1b]0;title\x07 done\rx')).toBe('ok done\nx')
  })
})
