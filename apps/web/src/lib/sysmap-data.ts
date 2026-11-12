import { BrainSocket } from '../trust/rpc'
import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import type { SysGraph, SysRecord, SysTick } from './types'

const KEEP = 5000

/**
 * The system page's data: the graph with every module's status (refetched
 * every 30 s for machines and providers coming and going), the flow records of
 * the last 15 minutes, and the live stream that adds to both every second.
 */
export function useSystemData() {
  const [graph, setGraph] = useState<SysGraph | null>(null)
  const [records, setRecords] = useState<SysRecord[]>([])
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      setGraph(await api.systemGraph())
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    let stopped = false
    let ws: BrainSocket | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    const history = async () => {
      try {
        const h = await api.systemHistory()
        if (!stopped) setRecords((h.records ?? []).slice(-KEEP))
      } catch {
        /* the next reconnect loads it again */
      }
    }
    const connect = () => {
      if (stopped) return
      ws = new BrainSocket('/api/v1/system/stream')
      ws.onopen = () => {
        setConnected(true)
        void history()
      }
      ws.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data)
          if (msg.type !== 'sys.tick') return
          const tick = msg.payload as SysTick
          if (tick.modules) setGraph((g) => (g ? { ...g, status: { ...g.status, ...tick.modules } } : g))
          if (tick.flows?.length) setRecords((r) => [...r, ...tick.flows!].slice(-KEEP))
        } catch {
          /* a malformed frame is skipped */
        }
      }
      ws.onclose = () => {
        if (stopped) return
        setConnected(false)
        retry = setTimeout(connect, 2000)
      }
    }
    void (async () => {
      await refresh()
      await history()
      connect()
    })()
    const interval = setInterval(() => void refresh(), 30000)
    return () => {
      stopped = true
      clearTimeout(retry)
      clearInterval(interval)
      ws?.close()
    }
  }, [refresh])

  return { graph, records, connected, error, refresh }
}
