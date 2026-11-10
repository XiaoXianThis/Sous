import { useState } from 'react'
import { api } from '../lib/api'
import { ui } from '../lib/app'
import { ago } from '../lib/time'
import type { GitState, RepoResult } from '../lib/types'
import type { Location } from '../lib/workbench'
import Icon from './Icon'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'

const chip = 'inline-flex items-center gap-1 h-[22px] rounded-[9px] px-1.5 text-[12px] font-medium no-underline max-w-full min-w-0 whitespace-nowrap transition-colors'

/** A short mark of where a checkout stands: "↓3 ●2"; '' when it is in step. */
export function gitMark(g?: GitState): string {
  if (!g) return ''
  const parts: string[] = []
  if (g.behind > 0) parts.push(`↓${g.behind}`)
  if (g.ahead > 0) parts.push(`↑${g.ahead}`)
  if (g.changes + g.untracked > 0) parts.push(`●${g.changes + g.untracked}`)
  return parts.join(' ')
}

export default function Locations({ locations, compact = false }: { locations: Location[]; compact?: boolean }) {
  const [open, setOpen] = useState('')
  const machines = () => {
    ui.state.settings = 'machines'
    ui.emit()
  }
  if (locations.length === 0) {
    return (
      <span className={`${chip} text-faint`} title="不在任何机器上">
        <Icon name="circle-dashed" size={13} />
      </span>
    )
  }
  // The full list names each kind: machines to work on, the deploy target,
  // the remote (end-to-end review 4.28).
  if (!compact && new Set(locations.map((l) => l.kind)).size > 1) {
    const groups: [Location['kind'], string][] = [['node', '开发机'], ['deploy', '部署到'], ['remote', '远端']]
    return (
      <div className="grid gap-1.5 min-w-0" aria-label="项目位置">
        {groups.filter(([k]) => locations.some((l) => l.kind === k)).map(([k, text]) => (
          <div key={k} className="grid gap-0.5 min-w-0">
            <span className="text-[11px] font-semibold text-faint">{text}</span>
            <Locations locations={locations.filter((l) => l.kind === k)} />
          </div>
        ))}
      </div>
    )
  }
  return (
    <div className="flex flex-wrap gap-1 items-center min-w-0" aria-label="项目位置">
      {locations.map((l) => {
        if (l.kind === 'remote') {
          const icon = l.name === 'GitHub' ? 'github' : l.icon === 'link' ? 'globe' : l.icon
          const inner = <><Icon name={icon} size={13} />{!compact && <span className="overflow-hidden text-ellipsis">{l.name}</span>}</>
          return l.href ? (
            <a key={l.kind + l.id} className={`${chip} bg-sunken text-muted hover:text-ink`} href={l.href} target="_blank" rel="noreferrer" title={l.title} aria-label={l.name}>
              {inner}
            </a>
          ) : (
            <span key={l.kind + l.id} className={`${chip} bg-sunken text-muted`} title={l.title} aria-label={l.name}>{inner}</span>
          )
        }
        if (l.kind === 'deploy') {
          return (
            <button key={l.kind + l.id} className={`${chip} border-0 bg-[color-mix(in_srgb,var(--warm)_13%,transparent)] text-warm hover:brightness-110`} title={`部署到 ${l.title}`} aria-label={`部署到 ${l.name}`} onClick={machines}>
              <Icon name="rocket" size={13} />{!compact && <span className="overflow-hidden text-ellipsis">{l.name}</span>}
            </button>
          )
        }
        const mark = gitMark(l.git)
        return (
          <Popover key={l.kind + l.id} open={open === l.id} onOpenChange={(o) => setOpen(o ? l.id : '')}>
            <PopoverTrigger asChild>
            <button
              className={`${chip} border-0 bg-sunken text-muted hover:text-ink ${l.online === false ? 'opacity-55' : ''}`}
              title={l.title}
              aria-label={`${l.name}${l.online === false ? '（离线）' : ''}${mark ? `，${describe(l.git!)}` : ''}`}
              // Inside a clickable card: the card does not open too.
              onClick={(e) => e.stopPropagation()}
            >
              <span className="relative grid">
                <Icon name={l.icon} size={13} />
              </span>
              <span className="overflow-hidden text-ellipsis max-w-[110px]">{l.name}</span>
              {mark && <span className="text-warm tabular-nums">{mark}</span>}
              <span className={`w-1.5 h-1.5 rounded-full flex-none ${l.online ? 'bg-ok' : 'bg-faint'}`}></span>
            </button>
            </PopoverTrigger>
            {open === l.id && <Place l={l} onClose={() => setOpen('')} onMachines={machines} />}
          </Popover>
        )
      })}
    </div>
  )
}

