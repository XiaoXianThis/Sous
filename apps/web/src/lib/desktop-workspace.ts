import { workspaceJSON } from './workspace-transport'

const origin = 'http://localhost:8787'
const replyEvent = 'sous-desktop-reply'

type Channel = { postMessage(message: string): void }
type DesktopWindow = Window & { webkit?: { messageHandlers?: { external?: Channel } }; chrome?: { webview?: Channel }; _wails?: { invoke?: (message: string) => void } }
type Reply = { id: number; debug?: boolean; sandbox?: boolean; grant?: string; error?: string }

let next = 0
let ready = false
let state: Promise<Reply> | undefined

export function desktopPage(): boolean {
  return location.origin === origin && location.pathname === '/desktop'
}

function getChannel(): Channel | undefined {
  if (!desktopPage()) return
  const w = window as DesktopWindow
  return w.webkit?.messageHandlers?.external ?? w.chrome?.webview ?? (w._wails?.invoke ? { postMessage: message => w._wails?.invoke?.(message) } : undefined)
}

function callDesktop(action: string, code?: string): Promise<Reply> {
  return new Promise((resolve, reject) => {
    const channel = getChannel()
    if (!channel) { reject(new Error('请在 Sous 桌面主窗口中操作。')); return }
    const id = ++next
    function cleanup() {
      clearTimeout(timer)
      window.removeEventListener(replyEvent, receive)
    }
    function receive(event: Event) {
      const reply = (event as CustomEvent<Reply>).detail
      if (!reply || reply.id !== id) return
      cleanup()
      if (reply.error) reject(new Error(reply.error))
      else resolve(reply)
    }
    const timer = setTimeout(() => {
      cleanup()
      reject(new Error('桌面程序没有响应，请重新打开 Sous 工作台后重试。'))
    }, action === 'unlock' ? 120_000 : 8000)
    window.addEventListener(replyEvent, receive)
    try {
      // These HTTP assets don't load the Wails runtime. Arm its ExecJS queue
      // before requesting the first reply, just like our native sandbox page.
      if (!ready) { channel.postMessage('wails:runtime:ready'); ready = true }
      channel.postMessage('sous.desktop:' + JSON.stringify({ id, action, code }))
    } catch (e) { cleanup(); reject(e) }
  })
}

export function desktopStatus() {
  return state ??= callDesktop('status').catch((err: unknown) => { state = undefined; throw err })
}

export const showDesktopDebugCode = () => callDesktop('show-code')
export const openDesktopSandbox = () => callDesktop('open-sandbox')

export async function unlockDesktop(code?: string) {
  const status = await desktopStatus()
  if (status.debug && code === undefined) throw new Error('调试登录不能授权此受保护操作。')
  const { grant } = await callDesktop(status.debug ? 'unlock-debug' : 'unlock', code)
  if (!grant) throw new Error('桌面程序没有返回有效的登录授权。')
  const result = await workspaceJSON<{ error?: { message?: string } }>('POST', '/api/v1/auth/desktop', { grant })
  if (result.status !== 200) throw new Error(result.json?.error?.message || '登录未完成，请重试。')
}
