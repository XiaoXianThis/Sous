import { useEffect, useRef, useState, type ReactNode } from 'react'
import { archivedCards, type ProjectCard } from '../lib/workbench'
import Icon from './Icon'

/**
 * The archived projects, opened from the workbench's "打开" button: latest
 * change first, searchable, drawn with the workbench's own cards.
 */
export default function ArchiveModal({ cards, renderCard, onClose }: {
  /** Every project card; the dialog keeps the archived ones. */
  cards: ProjectCard[]
  /** Draws one card, keyed by its project id. */
  renderCard: (card: ProjectCard) => ReactNode
  onClose: () => void
}) {
  const panel = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const total = cards.filter((c) => c.archived).length
  const shown = archivedCards(cards, query)

  useEffect(() => {
    input.current?.focus()
    // Escape closes only the topmost dialog (a project opened from here sits above).
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.isComposing) return
      const dialogs = document.querySelectorAll('[role="dialog"][aria-modal="true"]')
      if (dialogs[dialogs.length - 1] === panel.current) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <>
      <div className="backdrop" role="presentation" onClick={onClose}></div>
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label="已归档的项目"
        className="fixed z-[31] overflow-hidden bg-page shadow-pop grid grid-rows-[auto_minmax(0,1fr)] left-[max(16px,calc((100vw-1180px)/2))] top-[5dvh] h-[90dvh] w-[min(1180px,calc(100vw-32px))] rounded-[18px] rise max-[760px]:inset-0 max-[760px]:left-0 max-[760px]:top-0 max-[760px]:h-full max-[760px]:w-full max-[760px]:rounded-none"
      >
        <header className="flex items-center gap-3 px-5 pt-4 pb-3 bg-panel border-b border-line min-w-0 flex-wrap max-[760px]:px-3.5 max-[760px]:pt-[calc(var(--safe-t)+10px)]">
          <span className="grid place-items-center w-9 h-9 rounded-[12px] bg-sunken text-muted flex-none">
            <Icon name="archive" size={18} />
          </span>
          <h2 className="m-0 text-[17px] font-semibold tracking-tight flex items-baseline gap-1.5">
            已归档的项目<span className="text-[13px] text-faint tabular-nums font-medium">{total}</span>
          </h2>
          <span className="flex-1"></span>
          <label className="relative flex items-center w-[min(320px,100%)] max-[760px]:order-last max-[760px]:w-full">
            <Icon name="search" size={16} className="absolute left-2.5 text-faint pointer-events-none" />
            <input
              ref={input}
              type="search"
              className="w-full h-[34px] pl-8 pr-2.5 rounded-[10px] border border-line bg-page text-ink text-[13px] min-w-0"
              placeholder="搜索名字、说明或仓库"
              aria-label="搜索已归档的项目"
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
            />
          </label>
          <button className="icon-btn bg-sunken rounded-full" aria-label="关闭" onClick={onClose}><Icon name="x" size={16} /></button>
        </header>

        <div className="overflow-y-auto p-5 min-h-0 max-[760px]:p-3.5 max-[760px]:pb-[calc(16px+var(--safe-b))]">
          {shown.length ? (
            <div className="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] auto-rows-fr gap-4 max-[760px]:grid-cols-1">
              {shown.map(renderCard)}
            </div>
          ) : (
            <div className="grid justify-items-center gap-2 pt-[12dvh] px-4 text-muted text-center">
              <span className="grid place-items-center w-14 h-14 rounded-[16px] bg-sunken text-muted">
                <Icon name={total ? 'search' : 'archive'} size={26} />
              </span>
              <p className="text-[15px] text-ink font-semibold mt-2 mb-0">{total ? '没有匹配的项目' : '还没有归档的项目'}</p>
              {!total && <p className="m-0 text-[13px]">在项目详情里可以把暂时不做的项目归档，它们就不会出现在工作台上。</p>}
            </div>
          )}
        </div>
      </div>
    </>
  )
}