function describe(g: GitState): string {
  const parts: string[] = []
  if (g.behind > 0) parts.push(`落后远端 ${g.behind} 个提交`)
  if (g.ahead > 0) parts.push(`${g.ahead} 个提交没推送`)
  if (g.changes > 0) parts.push(`${g.changes} 个文件改了没提交`)
  if (g.untracked > 0) parts.push(`${g.untracked} 个新文件`)
  return parts.length ? parts.join('，') : '和远端一致'
}

/** Where a project lives, folded into one chip (laptop icon + count) that opens the full list. */
export function LocationsChip({ locations }: { locations: Location[] }) {
  const [open, setOpen] = useState(false)
  const n = locations.filter((l) => l.kind === 'node').length || locations.length
  if (locations.length === 0) return <Locations locations={locations} compact />
  const offline = locations.some((l) => l.kind === 'node' && l.online === false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          className={`${chip} border-0 bg-sunken text-muted hover:text-ink`}
          aria-label={`项目位置：${locations.map((l) => l.name).join('、')}`}
          title={locations.map((l) => l.name).join('、')}
          onClick={(e) => e.stopPropagation()}
        >
          <Icon name="laptop" size={13} /><span className="tabular-nums">{n}</span>
          {offline && <span className="w-1.5 h-1.5 rounded-full flex-none bg-faint"></span>}
        </button>
      </PopoverTrigger>
      {/* Opens above the chip (it sits at the bottom of a card), below when there is no room. */}
      <PopoverContent side="top" sideOffset={6} aria-label="项目位置" className="w-max max-w-[min(300px,calc(100vw-16px))] p-2" onClick={(e) => e.stopPropagation()}>
        <Locations locations={locations} />
      </PopoverContent>
    </Popover>
  )
}

/** One machine's checkout: where it stands, and bringing it in step. */
function Place({ l, onClose, onMachines }: { l: Location; onClose: () => void; onMachines: () => void }) {
  const [busy, setBusy] = useState('')
  const [result, setResult] = useState<RepoResult | null>(null)
  const [error, setError] = useState('')
  const g = result?.git ?? l.git

  async function act(what: 'sync' | 'wip') {
    if (!l.project) return
    setBusy(what)
    setError('')
    try {
      setResult(await (what === 'sync' ? api.repoSync(l.project, l.id) : api.repoWip(l.project, l.id)))
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  return (
    <PopoverContent
      aria-label={l.name}
      className="w-[min(300px,calc(100vw-16px))] p-3 grid gap-2 content-start text-[13px] text-ink font-normal whitespace-normal"
      onClick={(e) => e.stopPropagation()}
    >
        <div className="flex items-center gap-2">
          <Icon name={l.icon} size={15} />
          <strong className="flex-1">{l.name}</strong>
          <span className={`text-[12px] ${l.online ? 'text-ok' : 'text-faint'}`}>{l.online ? '在线' : '离线'}</span>
        </div>
        {l.path && <code className="font-mono text-[12px] text-muted break-all">{l.path}</code>}
        {g ? (
          <div className="grid gap-0.5">
            {g.upstream ? <span className="text-muted">跟踪 <code className="font-mono">{g.upstream}</code></span> : <span className="text-muted">这个分支没有跟踪远端</span>}
            <span>{describe(g)}</span>
            {g.fetched_at ? <span className="text-faint text-[12px]">{ago(g.fetched_at)}拉取过远端</span> : null}
          </div>
        ) : (
          <span className="text-muted">还不知道这里的 git 状态（机器上的 sous 需要更新）</span>
        )}
        {result && <p className={`m-0 ${result.outcome === 'fast_forwarded' || result.outcome === 'pushed' || result.outcome === 'up_to_date' ? 'text-ok' : 'text-warm'}`}>{result.message}</p>}
        {error && <p className="m-0 text-danger" role="alert">{error}</p>}
        <div className="flex gap-1.5 flex-wrap">
          <button className="btn small" disabled={!l.online || !!busy} onClick={() => void act('sync')} title="拉取远端；能快进就快进，有改动或分叉时什么都不动">
            <Icon name="download" size={14} />{busy === 'sync' ? '同步中…' : '同步'}
          </button>
          <button className="btn small ghost" disabled={!l.online || !!busy} onClick={() => void act('wip')} title="把没提交、没推送的改动推到 origin 的 sous/wip/ 分支，本地不变">
            <Icon name="branch" size={14} />{busy === 'wip' ? '推送中…' : '存成 WIP 分支'}
          </button>
          <button className="link text-[12.5px]" onClick={() => { onClose(); onMachines() }}>机器设置</button>
        </div>
    </PopoverContent>
  )
}
