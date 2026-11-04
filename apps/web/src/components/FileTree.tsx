import { useEffect, useState } from 'react'
import { api, type ProjectEntry } from '../lib/api'
import Icon from './Icon'

interface Dir { entries?: ProjectEntry[]; loading?: boolean; error?: string }

/** A project's files on its machine, one folder loaded at a time. */
export default function FileTree({ projectId, selected, onSelect }: {
  projectId: string
  selected: string
  onSelect: (path: string) => void
}) {
  const [dirs, setDirs] = useState<Record<string, Dir>>({})
  const [open, setOpen] = useState<Set<string>>(new Set())

  async function load(rel: string) {
    setDirs((d) => ({ ...d, [rel]: { ...d[rel], loading: true, error: '' } }))
    try {
      const r = await api.projectTree(projectId, rel)
      setDirs((d) => ({ ...d, [rel]: { entries: r.result.entries } }))
    } catch (e) {
      setDirs((d) => ({ ...d, [rel]: { error: (e as Error).message } }))
    }
  }

  useEffect(() => {
    setDirs({})
    setOpen(new Set())
    void load('')
  }, [projectId])

  // Open the folders above the selected file.
  useEffect(() => {
    const parts = selected.split('/').slice(0, -1)
    let acc = ''
    const need: string[] = []
    for (const p of parts) {
      acc = acc ? `${acc}/${p}` : p
      need.push(acc)
    }
    if (!need.length) return
    setOpen((o) => new Set([...o, ...need]))
    for (const d of need) if (!dirs[d]) void load(d)
  }, [selected])

  function toggle(rel: string) {
    setOpen((o) => {
      const n = new Set(o)
      if (n.has(rel)) n.delete(rel)
      else n.add(rel)
      return n
    })
    if (!dirs[rel]) void load(rel)
  }

  const rows = (rel: string, depth: number): React.ReactNode => {
    const d = dirs[rel]
    if (!d || d.loading) return d?.entries ? null : <p className="m-0 px-2.5 h-8 flex items-center text-[12px] text-faint" style={{ paddingLeft: 10 + depth * 16 }}>加载中…</p>
    if (d.error) {
      return (
        <p className="m-0 px-2.5 py-1.5 text-[12px] text-danger" style={{ paddingLeft: 10 + depth * 16 }} role="alert">
          {d.error}<button className="link accent ml-2" onClick={() => void load(rel)}>重试</button>
        </p>
      )
    }
    return d.entries?.map((e) => {
      const path = rel ? `${rel}/${e.name}` : e.name
      const isOpen = open.has(path)
      const sel = path === selected
      return (
        <div key={path}>
          <button
            className={`flex items-center gap-2 w-full text-left border-0 h-8 pr-2 rounded-[10px] min-w-0 text-[13px] transition-colors ${
              sel ? 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-accent' : 'bg-transparent text-ink hover:bg-hover'
            }`}
            style={{ paddingLeft: 8 + depth * 16 }}
            onClick={() => (e.dir ? toggle(path) : onSelect(path))}
            aria-expanded={e.dir ? isOpen : undefined}
            title={path}
          >
            {e.dir ? (
              <Icon name="chevron" size={12} className={`text-faint transition-transform ${isOpen ? '' : '-rotate-90'}`} />
            ) : (
              <span className="w-3 flex-none"></span>
            )}
            <Icon name={e.dir ? 'folder' : 'file'} size={16} className={e.dir ? 'text-accent' : 'text-muted'} />
            <span className="overflow-hidden text-ellipsis whitespace-nowrap">{e.name}</span>
          </button>
          {e.dir && isOpen && rows(path, depth + 1)}
        </div>
      )
    })
  }

  return <div className="grid gap-0.5 content-start" role="tree" aria-label="文件">{rows('', 0)}</div>
}
