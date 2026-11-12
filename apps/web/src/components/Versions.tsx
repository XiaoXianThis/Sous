import { useEffect, useState } from 'react'
import { relayUrl } from '../trust/vault'

interface Health {
  version: string
  claimed?: boolean
}

/** Which builds are running: the relay, and the page open here. */
export default function Versions() {
  const [h, setH] = useState<Health | null>(null)
  useEffect(() => {
    const r = relayUrl()
    if (!r) return
    fetch(r.replace(/\/+$/, '') + '/healthz').then((x) => x.json()).then(setH).catch(() => setH(null))
  }, [])
  const here = typeof __BUILD__ !== 'undefined' ? __BUILD__ : { commit: '', dirty: false, built_at: '' }
  return (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]" aria-label="版本">
      <dt className="text-muted">中继</dt>
      <dd className="m-0 font-mono">{h ? h.version : '连不上'}</dd>
      <dt className="text-muted">页面</dt>
      <dd className="m-0 font-mono">
        {here.commit || '未知'}{here.dirty ? '（有未提交的改动）' : ''}
        {here.built_at && <span className="text-muted font-sans"> · {new Date(here.built_at).toLocaleString('zh-CN')}构建</span>}
      </dd>
    </dl>
  )
}
