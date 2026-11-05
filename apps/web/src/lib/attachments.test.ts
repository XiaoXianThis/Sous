import { describe, expect, it } from 'vitest'
import { filesOf, fitSize, formatSize, nameOf, readyIds, uploading, type Pending } from './attachments'

const file = (name: string, type: string) => new File(['x'], name, { type })

describe('formatSize', () => {
  it('picks a unit', () => {
    expect(formatSize(900)).toBe('900 B')
    expect(formatSize(2048)).toBe('2 KB')
    expect(formatSize(5 * 1024 * 1024)).toBe('5.0 MB')
  })
})

describe('filesOf', () => {
  const data = (files: File[], types: string[]) => ({ files, items: [], types }) as unknown as DataTransfer

  it('takes the files of a drop', () => {
    expect(filesOf(data([file('a.pdf', 'application/pdf')], ['Files']))).toHaveLength(1)
  })
  it('takes a pasted screenshot', () => {
    expect(filesOf(data([file('image.png', 'image/png')], ['Files']), { paste: true })).toHaveLength(1)
  })
  it('leaves a paste that also has text to the text', () => {
    expect(filesOf(data([file('image.png', 'image/png')], ['text/plain', 'Files']), { paste: true })).toHaveLength(0)
  })
  it('falls back to the items', () => {
    const f = file('s.png', 'image/png')
    const items = [{ kind: 'file', getAsFile: () => f }, { kind: 'string', getAsFile: () => null }]
    expect(filesOf({ files: [], items, types: [] } as unknown as DataTransfer)).toEqual([f])
  })
  it('copes with nothing', () => {
    expect(filesOf(null)).toEqual([])
  })
})

describe('nameOf', () => {
  it('names a pasted picture by its time', () => {
    expect(nameOf(file('image.png', 'image/png'), new Date(2026, 0, 2, 3, 4, 5))).toBe('粘贴的图片 030405.png')
    expect(nameOf(file('image.jpeg', 'image/jpeg'), new Date(2026, 0, 2, 13, 4, 5))).toBe('粘贴的图片 130405.jpg')
  })
  it('keeps a real name', () => {
    expect(nameOf(file('设计稿.png', 'image/png'))).toBe('设计稿.png')
  })
})

describe('fitSize', () => {
  it('leaves a small picture alone', () => {
    expect(fitSize(1200, 800, 500_000)).toBeNull()
  })
  it('scales a wide one down', () => {
    expect(fitSize(4800, 2400, 1_000_000)).toEqual({ w: 2400, h: 1200 })
  })
  it('keeps the size of a heavy picture that is not wide', () => {
    expect(fitSize(2000, 1000, 9_000_000)).toEqual({ w: 2000, h: 1000 })
  })
})

describe('pending list', () => {
  const p = (status: Pending['status'], id?: string): Pending => ({
    key: id ?? status, name: 'f', size: 1, status, attachment: id ? { id, name: 'f', mime: 'text/plain', size: 1 } : undefined,
  })
  it('sends only the ready ones and waits for uploads', () => {
    const list = [p('ready', 'A'), p('uploading'), p('error')]
    expect(readyIds(list)).toEqual(['A'])
    expect(uploading(list)).toBe(true)
    expect(uploading([p('ready', 'A')])).toBe(false)
  })
})
