// Files and pictures the user sends with a message: picking them, pasting
// them, shrinking big pictures before they are uploaded.
import type { Attachment } from './types'

/** Pictures the models accept; anything else is sent as a file. */
const IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']
export const MAX_FILE = 20 * 1024 * 1024
export const MAX_FILES = 8
/** Pictures bigger than this (bytes) or wider than MAX_SIDE (px) are shrunk. */
const SHRINK_ABOVE = 3 * 1024 * 1024
const MAX_SIDE = 2400

export const isImage = (mime: string) => IMAGE_TYPES.includes(mime)

export const attachmentUrl = (id: string) => `/api/v1/attachments/${id}`

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

/**
 * The files in a paste or drop. A paste that also carries text (copying from
 * a page puts both text and a picture on the clipboard) is left to the text.
 */
export function filesOf(data: Pick<DataTransfer, 'files' | 'items' | 'types'> | null, opts: { paste?: boolean } = {}): File[] {
  if (!data) return []
  if (opts.paste && Array.from(data.types ?? []).includes('text/plain')) return []
  const out: File[] = Array.from(data.files ?? [])
  if (out.length === 0) {
    for (const item of Array.from(data.items ?? [])) {
      const f = item.kind === 'file' ? item.getAsFile() : null
      if (f) out.push(f)
    }
  }
  return out
}

/** A pasted picture has a generic name ("image.png"); give it a useful one. */
export function nameOf(f: File, now = new Date()): string {
  if (f.name && !/^image\.\w+$/i.test(f.name)) return f.name
  const p = (n: number) => String(n).padStart(2, '0')
  const ext = f.type === 'image/jpeg' ? 'jpg' : (f.type.split('/')[1] || 'png')
  return `粘贴的图片 ${p(now.getHours())}${p(now.getMinutes())}${p(now.getSeconds())}.${ext}`
}

/** The size a picture is shrunk to, or null when it can stay. */
export function fitSize(w: number, h: number, bytes: number): { w: number; h: number } | null {
  const big = Math.max(w, h)
  if (big <= MAX_SIDE && bytes <= SHRINK_ABOVE) return null
  const k = big > MAX_SIDE ? MAX_SIDE / big : 1
  return { w: Math.max(1, Math.round(w * k)), h: Math.max(1, Math.round(h * k)) }
}

/** Shrinks a big still picture to a JPEG; any other file comes back as it is. */
export async function shrinkImage(f: File): Promise<File> {
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(f.type) || typeof createImageBitmap === 'undefined') return f
  try {
    const bmp = await createImageBitmap(f)
    const to = fitSize(bmp.width, bmp.height, f.size)
    if (!to) {
      bmp.close()
      return f
    }
    const canvas = document.createElement('canvas')
    canvas.width = to.w
    canvas.height = to.h
    const g = canvas.getContext('2d')
    if (!g) return f
    g.fillStyle = '#fff' // JPEG has no transparency
    g.fillRect(0, 0, to.w, to.h)
    g.drawImage(bmp, 0, 0, to.w, to.h)
    bmp.close()
    for (const q of [0.9, 0.75]) {
      const blob = await new Promise<Blob | null>((res) => canvas.toBlob(res, 'image/jpeg', q))
      if (blob && (blob.size < f.size || q === 0.75)) {
        return new File([blob], f.name.replace(/\.\w+$/, '') + '.jpg', { type: 'image/jpeg' })
      }
    }
  } catch {
    // An undecodable picture is sent as it is; the Hub decides what it is.
  }
  return f
}

/** One file in the composer: uploading, ready or failed. */
export interface Pending {
  key: string
  name: string
  size: number
  /** A local preview of a picture. */
  preview?: string
  status: 'uploading' | 'ready' | 'error'
  error?: string
  attachment?: Attachment
}

export const readyIds = (list: Pending[]) => list.filter((p) => p.status === 'ready' && p.attachment).map((p) => p.attachment!.id)
export const uploading = (list: Pending[]) => list.some((p) => p.status === 'uploading')
