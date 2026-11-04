// Agent jobs on a machine (install, upgrade, login, logout): their terminal
// output, and the links and codes in it. Pure functions, unit tested.
import type { HubEvent, JobKind, JobTail } from './types'

export interface JobView {
  node_id: string
  job_id: string
  kind: JobKind | ''
  agent: string
  command: string
  /** Terminal output, oldest bytes dropped past MAX_JOB_TEXT. */
  data: string
  /** Byte offset where the next chunk should start. */
  end: number
  /** Bumped when data was replaced rather than appended (the terminal resets). */
  epoch: number
  running: boolean
  exit_code?: number
  error?: string
}

export const MAX_JOB_TEXT = 256 * 1024

const enc = new TextEncoder()
const bytes = (s: string) => enc.encode(s).length

function view(jobs: Record<string, JobView>, nodeId: string, jobId: string): JobView {
  if (!jobs[jobId]) {
    jobs[jobId] = { node_id: nodeId, job_id: jobId, kind: '', agent: '', command: '', data: '', end: 0, epoch: 0, running: true }
  }
  return jobs[jobId]
}

function trim(j: JobView) {
  if (j.data.length > MAX_JOB_TEXT) {
    j.data = j.data.slice(j.data.length - MAX_JOB_TEXT)
    j.epoch++
  }
}

/** Applies an agent.job.* event. Returns false for other events. */
export function applyJobEvent(jobs: Record<string, JobView>, ev: HubEvent): boolean {
  if (!ev.type.startsWith('agent.job.')) return false
  const p = ev.payload ?? {}
  if (!p.job_id) return true
  const j = view(jobs, p.node_id, p.job_id)
  switch (ev.type) {
    case 'agent.job.started':
      j.kind = p.kind
      j.agent = p.agent
      j.command = p.command ?? ''
      break
    case 'agent.job.output': {
      const data: string = p.data ?? ''
      const off: number = p.offset ?? 0
      const n = bytes(data)
      if (off + n <= j.end) break // already have it (from the tail)
      if (off < j.end) {
        // Overlaps what we have: keep the new part only.
        const skip = j.end - off
        const b = enc.encode(data).slice(skip)
        j.data += new TextDecoder().decode(b)
      } else {
        j.data += data
      }
      j.end = off + n
      trim(j)
      break
    }
    case 'agent.job.ended':
      j.running = false
      j.exit_code = p.exit_code ?? 0
      j.error = p.error || undefined
      if (!j.kind) j.kind = p.kind
      if (!j.agent) j.agent = p.agent
      break
  }
  return true
}

/** Merges what job.tail returned with what the stream already delivered. */
export function mergeJobTail(jobs: Record<string, JobView>, nodeId: string, t: JobTail) {
  const j = view(jobs, nodeId, t.job_id)
  j.kind = t.kind
  j.agent = t.agent
  j.command = t.command
  const tailEnd = t.offset + bytes(t.data)
  const ours = enc.encode(j.data)
  const ourStart = j.end - ours.length
  if (tailEnd >= j.end) {
    if (t.offset < ourStart || tailEnd > j.end || j.data === '') {
      j.data = t.data
      j.end = tailEnd
      j.epoch++
    }
  } else if (t.offset < ourStart) {
    // The tail has the beginning, the stream has the newest part.
    j.data = t.data + new TextDecoder().decode(ours.slice(Math.max(0, tailEnd - ourStart)))
    j.epoch++
  }
  if (!t.running) {
    j.running = false
    j.exit_code = t.exit_code ?? j.exit_code
  }
}

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g

/** Removes terminal escape sequences and carriage returns. */
export function stripAnsi(s: string): string {
  return s.replace(ANSI, '').replace(/\r(?!\n)/g, '\n').replace(/\r/g, '')
}

const URL_RE = /https?:\/\/[^\s"'<>`]+/g
// Device codes like ABCD-1234 or WQXD-ZDRP (capitals and digits, one dash).
// One without digits counts only on a line that talks about a code.
const CODE_RE = /\b[A-Z0-9]{4,5}-[A-Z0-9]{4,5}\b/g
const CODE_LINE = /code|验证码|授权码/i

/**
 * Finds the links and device codes in a job's output. A terminal program
 * breaks a long URL across lines at the terminal's width; a URL that
 * fills its line to the edge continues on the next one.
 */
export function extractLinks(raw: string, cols: number): { urls: string[]; codes: string[] } {
  const lines = stripAnsi(raw).split('\n')
  const urls: string[] = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    for (const m of line.matchAll(URL_RE)) {
      let url = m[0]
      let end = (m.index ?? 0) + url.length
      let j = i
      while (cols > 0 && end >= cols - 1 && end === lines[j].trimEnd().length && j + 1 < lines.length) {
        const next = lines[j + 1].match(/^[^\s"'<>`]+/)
        if (!next) break
        url += next[0]
        j++
        end = next[0].length
        if (next[0].length !== lines[j].trimEnd().length) break
      }
      url = url.replace(/[).,;:!?\]]+$/, '')
      if (!urls.includes(url)) urls.push(url)
    }
  }
  const codes: string[] = []
  for (const line of lines) {
    const text = line.replace(URL_RE, ' ')
    for (const m of text.matchAll(CODE_RE)) {
      const c = m[0]
      if (codes.includes(c) || !/[A-Z]/.test(c)) continue
      if (/[0-9]/.test(c) || CODE_LINE.test(line)) codes.push(c)
    }
  }
  return { urls, codes }
}

/** The last job of an agent on a machine, if any. */
export function latestJob(jobs: Record<string, JobView>, nodeId: string, agent: string): JobView | undefined {
  let found: JobView | undefined
  for (const id of Object.keys(jobs).sort()) {
    const j = jobs[id]
    if (j.node_id === nodeId && j.agent === agent) found = j
  }
  return found
}

export const jobTitle: Record<string, string> = { install: '安装', upgrade: '升级', login: '登录', logout: '退出登录' }
