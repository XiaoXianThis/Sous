import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { useChat } from '../lib/app'
import type { Memory, MemoryKind } from '../lib/types'

const kindNames: Record<MemoryKind, string> = { profile: '关于你', project: '项目', lesson: '经验', note: '其他' }
const kinds = Object.keys(kindNames) as MemoryKind[]

export default function Memories({ onClose }: { onClose: () => void }) {
  const chat = useChat()
  const [memories, setMemories] = useState<Memory[]>([])
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<MemoryKind | ''>('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<string | null>(null) // memory id, or 'new'
  const [draftKind, setDraftKind] = useState<MemoryKind>('profile')
  const [draftContent, setDraftContent] = useState('')
  const [saving, setSaving] = useState(false)

  async function load() {
    try {
      setError('')
      const res = await api.memories(query.trim(), query.trim() ? '' : filter)
      setMemories(filter && query.trim() ? res.memories.filter((m) => m.kind === filter) : res.memories)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  // Reload when the filter, the search, or any memory changes.
  useEffect(() => {
    const timer = setTimeout(() => {
      void load()
    }, 150)
    return () => clearTimeout(timer)
  }, [filter, query, chat.memoryRev])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (editing) setEditing(null)
        else onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [editing, onClose])

  function startEdit(m?: Memory) {
    setEditing(m?.id ?? 'new')
    setDraftKind(m?.kind ?? (filter || 'profile'))
    setDraftContent(m?.content ?? '')
  }

  async function save() {
    if (!draftContent.trim() || saving) return
    setSaving(true)
    try {
      if (editing === 'new') await api.createMemory(draftKind, draftContent)
      else if (editing) await api.updateMemory(editing, draftKind, draftContent)
      setEditing(null)
      await load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  async function remove(m: Memory) {
    if (!confirm(`删除这条记忆？\n\n${m.content}`)) return
    try {
      await api.deleteMemory(m.id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const editor = (
    <>
      <select
        className="border border-line bg-page rounded-[10px] px-2.5 py-2 outline-0 resize-y w-auto justify-self-start"
        value={draftKind}
        onChange={(e) => setDraftKind(e.currentTarget.value as MemoryKind)}
        aria-label="类型"
      >
        {kinds.map((k) => <option key={k} value={k}>{kindNames[k]}</option>)}
      </select>
      <textarea
        className="border border-line bg-page rounded-[10px] px-2.5 py-2 outline-0 resize-y w-full"
        rows={3}
        value={draftContent}
        onChange={(e) => setDraftContent(e.currentTarget.value)}
        placeholder="一条记忆只写一件事"
        aria-label="记忆内容"
      ></textarea>
      <div className="flex justify-end gap-3.5 items-center">
        <button className="link" onClick={() => setEditing(null)}>取消</button>
        <button className="btn small py-[7px] px-4" disabled={!draftContent.trim() || saving} onClick={() => void save()}>保存</button>
      </div>
    </>
  )

  return (
    <>
      <div className="backdrop" role="presentation" onClick={onClose}></div>
      <div className="sheet" role="dialog" aria-modal="true" aria-label="记忆">
        <header className="flex items-center justify-between">
          <h2 className="m-0 text-[18px]">记忆</h2>
          <button className="icon-btn" aria-label="关闭" onClick={onClose}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </header>
        <p className="text-muted text-[13px] mt-1 mb-3">sous 在对话中自动记下的长期信息。每次对话时，会带上“关于你”的全部记忆和其他相关记忆。</p>

        <div className="flex gap-2">
          <input
            type="search"
            className="flex-1 min-w-0 border border-line bg-page rounded-[10px] px-3 py-2 outline-0 focus:border-accent"
            placeholder="搜索记忆"
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            aria-label="搜索记忆"
          />
          <button className="btn small py-[7px] px-4" onClick={() => startEdit()}>新增</button>
        </div>
        <div className="flex gap-1 mt-2.5 mb-1.5 overflow-x-auto" role="tablist">
          <button
            role="tab"
            aria-selected={filter === ''}
            className={`border-0 bg-transparent text-muted px-3 py-[5px] rounded-full whitespace-nowrap text-[14px] ${filter === '' ? 'bg-sunken text-ink font-semibold' : ''}`}
            onClick={() => setFilter('')}
          >
            全部
          </button>
          {kinds.map((k) => (
            <button
              key={k}
              role="tab"
              aria-selected={filter === k}
              className={`border-0 bg-transparent text-muted px-3 py-[5px] rounded-full whitespace-nowrap text-[14px] ${filter === k ? 'bg-sunken text-ink font-semibold' : ''}`}
              onClick={() => setFilter(k)}
            >
              {kindNames[k]}
            </button>
          ))}
        </div>

        {error && <p className="text-danger text-[13px] m-0 mb-2" role="alert">{error}</p>}

        <ul className="list-none m-0 p-0 overflow-y-auto grid gap-2">
          {editing === 'new' && <li className="border border-line rounded-[12px] px-3 py-2.5 grid gap-2">{editor}</li>}
          {memories.map((m) => (
            <li key={m.id} className={`border border-line rounded-[12px] px-3 py-2.5 grid ${editing === m.id ? 'gap-2' : 'gap-1'}`}>
              {editing === m.id ? (
                editor
              ) : (
                <>
                  <span className="text-[12px] text-warm font-semibold">{kindNames[m.kind] ?? m.kind}</span>
                  <p className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{m.content}</p>
                  <div className="flex justify-end gap-3.5 items-center">
                    <button className="link" onClick={() => startEdit(m)}>编辑</button>
                    <button className="link danger" onClick={() => void remove(m)}>删除</button>
                  </div>
                </>
              )}
            </li>
          ))}
          {memories.length === 0 && !loading && editing !== 'new' && (
            <li className="text-muted text-center border border-dashed border-line rounded-[12px] px-3 py-6">
              {query ? '没有找到相关记忆' : '还没有记忆。和 sous 聊聊你的习惯和项目，它会自己记下来。'}
            </li>
          )}
        </ul>
      </div>
    </>
  )
}
