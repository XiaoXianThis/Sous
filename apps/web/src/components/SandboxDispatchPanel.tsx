import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import { desktopPage, openDesktopSandbox } from '../lib/desktop-workspace'
import type { SandboxDispatch, Task } from '../lib/types'

const messages: Record<SandboxDispatch['status'], string> = {
  pending: '在 Sous 托盘打开「本机沙盒与审阅」，刷新请求并选择这项任务。只有在那边确认后才会运行。',
  running: '本机沙盒正在处理。原项目不会自动改动。',
  cancel_requested: '正在等待本机停止并确认 Guest 已清理。',
  cancelled: '已撤回本机执行请求。',
  ready: '执行结果已保存在桌面端。请在「本机沙盒与审阅」检查；任务本身未自动标为完成。',
  failed: '执行未成功。在桌面端查看日志或重试。',
  interrupted: '已中断，未确认执行结果。请在桌面先检查回收状态；系统不会自动重跑。',
  expired: '本机确认请求已过期，可重新发起。',
  stale: '任务内容或前置条件已改变，可重新发起。',
}

export default function SandboxDispatchPanel({ task }: { task: Task }) {
  const [dispatch, setDispatch] = useState<SandboxDispatch | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [readError, setReadError] = useState('')
  const [mutationError, setMutationError] = useState('')
  const sequence = useRef(0)
  const mutationPending = useRef(false)
  const wake = useRef<() => void>(() => {})
  useEffect(() => {
    let mounted = true
    let timer: ReturnType<typeof setTimeout>
    let refreshVersion = 0
    async function refresh() {
      if (!mounted) return
      const timerVersion = ++refreshVersion
      let delay = 2200
      if (!mutationPending.current && !document.hidden) {
        const rev = ++sequence.current
        try {
          const value = await api.loadSandboxDispatch(task.id)
          if (mounted && sequence.current === rev) {
            setDispatch(value.dispatch); setLoading(false); setReadError('')
            const status = value.dispatch?.status
            delay = status === 'pending' || status === 'running' || status === 'cancel_requested' ? 2200 : 10000
          }
        } catch (e) {
          if (mounted && sequence.current === rev) { setReadError((e as Error).message); setLoading(false) }
          delay = 5000
        }
      }
      if (mounted && !document.hidden && timerVersion === refreshVersion) timer = setTimeout(refresh, delay)
    }
    function refreshOnResume() {
      if (!document.hidden && !mutationPending.current) {
        clearTimeout(timer)
        void refresh()
      }
    }
    wake.current = () => {
      refreshVersion++
      clearTimeout(timer)
      if (!document.hidden) timer = setTimeout(refresh, 2200)
    }
    document.addEventListener('visibilitychange', refreshOnResume)
    void refresh()
    return () => {
      mounted = false; clearTimeout(timer); sequence.current++
      wake.current = () => {}
      document.removeEventListener('visibilitychange', refreshOnResume)
    }
  }, [task.id])

  const state = dispatch?.status
  const active = state === 'pending' || state === 'running' || state === 'cancel_requested'
  const canRequest = !active && (task.status === 'inbox' || task.status === 'planned')
  async function act(cancel: boolean) {
    if (mutationPending.current) return
    const rev = ++sequence.current
    mutationPending.current = true
    setBusy(true)
    setMutationError('')
    try {
      const res = cancel && dispatch ? await api.cancelSandboxDispatch(dispatch.id) : await api.requestSandboxDispatch(task.id)
      if (sequence.current === rev) { setDispatch(res.dispatch); setReadError('') }
    } catch (e) {
      if (sequence.current === rev) setMutationError((e as Error).message)
    } finally {
      mutationPending.current = false
      if (sequence.current === rev) setBusy(false)
      wake.current()
    }
  }

  const error = mutationError || readError
  const inlineSandbox = desktopPage() && (state === 'pending' || state === 'ready' || state === 'interrupted' || state === 'failed')
  const description = state === 'pending' && inlineSandbox ? '在本机沙盒窗口选择此任务并确认执行。' : state ? messages[state] : '发送等待本机授权的请求。工作台不会替你授权宿主文件或执行命令。'
  if (!canRequest && !state && !loading && !error) return null
  return <section aria-label="本机沙盒" className="rounded-xl bg-sunken px-3.5 py-3 grid gap-2 sm:grid-cols-[1fr_auto] sm:items-start">
    <div className="min-w-0">
      <h2 className="m-0 text-[14px] font-semibold">本机沙盒</h2>
      <p role="status" className="m-0 mt-1 text-[13px] text-muted leading-relaxed">
        {loading ? '正在读取本机执行状态…' : description}
        {state === 'ready' && dispatch?.changes != null ? `（${dispatch.changes} 项变更）` : ''}
      </p>
      {inlineSandbox && <button className="text-accent text-sm mt-2" type="button" onClick={() => void openDesktopSandbox().catch((e: Error) => setMutationError(e.message))}>打开本机沙盒与审阅</button>}
      {error && <p role="alert" className="m-0 mt-1 text-[13px] text-danger">{error}</p>}
    </div>
    {canRequest && <button type="button" className="btn justify-self-start" disabled={busy || loading} onClick={() => void act(false)}>{state ? '重新请求' : '请求本机沙盒'}</button>}
    {(state === 'pending' || state === 'running') && <button type="button" className="btn justify-self-start" disabled={busy} onClick={() => void act(true)}>{state === 'running' ? '请求停止' : '撤回请求'}</button>}
  </section>
}
