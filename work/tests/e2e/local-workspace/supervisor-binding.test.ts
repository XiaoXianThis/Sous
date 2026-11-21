import assert from 'node:assert/strict'
import { spawn, type ChildProcess } from 'node:child_process'
import { once } from 'node:events'
import { copyFile, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { createInterface } from 'node:readline'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const bin = process.env.SOUS_CORE_BIN || join(root, 'build/core', process.platform === 'win32' ? 'sous.exe' : 'sous')
const web = process.env.SOUS_WEB_BUILD || join(root, 'build/web')
const home = await mkdtemp(join(tmpdir(), 'sous-binding-e2e-'))
const profile = join(home, 'supervisor.yaml')
const reserved = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch: () => new Response() })
const port = reserved.port
reserved.stop(true)
const origin = `http://localhost:${port}`
const headers = { 'Content-Type': 'text/event-stream' }
const sse = (content: string) => `data: ${JSON.stringify({choices:[{index:0,delta:{content}}]})}\n\n`
let hub: ChildProcess | undefined
let allowProbe = false
let slowStarted = false
let shouldBePinned = true
let probes = 0
let secondProviderCalls = 0
const model = Bun.serve({ hostname: '127.0.0.1', port: 0, idleTimeout: 90, async fetch(r) {
  const body = await r.json() as { messages: { role: string; content: string }[]; tools?: unknown[] }
  const last = body.messages.findLast(m => m.role === 'user')?.content ?? ''
  if (last === 'Connection check. Reply OK only.') {
    probes++
    assert.equal(body.messages.filter(m => m.role !== 'system').length, 1)
    assert.equal(body.tools?.length ?? 0, 0)
    return allowProbe ? new Response(sse('OK')+'data: [DONE]\n\n', {headers}) : new Response(null, {status: 503})
  }
  if (last === '保持这轮运行') {
    slowStarted = true
    let timer: ReturnType<typeof setTimeout>
    return new Response(new ReadableStream({ start(c) {
      c.enqueue(new TextEncoder().encode(sse('原主管正在回答…')))
      timer = setTimeout(() => c.close(), 60000)
    }, cancel() {clearTimeout(timer)} }), {headers})
  }
  return new Response(sse(`原主管已回复：${last}`)+'data: [DONE]\n\n', {headers})
} })
const another = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch() {
  secondProviderCalls++
  assert(!shouldBePinned, 'a file edit must not redirect a selected instance or resume it after restart')
  return new Response(sse('第二个模型连接成功')+'data: [DONE]\n\n', {headers})
} })
async function writeProfile(p: number) {
  await writeFile(profile, `kind: openai-chat\nmodel: fixture\nbase_url: http://127.0.0.1:${p}\n`, { mode: 0o600 })
}
async function start() {
  hub = spawn(bin, ['hub','--app-managed','--desktop-agent-switch','--desktop-sandbox-dispatch','--home',home,'--web-dir',web,'--listen',`127.0.0.1:${port}`], { stdio: ['pipe','pipe','pipe'] })
  const child = hub
  let logs = ''
  child.stderr!.on('data', chunk => { logs = (logs + chunk).slice(-8192) })
  return new Promise<{ origin: string; setup_url: string }>((accept, reject) => {
    const reader = createInterface({ input: child.stdout!, crlfDelay: Infinity })
    const timer = setTimeout(() => reject(new Error(`Hub readiness timed out: ${logs}`)), 10000)
    child.once('error', e => { clearTimeout(timer); reject(e) })
    child.once('exit', code => { clearTimeout(timer); reject(new Error(`Hub exited: ${code} ${logs}`)) })
    reader.once('line', l => { clearTimeout(timer); try { accept(JSON.parse(l)) } catch (e) { reject(e) } })
  })
}
async function stop() {
  if (!hub || hub.exitCode !== null || hub.signalCode !== null) return
  const done = once(hub, 'exit')
  hub.stdin!.end()
  const kill = setTimeout(() => hub?.kill('SIGKILL'), 8000)
  try { await done } finally { clearTimeout(kill) }
}
try {
  await writeProfile(model.port!)
  const ready = await start()
  const browser = await chromium.launch({ executablePath: process.env.SOUS_TEST_CHROMIUM, headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    const page = await context.newPage()
    const cdp = await context.newCDPSession(page)
    await cdp.send('WebAuthn.enable')
    await cdp.send('WebAuthn.addVirtualAuthenticator', {options: {protocol:'ctap2',transport:'internal',hasResidentKey:true,hasUserVerification:true,isUserVerified:true,automaticPresenceSimulation:true}})
    await page.goto(ready.setup_url)
    await page.getByRole('button',{ name:'为本机工作区创建 Passkey' }).click()
    const composer = page.getByRole('textbox', {name:'消息', exact:true})
    await composer.waitFor()
    const fixture = await page.evaluate(async () => {
      const csrf = document.cookie.split(/;\s*/).find(x=>x.startsWith('sous_csrf='))?.slice('sous_csrf='.length) ?? ''
      async function request(method: string, path: string, body?: unknown) {
        const res = await fetch(`/api/v1${path}`, {method,headers: {'X-CSRF-Token':csrf,'Content-Type':'application/json'},body: JSON.stringify(body)})
        if (!res.ok) throw new Error(`${path}: ${res.status}`)
        return res.json()
      }
      const {threads} = await request('GET','/threads')
      const task = (await request('POST','/tasks',{title:'保留的任务',kind:'general'})).id as string
      const {dispatch} = await request('POST',`/tasks/${task}/sandbox-dispatch`,{})
      return {
        main: threads.find((t:{kind:string})=>t.kind==='main').id as string,
        project: (await request('POST','/projects',{name:'保留的项目'})).id as string,
        task, dispatch: dispatch.id as string,
        memory: (await request('POST','/memories',{kind:'note',content:'保留的记忆'})).id as string,
      }
    })
    const openSettings = async () => {
      await page.getByRole('button',{name:'设置',exact:true}).first().click()
      await page.getByRole('button',{name:'主管',exact:true}).click()
    }
    const closeSettings = () => page.getByRole('button',{name:'关闭设置',exact:true}).last().click()
    const panel = page.getByRole('region',{name:'本机主管绑定'})
    const apply = panel.getByRole('button',{name:'应用主管选择'})
    const select = (name: string) => panel.getByRole('radio',{name:new RegExp(`^${name}`)}).check()
    const caps = () => page.evaluate(async () => (await (await fetch('/api/v1/capabilities')).json()).supervisor)
    async function change(status: number) {
      const waiting = page.waitForResponse(r => r.url().endsWith('/supervisor/binding') && r.request().method()==='PUT')
      await apply.click()
      const res = await waiting
      const body = await res.json()
      assert.equal(res.status(),status,`${res.request().postData()}: ${JSON.stringify(body)}`)
      return body
    }
    const forbidden = await page.evaluate(async () => {
      const csrf = document.cookie.split(/;\s*/).find(x=>x.startsWith('sous_csrf='))?.slice('sous_csrf='.length) ?? ''
      const res = await fetch('/api/v1/supervisor/binding', {method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},
        body:JSON.stringify({selection:'acp',generation:1,command:'do not execute this fixture'})})
      return res.status
    })
    assert.equal(forbidden,400)
    assert.equal(probes,0)
    await openSettings()
    await select('Sous ACP 主管')
    assert.equal((await change(409)).error.code,'binding_unavailable')
    assert.equal((await caps()).selection,'none')
    assert.equal(probes,1)
    console.log('Synthetic provider failure did not detach the current selection')
    allowProbe = true
    assert.equal((await change(200)).generation,2)
    await closeSettings()
    await composer.fill('切换后历史应保留')
    await composer.press('Enter')
    await page.getByText('原主管已回复：切换后历史应保留',{exact:true}).waitFor()
    console.log('Activated ACP without restarting the workspace, and got a streamed reply')
    await writeProfile(another.port!)
    await composer.fill('保持这轮运行')
    await composer.press('Enter')
    const end = Date.now() + 10000
    while (!slowStarted && Date.now() < end) await Bun.sleep(25)
    assert(slowStarted && secondProviderCalls===0, 'the old selected provider, not the edited profile, must receive the live turn')
    await openSettings()
    await select('兼容主管')
    assert.equal((await change(409)).error.code,'binding_busy')
    assert.equal((await caps()).generation,2)
    await closeSettings()
    const cancelled = page.waitForResponse(r=>r.url().endsWith('/supervisor/cancel')&&r.status()===202)
    await page.getByRole('button',{name:'停止本轮',exact:true}).click()
    await cancelled
    await page.getByText('已停止本轮。已经保存的任务不会撤销。',{exact:true}).waitFor()
    console.log('Blocked switch while the old pinned ACP instance was streaming')
    await stop()
    await start()
    await page.reload()
    await composer.waitFor()
    const degraded = await caps()
    assert.equal(degraded.selection,'acp')
    assert.equal(degraded.state,'unbound')
    assert(degraded.notice)
    assert.equal(secondProviderCalls,0)
    shouldBePinned = false
    await openSettings()
    assert.equal((await change(200)).generation,3)
    assert.equal(secondProviderCalls,1)
    await select('不使用主管')
    assert.equal((await change(200)).selection,'none')
    assert.equal((await caps()).generation,4)
    await closeSettings()
    const kept = await page.evaluate(async ids => {
      async function get(path: string) {
        const res = await fetch('/api/v1'+path)
        if (!res.ok) throw new Error(`${path}: ${res.status}`)
        return res.json()
      }
      const history = await get(`/threads/${ids.main}/messages`)
      const {task} = await get(`/tasks/${ids.task}`)
      const {dispatch} = await get(`/tasks/${ids.task}/sandbox-dispatch`)
      const projects = await get('/projects')
      const memories = await get('/memories')
      return { history: history.messages.some((m:{content:string})=>m.content==='原主管已回复：切换后历史应保留'), task:task.id,
        dispatch: dispatch.id === ids.dispatch && dispatch.status === 'pending', project: projects.projects.some((p:{id:string})=>p.id===ids.project), memory: memories.memories.some((m:{id:string})=>m.id===ids.memory) }
    }, fixture)
    assert.deepEqual(kept,{history:true,task:fixture.task,dispatch:true,project:true,memory:true})
    console.log('Real browser + ACP subprocess: probe failure preserves old binding; busy fence and cancellation; changed on-disk profile cannot reroute; explicit reconnect and detach preserve workspace history and IDs.')
    if (process.env.SOUS_TEST_BIND_LIVE_PROFILE) {
      await copyFile(process.env.SOUS_TEST_BIND_LIVE_PROFILE, profile)
      await openSettings()
      await select('Sous ACP 主管')
      assert.equal((await change(200)).module, 'acp')
      await closeSettings()
      await page.evaluate(() => new Promise<void>((resolve, reject) => {
        const ws = new WebSocket(`ws://${location.host}/ws?since=0`)
        ws.addEventListener('open', () => resolve(), {once:true})
        ws.addEventListener('error', () => reject(new Error('could not subscribe to local Hub')), {once:true})
        ws.addEventListener('message', e => {
          if (JSON.parse(e.data).type === 'message.assistant.delta') {
            document.documentElement.dataset.liveBindingReply = 'yes'
            ws.close()
          }
        })
      }))
      await composer.fill('请只回复「本机模型切换验证通过」，不要调用工具。')
      await composer.press('Enter')
      await page.waitForFunction(() => document.documentElement.dataset.liveBindingReply === 'yes', undefined, { timeout: 90000 })
      await page.waitForFunction(async id => {
        const { messages } = await (await fetch(`/api/v1/threads/${id}/messages`)).json()
        return messages.some((m: {role:string; content:string; error?:boolean}) => m.role==='assistant' && !m.error && m.content.includes('本机模型切换验证通过'))
      },fixture.main,{timeout:90000})
      console.log('Live local model passed explicit connection check, streamed in the browser and persisted its reply after hot binding.')
    }

  } finally { await browser.close() }
} finally {
  await stop()
  model.stop(true)
  another.stop(true)
  await rm(home,{recursive:true,force:true})
}
