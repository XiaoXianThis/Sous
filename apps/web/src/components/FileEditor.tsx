import { useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { api, type ProjectFile } from '../lib/api'
import { highlightCode, imageType, isMarkdownPath, languageOf } from '../lib/highlight'
import Icon from './Icon'
import Markdown from './Markdown'

// The textarea and the highlighted layer under it must lay text out identically.
const LAYER = 'm-0 p-3 font-mono text-[12.5px] leading-[1.6] whitespace-pre [tab-size:2]'

/** A textarea with a syntax-highlighted layer behind its (transparent) text. */
function CodeArea({ value, lang, label, onChange, onKeyDown }: {
  value: string
  lang: string
  label: string
  onChange: (v: string) => void
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
}) {
  const layer = useRef<HTMLPreElement>(null)
  const deferred = useDeferredValue(value)
  const html = useMemo(() => highlightCode(deferred, lang) + '\n', [deferred, lang])
  const sync = (t: HTMLTextAreaElement) => {
    if (!layer.current) return
    layer.current.scrollTop = t.scrollTop
    layer.current.scrollLeft = t.scrollLeft
  }
  return (
    <div className="relative mx-3 mb-3 min-h-0 rounded-[12px] bg-sunken overflow-hidden">
      <pre ref={layer} aria-hidden className={`${LAYER} absolute inset-0 overflow-hidden pointer-events-none`}>
        <code className="hljs" dangerouslySetInnerHTML={{ __html: html }} />
      </pre>
      <textarea
        className={`${LAYER} absolute inset-0 w-full h-full border-0 bg-transparent text-transparent caret-ink resize-none overflow-auto selection:bg-accent/25`}
        value={value}
        spellCheck={false}
        wrap="off"
        aria-label={label}
        onChange={(e) => onChange(e.currentTarget.value)}
        onScroll={(e) => sync(e.currentTarget)}
        onKeyDown={onKeyDown}
      />
    </div>
  )
}

/** Shows an image from a data URL on a checkerboard so transparency is visible. */
function ImageView({ src, name, size }: { src: string; name: string; size: number }) {
  const [dim, setDim] = useState('')
  return (
    <div className="mx-3 mb-3 min-h-0 rounded-[12px] bg-sunken grid grid-rows-[minmax(0,1fr)_auto] overflow-hidden">
      <div className="min-h-0 overflow-auto grid place-items-center p-3">
        <img
          src={src}
          alt={name}
          className="max-w-full max-h-full object-contain rounded-[6px] bg-panel"
          onLoad={(e) => setDim(`${e.currentTarget.naturalWidth} × ${e.currentTarget.naturalHeight}`)}
        />
      </div>
      <div className="px-3 py-1.5 text-[12px] text-muted border-t border-line">
        {dim && <span>{dim} · </span>}{size >= 1024 ? `${Math.ceil(size / 1024)} KB` : `${size} B`}
      </div>
    </div>
  )
}

/** One project file: read it, change it, save it (the machine checks it did not change meanwhile). */
export default function FileEditor({ projectId, path, onBack }: { projectId: string; path: string; onBack?: () => void }) {
  const [file, setFile] = useState<ProjectFile | null>(null)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [source, setSource] = useState(false)

  async function load() {
    setFile(null)
    setError('')
    setSaved(false)
    setSource(false)
    try {
      const r = await api.projectFile(projectId, path)
      setFile(r.result)
      setText(r.result.content)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => { void load() }, [projectId, path])

  const dirty = !!file && !file.binary && text !== file.content

  async function save() {
    if (!file || !dirty || busy) return
    setBusy(true)
    setError('')
    try {
      const r = await api.writeProjectFile(projectId, path, text, file.sha256)
      setFile(r.result)
      setText(r.result.content)
      setSaved(true)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const name = path.split('/').pop() ?? path
  const lang = languageOf(path)
  const mime = imageType(path)
  const isSvg = mime === 'image/svg+xml'
  // Markdown and SVG have a rendered view (the default) next to the source.
  const rendered = isMarkdownPath(path) || isSvg
  const showSource = !rendered || source
  const imageSrc = file?.binary && file.data && mime ? `data:${mime};base64,${file.data}` : isSvg ? `data:${mime};charset=utf-8,${encodeURIComponent(text)}` : ''

  return (
    <div className="grid grid-rows-[auto_minmax(0,1fr)] min-h-0 h-full">
      <div className="flex items-center gap-2 min-h-11 px-3 min-w-0">
        {onBack && <button className="icon-btn min-[761px]:hidden" data-back aria-label="返回文件列表" onClick={onBack}><Icon name="back" size={18} /></button>}
        <Icon name="file" size={16} className="text-muted" />
        <span className="flex-1 min-w-0 text-[13px] font-medium overflow-hidden text-ellipsis whitespace-nowrap" title={path}>
          {name}{dirty && <span className="text-warm" title="有未保存的修改"> ●</span>}
        </span>
        {saved && !dirty && <span className="text-[12px] text-ok flex items-center gap-1"><Icon name="check" size={14} />已保存</span>}
        {rendered && file && !file.binary && (
          <div className="flex p-0.5 rounded-[9px] bg-sunken text-[12px]" role="group" aria-label="查看方式">
            {([['渲染', false], ['源码', true]] as const).map(([t, v]) => (
              <button
                key={t}
                className={`px-2.5 h-6 rounded-[7px] border-0 ${source === v ? 'bg-panel text-ink shadow-soft' : 'bg-transparent text-muted'}`}
                aria-pressed={source === v}
                onClick={() => setSource(v)}
              >{t}</button>
            ))}
          </div>
        )}
        <button className="icon-btn" aria-label="重新加载" data-tip="重新加载" data-tip-pos="bottom" onClick={() => void load()}><Icon name="refresh" size={16} /></button>
        <button className="btn small" disabled={!dirty || busy} onClick={() => void save()}>{busy ? '保存中…' : '保存'}</button>
      </div>
      {error && <p className="m-0 mx-3 mb-2 text-danger text-[13px]" role="alert">{error}</p>}
      {!file && !error ? (
        <div className="m-3 rounded-[12px] bg-sunken animate-pulse"></div>
      ) : file?.binary && imageSrc ? (
        <ImageView src={imageSrc} name={name} size={file.size} />
      ) : file?.binary ? (
        <p className="m-3 text-muted text-[13px]">这是二进制文件（{Math.ceil(file.size / 1024)} KB），不能在页面上看。</p>
      ) : file && !showSource ? (
        isSvg ? <ImageView src={imageSrc} name={name} size={file.size} /> : (
          <div className="mx-3 mb-3 min-h-0 overflow-auto rounded-[12px] bg-sunken px-5 py-4 selectable">
            <Markdown text={text} className="text-[14px] leading-[1.75]" />
          </div>
        )
      ) : file ? (
        <CodeArea
          value={text}
          lang={lang}
          label={`编辑 ${path}`}
          onChange={(v) => { setText(v); setSaved(false) }}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 's') { e.preventDefault(); void save() }
            if (e.key === 'Tab' && !e.shiftKey) {
              e.preventDefault()
              const t = e.currentTarget
              const { selectionStart: a, selectionEnd: b } = t
              setText(text.slice(0, a) + '  ' + text.slice(b))
              requestAnimationFrame(() => { t.selectionStart = t.selectionEnd = a + 2 })
            }
          }}
        />
      ) : null}
    </div>
  )
}
