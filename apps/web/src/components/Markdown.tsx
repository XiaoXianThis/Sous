import { memo, useEffect, useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import Icon from './Icon'

// highlight.js and its languages are big and only needed once a code block
// shows up: they load on first use, not with the page (end-to-end review 4.24).
type Hljs = typeof import('../lib/highlight')['hljs']
let hljsLoaded: Hljs | null = null
let hljsLoading: Promise<Hljs> | null = null
function loadHljs(): Promise<Hljs> {
  hljsLoading ??= import('../lib/highlight').then((m) => (hljsLoaded = m.hljs))
  return hljsLoading
}
function useHljs(): Hljs | null {
  const [h, setH] = useState(hljsLoaded)
  useEffect(() => {
    if (!h) void loadHljs().then(setH)
  }, [h])
  return h
}

/** Copies text and shows a check for a moment. */
export function CopyButton({ text, label = '复制', className = '' }: { text: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={`grid place-items-center w-7 h-7 rounded-[8px] border-0 bg-transparent text-muted hover:bg-hover hover:text-ink ${className}`}
      aria-label={done ? '已复制' : label}
      data-tip={done ? '已复制' : label}
      data-tip-pos="bottom"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true)
          setTimeout(() => setDone(false), 1400)
        })
      }}
    >
      <Icon name={done ? 'check' : 'copy'} size={14} />
    </button>
  )
}

function CodeBlock({ lang, code, streaming }: { lang: string; code: string; streaming: boolean }) {
  const [wrap, setWrap] = useState(false)
  const hljs = useHljs()
  const known = lang && hljs?.getLanguage(lang)
  // Highlighting waits for the end of the stream: fences are re-split while text arrives.
  const html = !streaming && hljs && known ? hljs.highlight(code, { language: lang, ignoreIllegals: true }).value : ''
  return (
    <div className="md-code my-3 rounded-[12px] border border-line bg-sunken overflow-hidden" data-lang={lang || 'text'}>
      <div className="flex items-center gap-2 pl-3 pr-1.5 h-9 text-[12.5px] text-muted">
        <Icon name="terminal" size={14} className="text-faint" />
        <span className="font-mono lowercase flex-1 min-w-0 truncate">{lang || 'text'}</span>
        <button
          type="button"
          className={`grid place-items-center w-7 h-7 rounded-[8px] border-0 hover:bg-hover hover:text-ink ${wrap ? 'bg-hover text-ink' : 'bg-transparent text-muted'}`}
          aria-label={wrap ? '不换行' : '自动换行'}
          aria-pressed={wrap}
          data-tip={wrap ? '不换行' : '自动换行'}
          data-tip-pos="bottom"
          onClick={() => setWrap(!wrap)}
        >
          <Icon name="list" size={14} />
        </button>
        <CopyButton text={code} label="复制代码" />
      </div>
      <pre className={`m-0 px-3.5 pt-0.5 pb-3 overflow-x-auto font-mono text-[12.5px] leading-[1.6] ${wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : ''}`}>
        {html ? <code className="hljs" dangerouslySetInnerHTML={{ __html: html }} /> : <code>{code}</code>}
      </pre>
    </div>
  )
}

const heading = 'mt-6 mb-2.5 first:mt-0 font-semibold leading-snug text-ink'

function components(streaming: boolean): Components {
  return {
    h1: ({ children }) => <h1 className={`${heading} text-[18px]`}>{children}</h1>,
    h2: ({ children }) => <h2 className={`${heading} text-[16px]`}>{children}</h2>,
    h3: ({ children }) => <h3 className={`${heading} text-[14.5px]`}>{children}</h3>,
    h4: ({ children }) => <h4 className={`${heading} text-[14px]`}>{children}</h4>,
    h5: ({ children }) => <h5 className={`${heading} text-[14px] font-medium`}>{children}</h5>,
    h6: ({ children }) => <h6 className={`${heading} text-[14px] font-normal text-muted`}>{children}</h6>,
    p: ({ children }) => <p className="my-3 first:mt-0 last:mb-0">{children}</p>,
    a: ({ href, children }) => (
      <a href={href} target="_blank" rel="noreferrer" className="md-link">{children}</a>
    ),
    ul: ({ children }) => <ul className="my-3 pl-5 list-disc grid gap-1.5 marker:text-faint [&_ul]:my-1 [&_ol]:my-1">{children}</ul>,
    ol: ({ children, start }) => (
      <ol start={start} className="my-3 pl-6 list-decimal grid gap-1.5 marker:text-faint marker:tabular-nums [&_ul]:my-1 [&_ol]:my-1">{children}</ol>
    ),
    li: ({ children, className }) => (
      <li className={`pl-1 [&>p]:my-0 [&>p+p]:mt-1.5 ${className?.includes('task-list-item') ? 'list-none -ml-5 flex gap-2 items-baseline' : ''}`}>{children}</li>
    ),
    input: ({ checked }) => (
      <span className={`inline-grid place-items-center w-3.5 h-3.5 rounded-[4px] translate-y-[2px] flex-none ${checked ? 'bg-accent text-accent-text' : 'shadow-[inset_0_0_0_1.5px_var(--line-strong)]'}`}>
        {checked && <Icon name="check" size={12} stroke={3} />}
      </span>
    ),
    blockquote: ({ children }) => (
      <blockquote className="my-3 mx-0 border-l-2 border-line-strong pl-3 text-muted [&_p]:my-0 [&_p+p]:mt-2">{children}</blockquote>
    ),
    hr: () => <hr className="my-5 border-0 h-px bg-line" />,
    strong: ({ children }) => <strong className="font-semibold text-ink">{children}</strong>,
    table: ({ children }) => (
      <div className="my-3 overflow-x-auto rounded-[12px] border border-line">
        <table className="w-full border-collapse text-[13px]">{children}</table>
      </div>
    ),
    thead: ({ children }) => <thead className="bg-sunken">{children}</thead>,
    th: ({ children, style }) => <th style={style} className="text-left font-semibold px-3 py-2 border-b border-line whitespace-nowrap">{children}</th>,
    td: ({ children, style }) => <td style={style} className="px-3 py-2 border-b border-line align-top [tr:last-child>&]:border-b-0">{children}</td>,
    img: ({ src, alt }) => <img src={typeof src === 'string' ? src : undefined} alt={alt ?? ''} className="max-w-full rounded-[12px] my-2" loading="lazy" />,
    pre: ({ children }) => <>{children}</>,
    code: ({ className, children }) => {
      const text = String(children ?? '')
      const m = /language-([\w+#-]+)/.exec(className ?? '')
      // A fenced block has a language or spans lines; anything else is inline.
      if (m || text.includes('\n')) {
        return <CodeBlock lang={(m?.[1] ?? '').toLowerCase()} code={text.replace(/\n$/, '')} streaming={streaming} />
      }
      return <code className="md-inline-code">{children}</code>
    },
  }
}

const still = components(false)
const live = components(true)

/** Renders assistant Markdown in the ZCode reading style. Raw HTML is not rendered. */
function Markdown({ text, streaming = false, children, className = 'text-[14px] leading-[1.75]' }: {
  text: string
  streaming?: boolean
  children?: ReactNode
  /** Type size and colour; the default is the chat's reading size. */
  className?: string
}) {
  return (
    <div className={`md [overflow-wrap:anywhere] min-w-0 ${className}`}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={streaming ? live : still}>
        {text}
      </ReactMarkdown>
      {children}
    </div>
  )
}

export default memo(Markdown)
