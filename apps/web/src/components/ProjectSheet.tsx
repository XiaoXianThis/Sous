import { useState } from 'react'
import { api } from '../lib/api'
import { go, loadOverview, ui, useWb } from '../lib/app'
import { projectCards } from '../lib/workbench'
import AgentCard from './AgentCard'
import ApprovalCard from './ApprovalCard'
import Icon from './Icon'
import Locations from './Locations'
import ProjectEditor from './ProjectEditor'
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

  return (
    <>
      <Sheet title={project ? project.name : '项目'} onClose={onClose} wide>
      {!wb.data ? (
        <p className="text-muted text-[13px] m-0">加载中…</p>
      ) : !project ? (
        <p className="text-muted text-[13px] m-0">这个项目不存在了。</p>
      ) : (
        <>
          <Locations locations={card?.locations ?? []} />
          {project.notes && <p className="m-0 text-[13px] text-muted [overflow-wrap:anywhere]">{project.notes}</p>}
          {project.repo_url && (
            <p className="m-0 flex gap-[5px] items-center text-[12px] [overflow-wrap:anywhere]">
              <Icon name="link" size={13} /><a className="text-accent" href={project.repo_url} target="_blank" rel="noreferrer">{project.repo_url}</a>
            </p>
          )}

          <div className="flex gap-2 flex-wrap items-center">
            <button className="btn small" onClick={() => { ui.state.newTask = project.id; ui.emit() }}>新建任务</button>
            {project.deploy && <button className="btn small ghost" disabled={busy} onClick={() => void deploy()}>部署</button>}
            <button className="btn small ghost" disabled={busy} onClick={() => setEditing(true)}>编辑项目</button>
            {clonable.length > 0 && (
              <span className="inline-flex gap-2 items-center">
                <select
                  className="border border-dashed border-line bg-transparent rounded-full px-2.5 py-[3px] text-[13px] text-muted"
                  value={cloneTo}
                  onChange={(e) => setCloneTo(e.currentTarget.value)}
                  aria-label="clone 到"
                >
                  <option value="">clone 到…</option>
                  {clonable.map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
                </select>
                {cloneTo && <button className="link accent" disabled={busy} onClick={() => void clone()}>开始</button>}
              </span>
            )}
          </div>

          {error && <p className="m-0 text-danger text-[13px]" role="alert">{error}</p>}
          <p className="m-0 text-[12px] text-muted">sous 不会合并 PR；PR 合并后才部署。</p>

          {approvals.length > 0 && (
            <section className="grid gap-1" aria-label="需要你处理">
              <h3 className="mt-1.5 mb-0.5 text-[14px] text-muted">需要你处理</h3>
              {approvals.map((a) => <ApprovalCard key={a.id} approval={a} context={titleOf(a.run_id)} />)}
            </section>
          )}

          {card && card.allAgents.length > 0 && (
            <section className="grid gap-1" aria-label="正在工作">
              <h3 className="mt-1.5 mb-0.5 text-[14px] text-muted">正在工作</h3>
              {card.allAgents.map((a) => (
                <AgentCard key={a.run.id} agent={a} onOpen={() => { ui.state.runSheet = a.run.id; ui.emit() }} />
              ))}
            </section>
          )}

          <section className="grid gap-1" aria-label="任务">
            <h3 className="mt-1.5 mb-0.5 text-[14px] text-muted">任务</h3>
            {card?.allTasks.length ? (
              card.allTasks.map((t) => <TaskRow key={t.task.id} task={t} onOpen={() => go({ view: 'task', id: t.task.id })} />)
            ) : (
              <p className="text-muted text-[13px] m-0">还没有任务。</p>
            )}
          </section>
        </>
      )}
    </Sheet>
    {editing && project && <ProjectEditor project={project} onClose={() => setEditing(false)} />}
    </>
  )
}
