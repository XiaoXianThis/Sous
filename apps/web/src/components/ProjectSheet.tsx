import { useState } from 'react'
import { api } from '../lib/api'
import { go, loadOverview, ui, useWb } from '../lib/app'
import { projectCards } from '../lib/workbench'
import AgentCard from './AgentCard'
import ApprovalCard from './ApprovalCard'
import Icon from './Icon'
import Locations from './Locations'
import ProjectEditor from './ProjectEditor'
import ProjectTile from './ProjectTile'
import Sheet from './Sheet'
import TaskRow from './TaskRow'

export default function ProjectSheet({ id, onClose }: { id: string; onClose: () => void }) {
  const wb = useWb()
  const [editing, setEditing] = useState(false)
  const [cloneTo, setCloneTo] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const project = wb.data?.projects.find((p) => p.id === id)
  const card = wb.data ? projectCards(wb.data, wb.inbox?.tasks ?? []).find((c) => c.project.id === id) : undefined
  const clonable = (wb.data?.nodes ?? []).filter(
    (n) => n.role !== 'server' && n.online && (n.workspaces?.length ?? 0) > 0 && !project?.nodes.some((p) => p.node_id === n.id),
  )
  const runIds = new Set(card?.allAgents.map((a) => a.run.id) ?? [])
  const approvals = (wb.inbox?.approvals ?? []).filter((a) => a.review === 'user' && runIds.has(a.run_id))
  const titleOf = (runId: string) => card?.allAgents.find((a) => a.run.id === runId)?.title ?? ''

  async function deploy() {
    if (!project?.deploy) return
    if (!confirm(`在服务器上部署“${project.name}”？\n\n会建一个运维任务，按部署命令执行；重启服务之类的操作会请你批准。`)) return
    setBusy(true)
    setError('')
    try {
      const ops = wb.data?.personas.find((p) => p.kinds.includes('ops'))
      const t = await api.createTask({
        kind: 'ops', project_id: id, title: `部署 ${project.name}`, persona_id: ops?.id ?? '',
        detail: `把项目「${project.name}」部署到服务器：先拉取最新代码，再执行部署命令，最后做健康检查。`, dispatch: true,
      })
      go({ view: 'task', id: t.id })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function clone() {
    if (!cloneTo) return
    setBusy(true)
    setError('')
    try {
      await api.cloneProject(id, cloneTo)
      setCloneTo('')
      await loadOverview()
      ui.state.chatOpen = true
      ui.emit()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const head = project ? (
    <div className="flex items-center gap-3 min-w-0">
      <ProjectTile name={project.name} id={project.id} size={46} misc={project.id === 'misc'} />
      <div className="grid gap-1 min-w-0">
        <h2 className="m-0 text-[19px] font-bold tracking-tight overflow-hidden text-ellipsis whitespace-nowrap">{project.name}</h2>
        {project.id !== 'misc' && <Locations locations={card?.locations ?? []} />}
      </div>
    </div>
  ) : undefined
  const sectionTitle = 'm-0 mb-1 text-[12px] font-semibold text-muted flex items-center gap-1.5'

  return (
    <>
      <Sheet title={project ? project.name : '项目'} onClose={onClose} wide head={head}>
      {!wb.data ? (
        <div className="h-24 rounded-[16px] bg-sunken animate-pulse"></div>
      ) : !project ? (
        <p className="text-muted text-[13px] m-0">这个项目不存在了。</p>
      ) : (
        <>
          {project.notes && <p className="m-0 text-[13px] text-muted [overflow-wrap:anywhere]">{project.notes}</p>}

          <div className="flex gap-1.5 flex-wrap items-center">
            <button className="btn small" onClick={() => { ui.state.newTask = project.id; ui.emit() }}><Icon name="plus" size={15} stroke={2.2} />新任务</button>
            {project.deploy && (
              <button className="btn small ghost" disabled={busy} onClick={() => void deploy()} title="sous 不会合并 PR；PR 合并后才部署">
                <Icon name="rocket" size={14} />部署
              </button>
            )}
            {project.id !== 'misc' && (
              <button className="icon-btn solid" aria-label="编辑项目" data-tip="编辑项目" data-tip-pos="bottom" disabled={busy} onClick={() => setEditing(true)}><Icon name="edit" size={16} /></button>
            )}
            {project.repo_url && (
              <a className="icon-btn solid" aria-label="仓库" data-tip={project.repo_url} data-tip-pos="bottom" href={project.repo_url.startsWith('http') ? project.repo_url : undefined} target="_blank" rel="noreferrer">
                <Icon name={project.repo_url.includes('github') ? 'github' : 'link'} size={16} />
              </a>
            )}
            {clonable.length > 0 && (
              <span className="inline-flex gap-1.5 items-center">
                <select
                  className="h-8 border-0 bg-panel shadow-soft rounded-[12px] pl-2.5 text-[13px] text-muted"
                  value={cloneTo}
                  onChange={(e) => setCloneTo(e.currentTarget.value)}
                  aria-label="clone 到"
                >
                  <option value="">clone 到…</option>
                  {clonable.map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
                </select>
                {cloneTo && <button className="btn small" disabled={busy} onClick={() => void clone()}><Icon name="download" size={14} /></button>}
              </span>
            )}
          </div>

          {error && <p className="m-0 text-danger text-[13px]" role="alert">{error}</p>}

          {approvals.length > 0 && (
            <section className="grid gap-2" aria-label="需要你处理">
              <h3 className={sectionTitle}><Icon name="hand" size={14} className="text-warm" />要你处理</h3>
              {approvals.map((a) => <ApprovalCard key={a.id} approval={a} context={titleOf(a.run_id)} />)}
            </section>
          )}

          {card && card.allAgents.length > 0 && (
            <section className="grid gap-2" aria-label="正在工作">
              <h3 className={sectionTitle}><Icon name="play" size={12} className="text-info" />正在工作</h3>
              {card.allAgents.map((a) => (
                <AgentCard key={a.run.id} agent={a} onOpen={() => { ui.state.runSheet = a.run.id; ui.emit() }} />
              ))}
            </section>
          )}

          <section className="grid gap-1" aria-label="任务">
            <h3 className={sectionTitle}><Icon name="list" size={14} />任务<span className="tabular-nums text-faint">{card?.allTasks.length ?? 0}</span></h3>
            {card?.allTasks.length ? (
              <div className="card p-1.5 grid">
                {card.allTasks.map((t) => <TaskRow key={t.task.id} task={t} onOpen={() => go({ view: 'task', id: t.task.id })} />)}
              </div>
            ) : (
              <button
                className="flex items-center justify-center gap-1.5 h-12 rounded-[14px] border border-dashed border-line-strong bg-transparent text-muted text-[13px] hover:text-accent hover:border-accent"
                onClick={() => { ui.state.newTask = project.id; ui.emit() }}
              >
                <Icon name="plus" size={16} />新任务
              </button>
            )}
          </section>
        </>
      )}
    </Sheet>
    {editing && project && <ProjectEditor project={project} onClose={() => setEditing(false)} />}
    </>
  )
}
