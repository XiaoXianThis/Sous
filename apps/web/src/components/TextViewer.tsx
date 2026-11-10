import Icon from './Icon'
import { Dialog, DialogClose, DialogContent, DialogTitle } from './ui/dialog'

export default function TextViewer({ title, text, diff = false, onClose }: {
  title: string
  text: string
  diff?: boolean
  onClose: () => void
}) {
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
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        overlay="backdrop z-40"
        className="sheet z-[41] top-[5dvh] w-[min(900px,calc(100vw-32px))] max-h-[90dvh] rounded-[18px] px-4 py-[14px] max-[760px]:top-auto max-[760px]:w-auto max-[760px]:max-h-[calc(var(--vv-h,100dvh)-var(--safe-t)-24px)] max-[760px]:rounded-[34px] max-[760px]:pt-7"
        aria-label={title}
      >
        <header className="flex justify-between items-center mb-2">
          <DialogTitle className="m-0 text-[16px]">{title}</DialogTitle>
          <DialogClose className="icon-btn" aria-label="关闭">
            <Icon name="x" size={18} />
          </DialogClose>
        </header>
        {!text.trim() ? (
          <p className="text-muted">（空）</p>
        ) : (
          <pre className="m-0 overflow-auto overscroll-contain min-h-0 font-mono text-[12.5px] leading-[1.5] bg-page border border-line rounded-[12px] px-3 py-2.5">
            {lines.map((line, i) => (
              <span key={i} className={`block whitespace-pre min-h-[1.5em] ${cls(line)}`}>{line}</span>
            ))}
          </pre>
        )}
      </DialogContent>
    </Dialog>
  )
}
