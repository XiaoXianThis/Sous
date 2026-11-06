import { useState } from 'react'
import { api } from '../lib/api'
import { go, loadOverview, useWb } from '../lib/app'
import { setArchived } from '../lib/projects'
import { modeText } from '../lib/tasks'
import { isArchived } from '../lib/workbench'
import type { Deploy, Project, ProjectNode } from '../lib/types'
import Sheet from './Sheet'

export default function ProjectEditor({ project = null, preset = null, onClose }: {
  project?: Project | null
  /** A new project from a discovered repository. */
  preset?: { name: string; repo_url: string; node_id?: string; path?: string } | null
  onClose: () => void
}) {
  const wb = useWb()
  const isNew = !project
  const [name, setName] = useState(project?.name ?? preset?.name ?? '')
  const [repo, setRepo] = useState(project?.repo_url ?? preset?.repo_url ?? '')
  const [notes, setNotes] = useState(project?.notes ?? '')
  const [persona, setPersona] = useState(project?.default_persona ?? '')
  const [mode, setMode] = useState<string>(project?.default_mode ?? '')
  const [places, setPlaces] = useState<ProjectNode[]>(
    project
      ? project.nodes.map((n) => ({ ...n }))
      : preset?.node_id && preset.path && !preset.repo_url
        ? [{ node_id: preset.node_id, path: preset.path, priority: 0 }]
        : [],
  )
  const [deploy, setDeploy] = useState<Deploy>(project?.deploy ? { ...project.deploy } : { node_id: '', path: '', command: '', health: '', branch: '', auto_on_merge: false })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const nodes = wb.data?.nodes ?? []
  const personas = wb.data?.personas ?? []
  const servers = nodes.filter((n) => n.role === 'server')

  function addPlace() {
    const free = nodes.find((n) => !places.some((p) => p.node_id === n.id))
    if (free) setPlaces([...places, { node_id: free.id, path: '', priority: places.length, source: 'manual' }])
  }

  async function save() {
    setBusy(true)
    setError('')
    try {
      const body: Partial<Project> = {
        name, repo_url: repo, notes, default_persona: persona, default_mode: mode as Project['default_mode'],
        nodes: places.filter((p) => p.path.trim()),
        deploy: deploy.node_id ? { ...deploy, branch: deploy.branch?.trim() ?? '', auto_on_merge: !!deploy.auto_on_merge && !!deploy.branch?.trim() } : null,
      }
      const p = isNew ? await api.createProject(body) : await api.updateProject(project!.id, body)
      await loadOverview()
      onClose()
      if (isNew) go({ view: 'project', id: p.id })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!project || !confirm(`删除项目“${project.name}”？机器上的代码不会被删除。`)) return
    try {
      await api.deleteProject(project.id)
      await loadOverview()
      onClose()
      go({ view: 'projects', id: '' })
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <Sheet title={isNew ? '登记项目' : `项目设置 · ${project?.name}`} onClose={onClose} wide>
      <form className="grid gap-2.5" onSubmit={(e) => { e.preventDefault(); void save() }}>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-2.5">
          <label className="grid gap-1 text-[13px] text-muted">
            名字
            <input
              className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              required
            />
          </label>
          <label className="grid gap-1 text-[13px] text-muted">
            仓库地址
            <input
              className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
              value={repo}
              onChange={(e) => setRepo(e.currentTarget.value)}
              placeholder="https://github.com/you/app"
            />
          </label>
        </div>
        <p className="m-0 text-[12px] text-muted">同一个仓库在各台机器的工作空间里会被自动找到，不用手填路径。</p>
        <label className="grid gap-1 text-[13px] text-muted">
          项目说明（会写进 agent 的提示）
          <textarea
            className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
            rows={2}
            value={notes}
            onChange={(e) => setNotes(e.currentTarget.value)}
          ></textarea>
        </label>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-2.5">
          <label className="grid gap-1 text-[13px] text-muted">
            默认岗位
            <select
              className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
              value={persona}
              onChange={(e) => setPersona(e.currentTarget.value)}
            >
              <option value="">不指定</option>
              {personas.map((p) => <option key={p.id} value={p.id}>{p.glyph} {p.name}</option>)}
            </select>
          </label>
          <label className="grid gap-1 text-[13px] text-muted">
            权限模式
            <select
              className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
              value={mode}
              onChange={(e) => setMode(e.currentTarget.value)}
            >
              <option value="">按全局默认</option>
              {Object.entries(modeText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </label>
        </div>

        <fieldset className="border border-line rounded-[14px] px-3 pt-2 pb-3 grid gap-2">
          <legend className="text-[13px] text-muted px-1">在哪些机器上（越靠前越优先）</legend>
          {places.map((p, i) => (
            <div key={i} className="flex gap-1.5 items-center">
              <select
                className="flex-none w-[140px] border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                value={p.node_id}
                onChange={(e) => setPlaces(places.map((x, j) => (j === i ? { ...x, node_id: e.currentTarget.value } : x)))}
                aria-label="机器"
              >
                {nodes.map((n) => <option key={n.id} value={n.id}>{n.name}{n.role === 'server' ? '（服务器）' : ''}</option>)}
              </select>
              <input
                className="flex-1 border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                value={p.path}
                onChange={(e) => setPlaces(places.map((x, j) => (j === i ? { ...x, path: e.currentTarget.value } : x)))}
                placeholder="仓库在这台机器上的路径"
                aria-label="路径"
              />
              {p.source === 'scan' && <span className="text-[11px] text-muted border border-line rounded-[8px] px-1" title="在工作空间里自动找到">自动</span>}
              <button type="button" className="link" onClick={() => setPlaces(places.filter((_, j) => j !== i))}>移除</button>
            </div>
          ))}
          {nodes.length > places.length && <button type="button" className="link accent justify-self-start" onClick={addPlace}>添加机器</button>}
        </fieldset>

        <fieldset className="border border-line rounded-[14px] px-3 pt-2 pb-3 grid gap-2">
          <legend className="text-[13px] text-muted px-1">部署目标</legend>
          {servers.length === 0 ? (
            <p className="m-0 text-[12px] text-muted">还没有服务器。在“设置 → 机器”里把一台机器设为服务器后，就能在这里选它部署。</p>
          ) : (
            <>
              <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-2.5">
                <label className="grid gap-1 text-[13px] text-muted">
                  服务器
                  <select
                    className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                    value={deploy.node_id}
                    onChange={(e) => setDeploy({ ...deploy, node_id: e.currentTarget.value })}
                  >
                    <option value="">不部署</option>
                    {servers.map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
                  </select>
                </label>
                {deploy.node_id && (
                  <label className="grid gap-1 text-[13px] text-muted">
                    目录
                    <input
                      className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                      value={deploy.path}
                      onChange={(e) => setDeploy({ ...deploy, path: e.currentTarget.value })}
                      placeholder="/srv/app"
                    />
                  </label>
                )}
              </div>
              {deploy.node_id && (
                <>
                  <div className="grid grid-cols-[repeat(auto-fit,minmax(200px,1fr))] gap-2.5">
                    <label className="grid gap-1 text-[13px] text-muted">
                      部署命令
                      <input
                        className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                        value={deploy.command}
                        onChange={(e) => setDeploy({ ...deploy, command: e.currentTarget.value })}
                        placeholder="./deploy.sh"
                      />
                    </label>
                    <label className="grid gap-1 text-[13px] text-muted">
                      健康检查（可选）
                      <input
                        className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                        value={deploy.health ?? ''}
                        onChange={(e) => setDeploy({ ...deploy, health: e.currentTarget.value })}
                        placeholder="curl -fsS localhost:8080/health"
                      />
                    </label>
                  </div>
                  <label className="grid gap-1 text-[13px] text-muted">
                    部署分支
                    <input
                      className="border border-line bg-page rounded-[10px] px-2.5 py-1.5 text-ink min-w-0"
                      value={deploy.branch ?? ''}
                      onChange={(e) => setDeploy({ ...deploy, branch: e.currentTarget.value })}
                      placeholder="origin 上要部署的分支"
                    />
                  </label>
                  <label className="flex gap-1.5 items-center text-ink">
                    <input
                      type="checkbox"
                      checked={deploy.auto_on_merge ?? false}
                      disabled={!deploy.branch?.trim()}
                      onChange={(e) => setDeploy({ ...deploy, auto_on_merge: e.currentTarget.checked })}
                    />部署分支有新提交时自动部署
                  </label>
                </>
              )}
            </>
          )}
        </fieldset>

        {error && <p className="m-0 text-danger text-[13px]" role="alert">{error}</p>}
        <div className="flex gap-3.5 items-center">
          <button className="btn small px-4" type="submit" disabled={busy || !name.trim()}>保存</button>
          {!isNew && project && project.id !== 'misc' && (
            <button type="button" className="link" onClick={() => void setArchived(project, !isArchived(project)).then(onClose)}>
              {isArchived(project) ? '激活项目' : '归档项目'}
            </button>
          )}
          {!isNew && project?.id !== 'misc' && <button type="button" className="link danger" onClick={() => void remove()}>删除项目</button>}
        </div>
      </form>
    </Sheet>
  )
}
