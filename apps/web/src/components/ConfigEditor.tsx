import { useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import type { ConfigFile } from '../lib/types'
import Sheet from './Sheet'

const formatName: Record<string, string> = { json: 'JSON', jsonc: 'JSON（可以写注释）', toml: 'TOML', yaml: 'YAML', text: '文本' }

export default function ConfigEditor({ nodeId, agent, file, title, onClose }: {
  nodeId: string
  agent: string
  file: string
  title: string
  onClose: () => void
}) {
  const [loaded, setLoaded] = useState<ConfigFile | null>(null)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [conflict, setConflict] = useState(false)
  const [saving, setSaving] = useState(false)

  const dirty = loaded !== null && text !== loaded.content

  async function load() {
    try {
      const next = await api.readFile(nodeId, agent, file)
      setLoaded(next)
      setText(next.content)
      setError('')
      setConflict(false)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  async function reload() {
    if (dirty && !confirm('放弃你的修改，重新读取文件？')) return
    setNotice('')
    await load()
  }

  async function save() {
    if (!loaded) return
    if (loaded.format === 'json' && text.trim()) {
      try {
        JSON.parse(text)
      } catch (e) {
        setError(`JSON 格式不对：${(e as Error).message}`)
        return
      }
    }
    setSaving(true)
    try {
      const out = await api.writeFile(nodeId, agent, file, text, loaded.sha256)
      setLoaded(out)
      setText(out.content)
      setError('')
      setNotice(out.backup ? `已保存。原来的内容备份在 ${out.backup}` : '已保存。')
    } catch (e) {
      setConflict(e instanceof ApiError && e.code === 'conflict')
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  function close() {
    if (dirty && !confirm('还有没保存的修改，确定关闭？')) return
    onClose()
  }

  return (
    <Sheet title={title} onClose={close} wide>
      {loaded && (
        <>
          <p className="text-muted text-[13px] m-0 [overflow-wrap:anywhere]">
            <code className="text-[12px]">{loaded.path}</code> · {formatName[loaded.format] ?? loaded.format}{loaded.exists ? '' : ' · 文件还不存在，保存后创建'}
          </p>
          {agent === 'sous' && (
            <p className="text-muted text-[13px] m-0 [overflow-wrap:anywhere]">
              设备令牌和 API Key 显示成 <code className="text-[12px]">***</code>，保存时保持原值；hub_url、node_id、remote_admin 只能在这台电脑上改。保存后立即生效，并发数要重启 sous 后生效。
            </p>
          )}
          <textarea
            className="w-full min-h-[50dvh] resize-y box-border font-mono text-[13px] leading-[1.5] [tab-size:2] border border-line rounded-[12px] bg-sunken text-ink p-2.5"
            value={text}
            onChange={(e) => setText(e.currentTarget.value)}
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            aria-label="文件内容"
            onKeyDown={(e) => (e.metaKey || e.ctrlKey) && e.key === 's' && (e.preventDefault(), void save())}
          ></textarea>
          <div className="flex gap-3.5 items-center flex-wrap">
            <button className="btn small py-[7px] px-4" disabled={!dirty || saving} onClick={() => void save()}>{saving ? '正在保存…' : '保存'}</button>
            <button className="link" onClick={() => void reload()}>重新读取</button>
            {notice && <span className="text-muted text-[13px] m-0">{notice}</span>}
          </div>
        </>
      )}
      {!loaded && !error && <p className="text-muted text-[13px] m-0">正在读取…</p>}
      {error && (
        <p className="text-danger text-[13px] m-0" role="alert">
          {error}{conflict && <> <button className="link" onClick={() => void reload()}>重新读取</button></>}
        </p>
      )}
    </Sheet>
  )
}
