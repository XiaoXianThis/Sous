import { useEffect, useState } from 'react'
import { request } from '../lib/api'

const base = '/api/v1/external-mcp/'

type Preview = { client: string; callback: string; requests_drafts: boolean; projects: { id: string; name: string }[] }
type Grant = { id: string; client: string; project: string; allow_drafts: boolean; expires_at: number; active: boolean }

export function ExternalMCPConsent({ authorization }: { authorization: string }) {
  const [preview, setPreview] = useState<Preview>()
  const [project, setProject] = useState('')
  const [drafts, setDrafts] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [attempt, retry] = useState(0)
  useEffect(() => {
    let live = true
    setError('')
    void request<Preview>('GET', `${base}preview?${new URLSearchParams({ request: authorization })}`)
      .then(value => { if (live) setPreview(value) })
      .catch(e => { if (live) setError((e as Error).message) })
    return () => { live = false }
  }, [authorization, attempt])

  async function decide(decision: 'allow' | 'deny') {
    if (busy || !preview) return
    setBusy(true); setError('')
    try {
      const { redirect_to } = await request<{ redirect_to: string }>('POST', `${base}decision`, {
        request: authorization, decision, project_id: project, allow_drafts: drafts,
      })
      window.location.replace(redirect_to)
    } catch (e) {
      setError((e as Error).message); setBusy(false)
    }
  }

  return <main className="min-h-full overflow-auto px-5 py-10 grid justify-items-center content-start">
    <div className="w-full max-w-[460px] grid gap-4">
      <img src="/icon.svg" alt="Sous" width={48} height={48} className="rounded-xl" />
      <h1 className="text-2xl font-semibold m-0">允许外部客户端连接？</h1>
      {preview ? <>
        <p className="text-sm m-0"><strong>{preview.client}</strong> 请求访问此云工作区的一个项目。只在你信任该客户端时继续。</p>
        <p className="break-all text-[13px] text-muted m-0">授权后返回：{preview.callback}</p>
        <label className="grid gap-2 text-sm">授权项目
          <select className="input w-full" value={project} disabled={busy} onChange={e => setProject(e.target.value)}>
            <option value="">请选择一个项目</option>
            {preview.projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </label>
        {!preview.projects.length && <p className="text-sm text-muted m-0">没有可授权的项目。请先在 Sous 中新建项目。</p>}
        <div className="rounded-xl bg-sunken p-3 grid gap-2 text-sm">
          <p className="m-0">可读所选项目的名称、任务标题、状态和任务说明。</p>
          {preview.requests_drafts && <label className="flex items-start gap-2"><input type="checkbox" checked={drafts} disabled={busy} onChange={e => setDrafts(e.target.checked)} />允许创建任务草稿</label>}
          <p className="text-muted text-[13px] m-0">不会授权记忆、对话、文件、审批或派发。授权最长 7 天，可随时撤销；撤销不会收回已经读取的数据。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button className="btn" disabled={busy || !project} onClick={() => void decide('allow')}>{busy ? '正在处理…' : '允许连接'}</button>
          <button className="btn" disabled={busy} onClick={() => void decide('deny')}>拒绝</button>
        </div>
      </> : !error && <p role="status" className="m-0 text-sm text-muted">正在验证授权请求…</p>}
      {error && <p role="alert" className="m-0 text-sm text-danger">{error}</p>}
      {error && !preview && <button className="btn justify-self-start" onClick={() => retry(v => v + 1)}>重试</button>}
      <a href="/" className="link text-sm justify-self-start">返回 Sous 工作区</a>
    </div>
  </main>
}

export function ExternalMCPConnections() {
  const [data, setData] = useState<{ endpoint: string; grants: Grant[] }>()
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState('')
  const [attempt, retry] = useState(0)
  useEffect(() => {
    let live = true
    setError('')
    void request<{ endpoint: string; grants: Grant[] }>('GET', `${base}grants`)
      .then(value => { if (live) setData(value) })
      .catch(e => { if (live) setError((e as Error).message) })
    return () => { live = false }
  }, [attempt])

  async function revoke(g: Grant) {
    setBusy(g.id); setError(''); setNotice('')
    try {
      await request('DELETE', `${base}grants/${encodeURIComponent(g.id)}`)
      setData(last => last && { ...last, grants: last.grants.filter(item => item.id !== g.id) })
      setNotice(`已撤销 ${g.client} 对「${g.project}」的授权。`)
    } catch (e) { setError((e as Error).message) }
    finally { setBusy('') }
  }

  return <section aria-label="外部连接" className="grid gap-3 p-3 max-w-[560px]">
    <h3 className="m-0 text-base font-semibold">连接外部 MCP 客户端</h3>
    <p className="m-0 text-sm text-muted">在兼容且已注册的客户端中添加此地址，随后在 Sous 中选择要授权的项目。</p>
    {data && <>
      <code className="break-all rounded-lg bg-sunken p-2 text-[13px] select-all">{data.endpoint}</code>
      {!data.grants.length ? <p className="m-0 text-sm text-muted">暂无外部连接。</p> :
        <ul className="list-none p-0 m-0 grid gap-3">
          {data.grants.map(g => <li key={g.id} className="flex justify-between items-start gap-2">
            <div className="min-w-0 grid gap-1">
              <strong className="text-sm break-words">{g.client} · {g.project}</strong>
              <span className="text-[13px] text-muted">{g.active ? (g.allow_drafts ? '可读任务、创建草稿' : '只读') : '暂不可用'} · 到期 {new Date(g.expires_at).toLocaleString()}</span>
            </div>
            <button className="btn shrink-0" disabled={!!busy} onClick={() => void revoke(g)} aria-label={`撤销 ${g.client} 对 ${g.project} 的授权`}>{busy === g.id ? '正在撤销…' : '撤销'}</button>
          </li>)}
        </ul>}
    </>}
    {!data && !error && <p role="status" className="m-0 text-sm text-muted">正在读取外部连接…</p>}
    {notice && <p role="status" className="m-0 text-sm">{notice}</p>}
    {error && <p role="alert" className="m-0 text-sm text-danger">{error}</p>}
    {!data && error && <button className="btn justify-self-start" onClick={() => retry(v => v + 1)}>重试</button>}
  </section>
}
