import { useEffect, useState } from 'react'

interface Health {
  version: string
  commit?: string
  web?: { commit: string; dirty?: boolean; built_at: string }
}

const short = (c = '') => c.replace(/-dirty$/, '').slice(0, 8)
const same = (a = '', b = '') => !!a && !!b && (short(a).startsWith(short(b)) || short(b).startsWith(short(a)))

/**
 * Which builds are running: the Hub, the page it serves, and the page open
 * here. A page built from another commit than the Hub, or an open page older
 * than the served one, is said plainly (end-to-end review 4.23).
 */
export default function Versions() {
  const [h, setH] = useState<Health | null>(null)
  useEffect(() => {
    fetch('/healthz').then((r) => r.json()).then(setH).catch(() => setH(null))
  }, [])
  const here = typeof __BUILD__ !== 'undefined' ? __BUILD__ : { commit: '', dirty: false, built_at: '' }
  if (!h) return null
  const warnings: string[] = []
  if (h.web && h.commit && h.web.commit && !same(h.commit, h.web.commit)) warnings.push('Hub 托管的页面不是用 Hub 同一个提交构建的，运行 wok build web 重新构建。')
  if (!h.web) warnings.push('Hub 托管的页面没有构建信息，可能是旧的构建；运行 wok build web 重新构建。')
  if (h.web?.commit && here.commit && !same(h.web.commit, here.commit)) warnings.push('这个页面和 Hub 上的不是同一个构建，刷新一下。')
  return (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]" aria-label="版本">
      <dt className="text-muted">Hub</dt>
      <dd className="m-0 font-mono">{h.version}{h.commit ? ` · ${h.commit}` : ''}</dd>
      <dt className="text-muted">页面</dt>
      <dd className="m-0 font-mono">
        {here.commit || '未知'}{here.dirty ? '（有未提交的改动）' : ''}
        {here.built_at && <span className="text-muted font-sans"> · {new Date(here.built_at).toLocaleString('zh-CN')}构建</span>}
      </dd>
      {warnings.map((w) => (
        <dd key={w} className="col-span-2 m-0 text-warm" role="status">{w}</dd>
      ))}
    </dl>
  )
}
