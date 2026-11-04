import { useEffect, useState, type CSSProperties } from 'react'
import { go, ui, useUi, useWb } from '../lib/app'
import { columnCount, distribute, estimateHeight } from '../lib/masonry'
import { projectCards } from '../lib/workbench'
import Icon from './Icon'
import ProjectCardComponent from './ProjectCard'
import ProjectSetupSheet from './ProjectSetupSheet'

export default function Workbench() {
  const wbState = useWb()
  const uiState = useUi()
  const [setup, setSetup] = useState(false)
  const [width, setWidth] = useState(0)
  const [gridEl, setGridEl] = useState<HTMLDivElement | null>(null)

  const data = wbState.data
  const cards = data ? projectCards(data, wbState.inbox?.tasks ?? []) : []
  const attention = uiState.route.view === 'inbox'
  const pending = (wbState.inbox?.approvals.filter((a) => a.review === 'user').length ?? 0) + (wbState.inbox?.tasks.length ?? 0)
  const waitingRunIds = new Set((wbState.inbox?.approvals ?? []).filter((a) => a.review === 'user').map((a) => a.run_id))
  const shown = attention ? cards.filter((c) => c.rank === 'attention' || c.agents.some((a) => waitingRunIds.has(a.run.id))) : cards
  const cols = columnCount(width)
  const columns = distribute(shown, cols, estimateHeight)
  const busy = (nodeId: string) => data?.runs.filter((r) => r.node_id === nodeId).length ?? 0
  const filter = () => go({ view: attention ? 'overview' : 'inbox', id: '' })

  useEffect(() => {
    if (!gridEl) return
    const ro = new ResizeObserver(() => setWidth(gridEl.clientWidth))
    ro.observe(gridEl)
    setWidth(gridEl.clientWidth)
    return () => ro.disconnect()
  }, [gridEl])

  return (
    <>
    <div className="px-5 pt-4 pb-[30px] grid gap-3 content-start max-w-[1400px] max-[760px]:px-3.5 max-[760px]:pt-3 max-[760px]:pb-6">
      <header className="flex items-center gap-2 flex-wrap">
        <h1 className="text-[20px] m-0">工作台</h1>
        <span className="flex-1"></span>
        {pending > 0 ? (
          <button
            className={`inline-flex items-center gap-1 border rounded-full px-3 py-1 text-[13px] text-warm ${
              attention ? 'bg-[color-mix(in_srgb,var(--warm)_12%,var(--panel))] border-warm' : 'border-line bg-panel hover:border-warm'
            }`}
            aria-pressed={attention}
            onClick={filter}
          >
            <Icon name="hand" size={14} />需要你处理 {pending}
          </button>
        ) : attention ? (
          <button className="inline-flex items-center gap-1 border border-line bg-panel rounded-full px-3 py-1 text-[13px] text-warm hover:border-warm" onClick={filter}>
            显示全部
          </button>
        ) : null}
        <button className="btn small ghost inline-flex items-center gap-1" onClick={() => setSetup(true)}>
          <Icon name="plus" size={15} />新建 / 登记
        </button>
      </header>

      {data && (
        <div className="flex gap-1.5 flex-wrap overflow-x-auto" aria-label="机器">
          {data.nodes.map((n) => (
            <button
              key={n.id}
              className={`inline-flex items-center gap-1.5 border border-line bg-panel rounded-full px-2.5 py-[3px] text-[12px] text-muted whitespace-nowrap hover:border-accent hover:text-ink ${
                n.online ? '' : 'opacity-[.55]'
              }`}
              title={`${n.name}${n.online ? '' : '（离线）'}`}
              onClick={() => {
                ui.state.settings = 'machines'
                ui.emit()
              }}
            >
              <Icon name={n.role === 'server' ? 'server' : 'laptop'} size={14} />
              <span className="text-ink">{n.name}</span>
              <span className="tabular-nums">{busy(n.id)}/{n.concurrency}</span>
              <span className={`w-[7px] h-[7px] rounded-full ${n.online ? 'bg-ok' : 'bg-muted'}`}></span>
            </button>
          ))}
          {!data.nodes.length && (
            <button
              className="inline-flex items-center gap-1.5 border border-line bg-panel rounded-full px-2.5 py-[3px] text-[12px] text-muted whitespace-nowrap hover:border-accent hover:text-ink"
              onClick={() => {
                ui.state.settings = 'machines'
                ui.emit()
              }}
            >
              <Icon name="laptop" size={14} />还没有机器，去配对
            </button>
          )}
        </div>
      )}

      {wbState.error && !data ? (
        <p className="text-danger">{wbState.error}</p>
      ) : !data ? (
        <p className="text-muted text-sm m-0">加载中…</p>
      ) : attention && !shown.length ? (
        <div className="grid justify-items-center gap-1.5 pt-[12vh] px-4 text-muted text-center">
          <Icon name="check" size={36} />
          <p className="text-[18px] text-ink font-semibold mt-1.5 mb-0">现在没有需要你处理的事。</p>
          <p className="m-0">待办、进行中和已结束的任务都在项目卡片里。</p>
        </div>
      ) : !cards.length ? (
        <div className="grid justify-items-center gap-1.5 pt-[12vh] px-4 text-muted text-center">
          <Icon name="folders" size={36} />
          <p className="text-[18px] text-ink font-semibold mt-1.5 mb-0">还没有项目</p>
          <p className="m-0">新建一个，或者登记工作空间里已有的仓库。</p>
          <button className="btn mt-2.5" onClick={() => setSetup(true)}>新建或登记项目</button>
        </div>
      ) : (
        <div
          ref={setGridEl}
          className="grid grid-cols-[repeat(var(--cols,1),minmax(0,1fr))] gap-3.5 items-start max-[760px]:grid-cols-1"
          style={{ '--cols': cols } as CSSProperties}
        >
          {columns.map((column, i) => (
            <div key={i} className="grid gap-3.5 content-start min-w-0">
              {column.map((card) => (
                <ProjectCardComponent
                  key={card.project.id}
                  card={card}
                  onOpenTask={(id) => go({ view: 'task', id })}
                  onOpenProject={(id) => go({ view: 'project', id })}
                />
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
    {setup && <ProjectSetupSheet onClose={() => setSetup(false)} />}
    </>
  )
}
