import { useState } from 'react'
import { api } from '../lib/api'
import { go, loadOverview, ui, useWb } from '../lib/app'
import type { RepoScan } from '../lib/types'
import Icon from './Icon'
import ProjectEditor from './ProjectEditor'
import Sheet from './Sheet'

export default function ProjectSetupSheet({ onClose }: { onClose: () => void }) {
  const wb = useWb()
  const [name, setName] = useState('')
  const [nodeId, setNodeId] = useState('')
  const [layout, setLayout] = useState<'wokspace' | 'plain'>('wokspace')
  const [owner, setOwner] = useState('')
  const [isPrivate, setIsPrivate] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [editor, setEditor] = useState<null | { name: string; repo_url: string; node_id?: string; path?: string }>(null)

  const devs = (wb.data?.nodes ?? []).filter((n) => n.role !== 'server' && n.online && (n.workspaces?.length ?? 0) > 0)
  const discovered = wb.data?.discovered ?? []
  const validName = /^[a-z0-9]+(-[a-z0-9]+)*$/.test(name)
  const nodeName = (id: string) => wb.data?.nodes.find((n) => n.id === id)?.name ?? '未知机器'
  const repoName = (r: RepoScan) => r.path.split(/[\\/]/).pop() ?? r.path

  async function create() {
    if (!validName) return
    setBusy(true)
    setError('')
    try {
      const r = await api.newProject({
        name, node_id: nodeId || undefined, layout, owner: owner.trim() || undefined, private: isPrivate,
      })
      await loadOverview()
      ui.state.chatOpen = true
      ui.emit()
      onClose()
      go({ view: 'project', id: r.project.id })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Sheet title="新建或登记项目" onClose={onClose} wide>
        <section className="grid gap-2">
          <h3 className="mt-1 mb-0 text-[15px]">新建项目</h3>
          <p className="m-0 text-[13px] text-muted">
            在一台开发机的工作空间里创建。默认使用 <a className="text-accent" href="https://github.com/XiaoXianThis/wokspace" target="_blank" rel="noreferrer">wokspace</a> 规范。
          </p>
          <form onSubmit={(e) => { e.preventDefault(); void create() }}>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-2.5">
              <label className="grid gap-1 text-[13px] text-muted">
                项目名
                <input
                  className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                  value={name}
                  onChange={(e) => setName(e.currentTarget.value)}
                  placeholder="my-app"
                  aria-invalid={!!name && !validName}
                />
              </label>
              <label className="grid gap-1 text-[13px] text-muted">
                机器
                <select
                  className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                  value={nodeId}
                  onChange={(e) => setNodeId(e.currentTarget.value)}
                >
                  <option value="">自动选一台</option>
                  {devs.map((n) => <option key={n.id} value={n.id}>{n.name}{n.wok ? '' : '（没装 wok）'}</option>)}
                </select>
              </label>
              <label className="grid gap-1 text-[13px] text-muted">
                结构
                <select
                  className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                  value={layout}
                  onChange={(e) => setLayout(e.currentTarget.value as 'wokspace' | 'plain')}
                >
                  <option value="wokspace">wokspace</option>
                  <option value="plain">只 git init</option>
                </select>
              </label>
              <label className="grid gap-1 text-[13px] text-muted">
                GitHub 用户或组织（可选）
                <input
                  className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                  value={owner}
                  onChange={(e) => setOwner(e.currentTarget.value)}
                  placeholder="不填就不建远端仓库"
                />
              </label>
            </div>
            {owner.trim() && (
              <label className="flex gap-1.5 items-center text-ink">
                <input type="checkbox" checked={isPrivate} onChange={(e) => setIsPrivate(e.currentTarget.checked)} />私有仓库
              </label>
            )}
            {name && !validName && <p className="text-danger text-[13px] m-0">只能用小写字母、数字和连字符，比如 my-app</p>}
            {devs.length === 0 && <p className="text-danger text-[13px] m-0">没有在线并设置了工作空间的开发机：先在“设置 → 机器”里给一台机器设置工作空间。</p>}
            {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
            <div><button className="btn small justify-self-start" type="submit" disabled={busy || !validName}>创建</button></div>
          </form>
        </section>

        <section className="grid gap-2">
          <h3 className="mt-1 mb-0 text-[15px]">登记已有仓库</h3>
          <p className="m-0 text-[13px] text-muted">项目已经在这台机器上时，登记它的远端地址或本机路径即可，不需要新建。</p>
          <button className="btn small ghost justify-self-start" onClick={() => setEditor({ name: '', repo_url: '' })}>手动登记</button>
          {discovered.length > 0 ? (
            <div className="grid gap-1.5">
              {discovered.map((r) => (
                <div key={r.node_id + r.path} className="grid grid-cols-[minmax(90px,1fr)_minmax(0,1.6fr)_minmax(0,1.6fr)_auto] gap-2.5 items-center border border-line rounded-[12px] px-2.5 py-[7px] bg-panel max-[760px]:grid-cols-1 max-[760px]:gap-0.5">
                  <span className="flex gap-[5px] items-center min-w-0">
                    <strong>{repoName(r)}</strong>
                    {r.wokspace && <span className="text-muted inline-grid" title="wokspace"><Icon name="wok" size={12} /></span>}
                  </span>
                  <span className="text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap inline-flex gap-1 items-center" title={r.path}>
                    <Icon name="laptop" size={12} />{nodeName(r.node_id)} · {r.path}
                  </span>
                  <span className="text-[12px] text-muted overflow-hidden text-ellipsis whitespace-nowrap inline-flex gap-1 items-center">{r.remote || '没有远端'}</span>
                  <button className="link accent" onClick={() => setEditor({ name: repoName(r), repo_url: r.remote, node_id: r.node_id, path: r.path })}>登记</button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-muted text-[13px] m-0">机器上还没有发现未登记的仓库。</p>
          )}
        </section>
      </Sheet>

      {editor && <ProjectEditor preset={editor} onClose={() => setEditor(null)} />}
    </>
  )
}
