import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import { openTopic, ui, useChat, useWb } from '../lib/app'
import { MAX_FILES } from '../lib/attachments'
import { noteGroups } from '../lib/notes'
import { ago } from '../lib/time'
import type { Attachment, Note } from '../lib/types'
import Icon from './Icon'
import Markdown from './Markdown'
import { Attachments, Clamp } from './Message'
import NoteSendDialog from './NoteSendDialog'
import { Dialog, DialogContent, DialogTitle } from './ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from './ui/dropdown-menu'

const TABS: [Note['status'], string][] = [
  ['open', '未发送'],
  ['sent', '已发送'],
  ['archived', '已归档'],
]

const EMPTY: Record<Note['status'], string> = {
  open: '还没有笔记。想到什么就记在上面，需要时再发给 sous。',
  sent: '还没有发出过笔记。',
  archived: '没有归档的笔记。',
}

/**
 * The notepad: write a thought down at any time, then hand some of them to
 * sous when ready (work/specs/notes-meetings-agent-chat). A modal over the page.
 */
export default function NotesDialog() {
  const chat = useChat()
  const wb = useWb()
  const projects = (wb.data?.projects ?? []).filter((p) => !p.archived_at)
  const projectName = (id?: string) => projects.find((p) => p.id === id)?.name
  const [notes, setNotes] = useState<Note[]>([])
  const [text, setText] = useState('')
  const [files, setFiles] = useState<Attachment[]>([])
  const [project, setProject] = useState(ui.state.route.view === 'project' ? ui.state.route.id : '')
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const [tab, setTab] = useState<Note['status']>('open')
  const [selected, setSelected] = useState<string[]>([])
  const [editing, setEditing] = useState('')
  const [editText, setEditText] = useState('')
  const [sending, setSending] = useState(false)
  const [flash, setFlash] = useState('')
  const input = useRef<HTMLTextAreaElement>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const close = () => {
    ui.state.notesOpen = false
    ui.emit()
  }
  const say = (m: string) => {
    setFlash(m)
    setTimeout(() => setFlash(''), 2400)
  }
  useEffect(() => {
    const timer = setTimeout(
      () => {
        api
          .notes({ ...(filter && { project_id: filter }), ...(query && { q: query }) })
          .then((x) => setNotes(x.notes))
          .catch((e) => say(e instanceof Error ? e.message : String(e)))
      },
      query ? 300 : 0,
    )
    return () => clearTimeout(timer)
  }, [query, filter, chat.notesRev])

  const grow = (el: HTMLTextAreaElement) => {
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 220)}px`
  }
  const save = async () => {
    if (!text.trim() && files.length === 0) return
    try {
      await api.createNote(text, project, files.map((a) => a.id))
      setText('')
      setFiles([])
      if (input.current) {
        input.current.style.height = 'auto'
        input.current.focus()
      }
      setTab('open')
    } catch (e) {
      say(e instanceof Error ? e.message : String(e))
    }
  }
  const upload = async (list: FileList | null) => {
    if (!list) return
    try {
      const room = MAX_FILES - files.length
      const added = await Promise.all(Array.from(list).slice(0, room).map((f) => api.upload(f, f.name)))
      setFiles((old) => [...old, ...added])
    } catch (e) {
      say(e instanceof Error ? e.message : String(e))
    }
  }
  const toggle = (id: string) => setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]))
  const groups = noteGroups(notes)
  const shown = groups[tab]

  const projectMenu = (value: string, change: (v: string) => void, none: string, label: string) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button className="btn ghost small max-w-[200px]" aria-label={label}>
          <Icon name="folder" size={14} className="shrink-0" />
          <span className="truncate">{projectName(value) ?? none}</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-[50dvh] overflow-y-auto">
        <DropdownMenuItem onSelect={() => change('')}>{none}</DropdownMenuItem>
        {projects.map((p) => (
          <DropdownMenuItem key={p.id} onSelect={() => change(p.id)}>
            {p.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )

  return (
    <>
      <Dialog open onOpenChange={(o) => !o && close()}>
        <DialogContent
          aria-label="记事本"
          className="sheet wide !p-0 overflow-hidden !flex-none grid grid-rows-[auto_auto_minmax(0,1fr)_auto_auto] h-[min(760px,88dvh)]"
          onOpenAutoFocus={(e) => {
            e.preventDefault()
            input.current?.focus()
          }}
        >
          <header className="flex items-center gap-3 px-5 pt-4 pb-3">
            <DialogTitle className="text-[17px] font-semibold m-0">记事本</DialogTitle>
            <span className="text-[13px] text-muted">{groups.open.length} 条没发出</span>
            {flash && <span className="pill rise">{flash}</span>}
            <span className="flex-1" />
            <button className="icon-btn" data-back aria-label="关闭记事本" onClick={close}>
              <Icon name="x" size={18} />
            </button>
          </header>

          <div className="flex items-center gap-2 px-5 pt-4 pb-3 flex-wrap">
            <div className="flex rounded-[10px] bg-sunken p-[3px]" role="tablist" aria-label="笔记分组">
              {TABS.map(([key, label]) => (
                <button
                  key={key}
                  role="tab"
                  aria-selected={tab === key}
                  className={`h-[28px] px-3 rounded-[8px] text-[12.5px] ${tab === key ? 'bg-panel shadow-[var(--shadow-sm)] font-semibold text-ink' : 'text-muted hover:text-ink'}`}
                  onClick={() => {
                    setTab(key)
                    setSelected([])
                  }}
                >
                  {label} <span className="tabular-nums opacity-70">{groups[key].length}</span>
                </button>
              ))}
            </div>
            <label className="flex items-center gap-2 h-[34px] px-3 rounded-[10px] bg-sunken flex-1 min-w-[160px] focus-within:ring-2 focus-within:ring-accent/40">
              <Icon name="search" size={14} className="text-faint" />
              <input className="bg-transparent border-0 outline-none focus-visible:!shadow-none text-[16px] md:text-[13px] w-full" placeholder="搜索笔记" value={query} onChange={(e) => setQuery(e.target.value)} />
            </label>
            {projectMenu(filter, setFilter, '全部项目', '只看项目')}
          </div>

          <div className="overflow-y-auto overscroll-contain px-5 pb-4 grid gap-2 content-start">
            {shown.length === 0 && (
              <div className="grid justify-items-center gap-2 py-14 text-[13px] text-muted text-center">
                <Icon name="note" size={22} className="text-faint" />
                {query ? '没有找到' : EMPTY[tab]}
              </div>
            )}
            {shown.map((n) => {
              const on = selected.includes(n.id)
              return (
                <article key={n.id} className={`group rounded-[14px] bg-sunken p-3 flex gap-3 ${on ? 'shadow-[0_0_0_2px_var(--accent)]' : ''}`}>
                  <button
                    role="checkbox"
                    aria-checked={on}
                    aria-label="选择这条笔记"
                    className={`mt-[2px] w-[18px] h-[18px] rounded-full grid place-items-center shrink-0 ${on ? 'bg-accent text-[var(--accent-text)]' : 'border-[1.5px] border-faint'}`}
                    onClick={() => toggle(n.id)}
                  >
                    {on && <Icon name="check" size={12} />}
                  </button>
                  <div className="min-w-0 flex-1 grid gap-2">
                    {editing === n.id ? (
                      <textarea
                        autoFocus
                        className="w-full resize-none rounded-[10px] bg-panel p-2 text-[16px] outline-none focus-visible:!shadow-none ring-2 ring-accent/40"
                        rows={3}
                        value={editText}
                        onChange={(e) => {
                          setEditText(e.target.value)
                          grow(e.currentTarget)
                        }}
                        onKeyDown={(e) => {
                          if (e.key === 'Escape') {
                            e.stopPropagation()
                            setEditing('')
                          }
                          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                            e.preventDefault()
                            void api.updateNote(n.id, { text: editText }).then(() => setEditing(''))
                          }
                        }}
                      />
                    ) : (
                      n.text && (
                        <div className="text-[14px] selectable">
                          <Clamp max={160}>
                            <Markdown text={n.text} />
                          </Clamp>
                        </div>
                      )
                    )}
                    {!!n.attachments?.length && <Attachments list={n.attachments} />}
                    <div className="flex items-center gap-2 flex-wrap text-[12px] text-faint">
                      {projectName(n.project_id) && (
                        <span className="inline-flex items-center gap-1">
                          <Icon name="folder" size={12} />
                          {projectName(n.project_id)}
                        </span>
                      )}
                      <span>{ago(n.updated_at)}</span>
                      {n.sends?.map((s) => (
                        <button key={s.message_id} className="link text-[12px]" onClick={() => (close(), void openTopic(s.topic_id))}>
                          发到 {chat.threads.find((t) => t.id === s.thread_id)?.title ?? '对话'}
                        </button>
                      ))}
                    </div>
                  </div>
                  <div className="flex items-start gap-0.5 opacity-0 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100">
                    <button className="icon-btn" aria-label="编辑" data-tip="编辑" onClick={() => (setEditing(n.id), setEditText(n.text))}>
                      <Icon name="edit" size={16} />
                    </button>
                    <button
                      className="icon-btn"
                      aria-label={n.status === 'archived' ? '放回' : '归档'}
                      data-tip={n.status === 'archived' ? '放回未发送' : '归档'}
                      onClick={() => void api.updateNote(n.id, { status: n.status === 'archived' ? 'open' : 'archived' })}
                    >
                      <Icon name={n.status === 'archived' ? 'unarchive' : 'archive'} size={16} />
                    </button>
                    <button
                      className="icon-btn"
                      aria-label="删除"
                      data-tip="删除"
                      onClick={() => {
                        if (window.confirm('删除这条笔记？删除后不能恢复。')) void api.deleteNote(n.id)
                      }}
                    >
                      <Icon name="trash" size={16} />
                    </button>
                  </div>
                </article>
              )
            })}
          </div>

          {selected.length > 0 ? (
            <footer className="flex items-center gap-2 px-5 py-3 border-t border-line">
              <span className="text-[13px]">已选 {selected.length} 条</span>
              <span className="flex-1" />
              <button className="btn ghost small" onClick={() => setSelected([])}>
                取消选择
              </button>
              <button
                className="btn small"
                onClick={() => {
                  ui.state.noteSend = selected
                  ui.emit()
                  setSending(true)
                }}
              >
                发给…
              </button>
            </footer>
          ) : (
            <span />
          )}

          <div className="px-5 pt-2 pb-4 border-t border-line">
            <div className="rounded-[14px] bg-sunken p-3 grid gap-2 focus-within:ring-2 focus-within:ring-accent/40">
              <textarea
                ref={input}
                rows={2}
                className="w-full resize-none bg-transparent border-0 outline-none focus-visible:!shadow-none text-[16px] leading-[1.5] placeholder:text-faint"
                placeholder="想到什么就记下来…"
                value={text}
                onChange={(e) => {
                  setText(e.target.value)
                  grow(e.currentTarget)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault()
                    void save()
                  }
                }}
              />
              {files.length > 0 && <Attachments list={files} />}
              <div className="flex items-center gap-2">
                <button className="icon-btn" aria-label="添加附件" data-tip="添加附件" onClick={() => fileInput.current?.click()}>
                  <Icon name="paperclip" size={17} />
                </button>
                <input ref={fileInput} type="file" multiple hidden onChange={(e) => (void upload(e.target.files), (e.target.value = ''))} />
                {projectMenu(project, setProject, '不挂项目', '挂到项目')}
                <span className="flex-1" />
                <span className="text-[12px] text-faint max-[760px]:hidden">Enter 保存 · Shift+Enter 换行</span>
                <button className="btn small" disabled={!text.trim() && files.length === 0} onClick={() => void save()}>
                  记下
                </button>
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>
      {sending && (
        <NoteSendDialog
          notes={notes.filter((n) => selected.includes(n.id))}
          close={() => setSending(false)}
          sent={() => {
            setSending(false)
            setSelected([])
            say('已发出')
          }}
        />
      )}
    </>
  )
}
