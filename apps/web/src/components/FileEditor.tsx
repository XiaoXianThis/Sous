import { useEffect, useState } from 'react'
import { api, type ProjectFile } from '../lib/api'
import Icon from './Icon'

/** One project file: read it, change it, save it (the machine checks it did not change meanwhile). */
export default function FileEditor({ projectId, path, onBack }: { projectId: string; path: string; onBack?: () => void }) {
  const [file, setFile] = useState<ProjectFile | null>(null)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)

  async function load() {
    setFile(null)
    setError('')
    setSaved(false)
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

  const name = path.split('/').pop()

  return (
    <div className="grid grid-rows-[auto_minmax(0,1fr)] min-h-0 h-full">
      <div className="flex items-center gap-2 min-h-11 px-3 min-w-0">
        {onBack && <button className="icon-btn min-[761px]:hidden" data-back aria-label="返回文件列表" onClick={onBack}><Icon name="back" size={18} /></button>}
        <Icon name="file" size={16} className="text-muted" />
        <span className="flex-1 min-w-0 text-[13px] font-medium overflow-hidden text-ellipsis whitespace-nowrap" title={path}>
          {name}{dirty && <span className="text-warm" title="有未保存的修改"> ●</span>}
        </span>
        {saved && !dirty && <span className="text-[12px] text-ok flex items-center gap-1"><Icon name="check" size={14} />已保存</span>}
        <button className="icon-btn" aria-label="重新加载" data-tip="重新加载" data-tip-pos="bottom" onClick={() => void load()}><Icon name="refresh" size={16} /></button>
        <button className="btn small" disabled={!dirty || busy} onClick={() => void save()}>{busy ? '保存中…' : '保存'}</button>
      </div>
      {error && <p className="m-0 mx-3 mb-2 text-danger text-[13px]" role="alert">{error}</p>}
      {!file && !error ? (
        <div className="m-3 rounded-[12px] bg-sunken animate-pulse"></div>
      ) : file?.binary ? (
        <p className="m-3 text-muted text-[13px]">这是二进制文件（{Math.ceil(file.size / 1024)} KB），不能在页面上看。</p>
      ) : file ? (
        <textarea
          className="m-0 mx-3 mb-3 p-3 rounded-[12px] border-0 bg-sunken font-mono text-[12.5px] leading-[1.6] resize-none min-h-0 whitespace-pre"
          value={text}
          spellCheck={false}
          wrap="off"
          aria-label={`编辑 ${path}`}
          onChange={(e) => { setText(e.currentTarget.value); setSaved(false) }}
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
