import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { useChat } from '../lib/app'
import { cronPresets, describeCron } from '../lib/schedules'
import type { Schedule } from '../lib/types'
import Sheet from './Sheet'

export default function Schedules({ onClose }: { onClose: () => void }) {
  const chat = useChat()
  const [list, setList] = useState<Schedule[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [info, setInfo] = useState('')
  const [editing, setEditing] = useState<Partial<Schedule> | null>(null)
  const [busy, setBusy] = useState(false)
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  async function load() {
    try {
      setList((await api.schedules()).schedules)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [chat.schedulesRev])

  function startNew() {
    setInfo('')
    setEditing({ title: '', cron: cronPresets[0].cron, prompt: '', enabled: true, tz })
  }

  async function save() {
    if (!editing) return
    setBusy(true)
    setError('')
    try {
      const body = { title: editing.title, cron: editing.cron, prompt: editing.prompt, enabled: editing.enabled, tz: editing.tz || tz }
      if (editing.id) await api.updateSchedule(editing.id, body)
      else await api.createSchedule(body)
      setEditing(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function toggle(s: Schedule) {
    try {
      await api.updateSchedule(s.id, { enabled: !s.enabled })
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function remove(s: Schedule) {
    if (!confirm(`删除定时任务“${s.title}”？`)) return
    try {
      await api.deleteSchedule(s.id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function runNow(s: Schedule) {
    try {
      await api.runSchedule(s.id)
      setInfo(`已触发“${s.title}”，结果会出现在${s.thread_id ? '它所在的对话' : '主线'}里。`)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  function when(ts: number, zone: string) {
    if (!ts) return ''
    return new Date(ts).toLocaleString('zh-CN', { timeZone: zone || undefined, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  }

  return (
    <Sheet title="定时任务" onClose={onClose}>
      <p className="text-muted text-[13px] m-0">到时间时，sous 会把说明交给主管去做，比如每天早上汇总进展。也可以直接在对话里说“每天 9 点帮我……”。</p>

      {editing ? (
        <form className="grid gap-2.5 border border-dashed border-accent rounded-[14px] p-3" onSubmit={(e) => { e.preventDefault(); void save() }}>
          <label className="grid gap-1 text-[14px]">
            名字
            <input
              className="border border-line bg-page rounded-[10px] px-2 py-1.5 text-ink text-[14px]"
              value={editing.title ?? ''}
              onChange={(e) => setEditing({ ...editing, title: e.currentTarget.value })}
              placeholder="比如：每日进展"
            />
          </label>
          <label className="grid gap-1 text-[14px]">
            时间
            <div className="flex gap-2.5 items-center flex-wrap">
              <select
                className="border border-line bg-page rounded-[10px] px-2 py-1.5 text-ink"
                aria-label="常用时间"
                defaultValue=""
                onChange={(e) => e.currentTarget.value && setEditing({ ...editing, cron: e.currentTarget.value })}
              >
                <option value="">常用时间…</option>
                {cronPresets.map((p) => <option key={p.cron} value={p.cron}>{p.label}</option>)}
              </select>
              <input
                className="font-mono text-[13px] flex-1 min-w-0 border border-line bg-page rounded-[10px] px-2 py-1.5 text-ink"
                value={editing.cron ?? ''}
                onChange={(e) => setEditing({ ...editing, cron: e.currentTarget.value })}
                aria-label="cron 表达式"
                placeholder="分 时 日 月 周"
              />
            </div>
            <span className="text-muted text-[13px]">{describeCron(editing.cron ?? '')}（{editing.tz || tz}）</span>
          </label>
          <label className="grid gap-1 text-[14px]">
            要做的事
            <textarea
              className="border border-line bg-page rounded-[10px] px-2 py-1.5 text-ink resize-y text-[14px]"
              rows={4}
              value={editing.prompt ?? ''}
              onChange={(e) => setEditing({ ...editing, prompt: e.currentTarget.value })}
              placeholder="比如：汇总昨天完成和受阻的任务，三句话以内发给我"
            ></textarea>
          </label>
          <label className="flex items-center gap-1.5 text-[14px]">
            <input type="checkbox" checked={editing.enabled ?? false} onChange={(e) => setEditing({ ...editing, enabled: e.currentTarget.checked })} /> 启用
          </label>
          <div className="flex gap-2.5 items-center flex-wrap">
            <button className="btn small py-[7px] px-4" type="submit" disabled={busy || !editing.prompt?.trim() || !editing.cron?.trim()}>保存</button>
            <button className="link" type="button" onClick={() => setEditing(null)}>取消</button>
          </div>
        </form>
      ) : (
        <button className="btn small py-[7px] px-4 justify-self-start" onClick={startNew}>新建定时任务</button>
      )}

      {error && <p className="text-danger text-[13px] m-0" role="alert">{error}</p>}
      {info && <p className="text-muted text-[13px] m-0">{info}</p>}

      <ul className="list-none m-0 p-0 grid gap-2">
        {list.map((s) => (
          <li key={s.id} className={`border border-line rounded-[14px] px-3 py-2.5 grid gap-1 ${s.enabled ? '' : 'opacity-[.65]'}`}>
            <div className="flex gap-2 items-baseline flex-wrap">
              <strong>{s.title}</strong>
              <span className="text-muted text-[13px]">{describeCron(s.cron)}{s.tz ? `（${s.tz}）` : ''}</span>
            </div>
            <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere] text-[14px]">{s.prompt}</p>
            <p className="m-0 text-muted text-[13px]">
              {s.enabled && s.next_run ? <>下次：{when(s.next_run, s.tz)}</> : !s.enabled ? '已停用' : null}
              {s.last_run ? <> · 上次：{when(s.last_run, s.tz)}</> : null}
            </p>
            <div className="flex gap-3.5 justify-end flex-wrap">
              <button className="link" onClick={() => void runNow(s)}>现在执行一次</button>
              <button className="link" onClick={() => void toggle(s)}>{s.enabled ? '停用' : '启用'}</button>
              <button className="link" onClick={() => { setInfo(''); setEditing({ ...s }) }}>编辑</button>
              <button className="link danger" onClick={() => void remove(s)}>删除</button>
            </div>
          </li>
        ))}
        {list.length === 0 && !loading && !editing && (
          <li className="text-muted text-center border border-dashed border-line rounded-[14px] px-3 py-6">还没有定时任务。</li>
        )}
      </ul>
    </Sheet>
  )
}
