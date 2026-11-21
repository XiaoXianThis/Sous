import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import { cloudWorkDescription } from '../lib/cloud-work-state'
import { unb64 } from '../trust/bytes'
import type { CloudCapacityPhase, CloudWork, Task } from '../lib/types'

const isActive = (job: CloudWork | null) => job != null && ['queued', 'running', 'cancel_requested'].includes(job.status)
const maxBytes = 1024 ** 2

function standardBase64(bytes: Uint8Array) {
  let encoded = ''
  for (let i = 0; i < bytes.length; i += 8192) encoded += String.fromCharCode(...bytes.subarray(i, i + 8192))
  return btoa(encoded)
}

export default function CloudWorkPanel({ task }: { task: Task }) {
  const [job, setJob] = useState<CloudWork | null>(null)
  const [phase, setPhase] = useState<CloudCapacityPhase>()
  const [loaded, setLoaded] = useState(false)
  const [script, setScript] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [base, setBase] = useState('')
  const [sources, setSources] = useState<CloudWork[]>([])
  const [result, setResult] = useState<{ id: string; files: Record<string, string> }>()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [readError, setReadError] = useState('')
  const seq = useRef(0)
  const submitting = useRef(false)
  const grantID = useRef('')
  const active = isActive(job)
  const canStart = ['inbox', 'planned'].includes(task.status) && !task.node_pref && !!task.project_id
  const total = files.reduce((sum, file) => sum + file.size, 0)

  useEffect(() => {
    let live = true
    let timer: ReturnType<typeof setTimeout>
    async function refresh() {
      if (!live || submitting.current || document.hidden) return
      const rev = ++seq.current
      let delay = 10000
      try {
        const value = await api.loadCloudWork(task.id)
        if (live && seq.current === rev) { setJob(value.job); setPhase(value.capacity_phase); setReadError(''); setLoaded(true) }
        if (isActive(value.job)) delay = 2000
      } catch (e) { if (live && seq.current === rev) setReadError((e as Error).message); delay = 5000 }
      if (live && seq.current === rev) timer = setTimeout(refresh, delay)
    }
    function visibility() { clearTimeout(timer); void refresh() }
    document.addEventListener('visibilitychange', visibility)
    void refresh()
    return () => { live = false; clearTimeout(timer); seq.current++; document.removeEventListener('visibilitychange', visibility) }
  }, [task.id, active, busy])

  async function edit() {
    setError(''); setOpen(v => !v)
    if (open) return
    try { setSources((await api.projectCloudResults(task.project_id)).results) }
    catch (e) { setError((e as Error).message) }
  }

  async function mutate(fn: () => Promise<void>) {
    if (submitting.current) return
    submitting.current = true; seq.current++; setBusy(true); setError('')
    try { await fn() } catch (e) { setError((e as Error).message) }
    finally { submitting.current = false; setBusy(false) }
  }

  function submit() {
    void mutate(async () => {
      if (!script.trim() || new TextEncoder().encode(script).length > 8192 || (!base && (!files.length || files.length > 64 || total > maxBytes))) {
        throw new Error('请选择最多 64 个、合计不超过 1 MiB 的文件，或选用已保存成果。命令最多 8 KiB。')
      }
      const entries = new Map<string, string>()
      if (!base) for (const file of files) {
        if (entries.has(file.name)) throw new Error('上传材料不能包含同名文件。')
        entries.set(file.name, standardBase64(new Uint8Array(await file.arrayBuffer())))
      }
      grantID.current ||= crypto.randomUUID()
      const response = await api.requestCloudWork(task.id, {
        request_id: grantID.current, task_revision: task.updated_at, script,
        ...(base ? { base_job_id: base } : { files: Object.fromEntries(entries) }),
      })
      setJob(response.job); setPhase(undefined); setOpen(false); setResult(undefined)
      setScript(''); setFiles([]); setBase(''); grantID.current = ''
    })
  }

  function download(bytes: string, name: string) {
    const url = URL.createObjectURL(new Blob([unb64(bytes)], { type: 'application/octet-stream' }))
    const a = document.createElement('a')
    a.href = url; a.download = name.split('/').at(-1) || 'sous-result'; a.click()
    setTimeout(() => URL.revokeObjectURL(url), 30000)
  }

  return <section aria-label="云工作机" className="rounded-xl bg-sunken px-3.5 py-3 grid gap-3">
    <div className="flex flex-wrap items-start justify-between gap-2">
      <div className="min-w-0">
        <h2 className="m-0 text-[14px] font-semibold">云工作机</h2>
        <p role="status" className="m-0 mt-1 text-[13px] text-muted leading-relaxed">
          {job ? cloudWorkDescription(job.status, phase) : loaded ? '只在你明确授权后运行，不读取个人电脑。' : '正在查询云执行状态…'}
        </p>
        {job != null && job.exit_code >= 0 && <p className="m-0 text-[12px] text-muted">退出码 {job.exit_code} · {job.files} 个文件</p>}
      </div>
      {loaded && !active && canStart && <button type="button" className="btn" aria-expanded={open} disabled={busy} onClick={() => void edit()}>{open ? '收起' : job ? '重新提交' : '授权云执行'}</button>}
      {(job?.status === 'queued' || job?.status === 'running') && <button type="button" className="btn" disabled={busy} onClick={() => void mutate(async () => { setJob((await api.cancelCloudWork(job.id)).job) })}>请求停止</button>}
    </div>
    {open && canStart && !active && <form className="grid gap-2.5" onSubmit={e => { e.preventDefault(); submit() }}>
      <fieldset className="border-0 m-0 p-0 min-w-0 grid gap-2.5" disabled={busy}>
        <label className="grid gap-1 text-[13px]">起始材料
          <select className="input max-w-full" value={base} onChange={e => { grantID.current = ''; setBase(e.currentTarget.value) }}>
            <option value="">上传文件副本</option>
            {sources.map(x => <option key={x.id} value={x.id}>{new Date(x.created_at).toLocaleString()} · {x.files} 个文件 · {x.id.slice(-6)}</option>)}
          </select>
        </label>
        {!base && <label className="grid gap-1 text-[13px]">本次上传的文件
          <input type="file" multiple aria-label="本次上传的文件" className="max-w-full" onChange={e => { grantID.current = ''; setFiles(Array.from(e.currentTarget.files ?? [])) }} />
          {files.length > 0 && <span className="text-[12px] text-muted break-all">{files.map(f => f.name).join('、')} · {Math.ceil(total / 1024)} KiB</span>}
        </label>}
        <label className="grid gap-1 text-[13px]">Guest 命令
          <textarea aria-label="云 Guest 命令" className="input min-h-[88px] font-mono" value={script} onChange={e => { grantID.current = ''; setScript(e.currentTarget.value) }} />
        </label>
        <p className="m-0 text-[12px] text-muted">点击确认将把上述材料和命令交给此云工作区。Guest 无网络；任务说明不会自动变成命令。</p>
        <button type="submit" className="btn justify-self-start" disabled={!script.trim() || (!base && !files.length)}>{busy ? '正在提交…' : '确认并排队'}</button>
      </fieldset>
    </form>}
    {job != null && job.files > 0 && <div className="grid justify-items-start gap-1.5">
      {result?.id !== job.id ? <button className="link" disabled={busy} onClick={() => void mutate(async () => {
        const files = (await api.cloudWorkResults(job.id)).files
        setResult({ id: job.id, files: files ?? {} })
      })}>查看成果文件</button> : Object.entries(result.files).map(([name, bytes]) =>
        <button key={name} className="link break-all text-left" onClick={() => download(bytes, name)}>下载 {name}</button>)}
    </div>}
    {(error || readError) && <p role="alert" className="m-0 text-[13px] text-danger">{error || readError}</p>}
  </section>
}
