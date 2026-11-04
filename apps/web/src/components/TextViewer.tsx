import { useEffect } from 'react'

export default function TextViewer({ title, text, diff = false, onClose }: {
  title: string
  text: string
  diff?: boolean
  onClose: () => void
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const lines = text.split('\n')
  function cls(line: string): string {
    if (!diff) return ''
    if (line.startsWith('+++') || line.startsWith('---')) return 'font-semibold'
    if (line.startsWith('diff --git')) return 'font-semibold'
    if (line.startsWith('@@')) return 'text-muted'
    if (line.startsWith('+')) return 'bg-[color-mix(in_srgb,var(--done)_16%,transparent)]'
    if (line.startsWith('-')) return 'bg-[color-mix(in_srgb,var(--danger)_14%,transparent)]'
    return ''
  }

  return (
    <>
      <div className="backdrop z-40" role="presentation" onClick={onClose}></div>
      <div
        className="sheet z-[41] top-[5vh] w-[min(900px,calc(100vw-32px))] max-h-[90vh] rounded-[16px] px-4 py-[14px] max-[760px]:w-full max-[760px]:max-h-[92vh] max-[760px]:rounded-t-[16px] max-[760px]:rounded-b-none max-[760px]:pb-[14px]"
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header className="flex justify-between items-center mb-2">
          <h2 className="m-0 text-[16px]">{title}</h2>
          <button className="icon-btn" aria-label="关闭" onClick={onClose}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </header>
        {!text.trim() ? (
          <p className="text-muted">（空）</p>
        ) : (
          <pre className="m-0 overflow-auto font-mono text-[12.5px] leading-[1.5] bg-page border border-line rounded-[10px] px-3 py-2.5">
            {lines.map((line, i) => (
              <span key={i} className={`block whitespace-pre min-h-[1.5em] ${cls(line)}`}>{line}</span>
            ))}
          </pre>
        )}
      </div>
    </>
  )
}
