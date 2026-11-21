import assert from 'node:assert/strict'
import { spawn, type ChildProcess } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const tmp = await mkdtemp(join(tmpdir(), 'sous-workspace-'))
const binary = process.env.SOUS_CORE_BIN || join(root, 'build', 'core', process.platform === 'win32' ? 'sous.exe' : 'sous')
const web = process.env.SOUS_WEB_BUILD || join(root, 'build', 'web')
const probe = createServer()
await new Promise<void>((done, reject) => {
  probe.once('error', reject)
  probe.listen(0, '127.0.0.1', done)
})
const bound = probe.address()
assert(bound && typeof bound !== 'string')
const origin = `http://localhost:${bound.port}`
await new Promise<void>((done) => probe.close(() => done()))
const config = join(tmp, 'hub.yaml')
const liveModel = process.env.SOUS_TEST_ACP_MODEL_CONFIG
const legacy = process.env.SOUS_TEST_LEGACY === '1'
const acp = !!liveModel || process.env.SOUS_TEST_ACP === '1'
assert(!(legacy && acp), 'select one supervisor test mode')
let agentMode = acp ? 'acp' : legacy ? 'legacy' : 'none'
const modelConfig = liveModel || join(tmp, 'model.yaml')
let slowModelStarted = false
let draftID: string | undefined
let savedDraft: string | undefined
const model = acp && !liveModel ? Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(r) {
  if (new URL(r.url).pathname !== '/chat/completions') return new Response('not found', { status: 404 })
  const { messages, tools } = await r.json() as { messages: { role: string; content: string }[], tools: {function: {name: string}}[] }
  const last = messages.findLast(m => m.role === 'user')?.content ?? ''
  const results = messages.filter(m => m.role === 'tool')
  const sse = (delta: unknown) => `data: ${JSON.stringify({choices:[{index:0,delta}]})}\n\n`
  const headers = { 'Content-Type': 'text/event-stream' }
  if (last.includes('长回复以便取消')) {
    slowModelStarted = true
    let timer: ReturnType<typeof setTimeout>
    return new Response(new ReadableStream({ start(c) {
      c.enqueue(new TextEncoder().encode(sse({ content: '正在生成一条长回复…' })))
      timer = setTimeout(() => c.close(), 12000)
    }, cancel() { clearTimeout(timer) } }), { headers })
  }
  if (last.includes('只登记验收草稿') && results.length === 0) {
    assert.deepEqual(tools.map(t => t.function.name).sort(), ['current_time', 'memory_search', 'project_list', 'task_create'])
    return new Response(sse({ tool_calls: [
      { index: 0, id: 'bad', type: 'function', function: { name: 'task_create', arguments: JSON.stringify({ title: '不能创建或派发', dispatch: true }) } },
      { index: 1, id: 'list', type: 'function', function: { name: 'project_list', arguments: '{}' } },
      { index: 2, id: 'create', type: 'function', function: { name: 'task_create', arguments: JSON.stringify({ title: 'ACP 实际登记的任务', detail: '走 MCP 草稿工具，不执行' }) } },
      { index: 3, id: 'repeat', type: 'function', function: { name: 'task_create', arguments: JSON.stringify({ title: 'ACP 实际登记的任务', detail: '走 MCP 草稿工具，不执行' }) } },
    ] }) + 'data: [DONE]\n\n', { headers })
  }
  if (results.length) {
    assert.equal(results.length, 4)
    assert.match(results[0].content, /不允许/)
    assert.match(results[1].content, /本机验收项目/)
    assert.match(results[2].content, /未派发/)
    assert.equal(results[2].content, results[3].content)
    draftID = /[0-7][0-9A-HJKMNP-TV-Z]{25}/.exec(results[2].content)?.[0]
    assert(draftID, 'draft must return a durable task ID')
  }
  const content = results.length ? 'ACP 草稿操作已完成。' : `ACP 本机模型链路确认：${last}`
  return new Response(sse({ content }) + 'data: [DONE]\n\n', { headers })
} }) : undefined
if (model) await writeFile(modelConfig, `kind: openai-chat\nmodel: local-sse-fixture\nbase_url: http://127.0.0.1:${model.port}\n`, { mode: 0o600 })
const models = legacy ? 'providers:\n  - name: mock\n    kind: mock\nroles:\n  supervisor: mock/echo\n' : ''
await writeFile(config, `listen: 127.0.0.1:${bound.port}\npublic_url: ${origin}\n${models}`, { mode: 0o600 })
let child: ChildProcess | null = null

async function start() {
  const proc = spawn(binary, ['hub', '--app-managed', '--config', config, '--web-dir', web, '--agent', agentMode, ...(agentMode === 'acp' ? ['--agent-config', modelConfig] : [])], { stdio: ['pipe', 'pipe', 'pipe'] })
  child = proc
  let log = '', ready = ''
  proc.stderr?.on('data', (b) => { log += b.toString() })
  proc.stdout?.on('data', (b) => { ready += b.toString() })
  let spawnError: Error | undefined
  proc.on('error', (e) => { spawnError = e })
  for (let i = 0; i < 100; i++) {
    if (spawnError) throw spawnError
    if (proc.exitCode !== null) throw new Error(`Hub exited: ${log.replace(/#token=\S+/g, '#token=[redacted]')}`)
    if (ready.includes('\n') && (await fetch(`${origin}/healthz`).catch(() => null))?.ok) {
      const record = JSON.parse(ready.trim())
      assert.equal(record.origin, origin)
      assert(!log.includes('#token='), 'managed startup must not log the setup token')
      return record.setup_url || undefined
    }
    await Bun.sleep(50)
  }
  throw new Error('Hub startup timeout')
}

async function until<T>(check: () => Promise<T | null | false>, label: string, timeoutMs = 30000): Promise<T> {
  const untilAt = Date.now() + timeoutMs
  do {
    const found = await check()
    if (found) return found
    await Bun.sleep(200)
  } while (Date.now() < untilAt)
  throw new Error(`Timed out waiting for ${label}`)
}

async function stop() {
  const proc = child
  child = null
  if (!proc || !proc.pid || proc.exitCode !== null || proc.signalCode !== null) return
  const exit = once(proc, 'exit')
  const timeout = setTimeout(() => proc.kill('SIGKILL'), 5000)
  try { proc.stdin?.end(); await exit } finally { clearTimeout(timeout) }
}

try {
  const setup = await start()
  assert(setup, 'a new work area must issue a setup link')
  const browser = await chromium.launch({ executablePath: process.env.SOUS_TEST_CHROMIUM, headless: true })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } })
    const page = await ctx.newPage()
    const external: string[] = []
    await ctx.route('**/*', async (route) => {
      if (new URL(route.request().url()).origin === origin) return route.continue()
      external.push(route.request().url())
      return route.abort()
    })
    // Exercise real WebAuthn ceremonies with Chromium's software authenticator;
    // never bypass authentication or use a real Passkey.
    const cdp = await ctx.newCDPSession(page)
    await cdp.send('WebAuthn.enable')
    await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
      protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true,
      isUserVerified: true, automaticPresenceSimulation: true,
    } })
    await page.goto(setup)
    await page.getByRole('button', { name: '为本机工作区创建 Passkey' }).click()
    await page.getByRole('textbox', { name: '消息', exact: true }).waitFor()
    if (!legacy && !acp) await page.getByText('未连接主管。消息会保存', { exact: false }).waitFor()
    if (acp) await page.getByText('ACP 主管预览', { exact: false }).waitFor()
    assert(!page.url().includes('token='), 'setup token must disappear after registration')

    await page.locator('[title="在线"]').first().waitFor()
    if (legacy || acp) {
      await page.evaluate(() => new Promise<void>((resolve, reject) => {
        const stream = new WebSocket(`ws://${location.host}/ws?since=0`)
        stream.addEventListener('open', () => resolve(), { once: true })
        stream.addEventListener('error', () => reject(new Error('local event connection failed')), { once: true })
        stream.addEventListener('message', (event) => {
          if (JSON.parse(event.data).type === 'message.assistant.delta') {
            document.documentElement.dataset.checkedReply = 'yes'
            stream.close()
          }
        })
      }))
    }
    const text = liveModel ? '请只回复「本机推理已连通」，不要调用工具。' : '本机验收：在重启之后也应看到这条消息'
    const composer = page.getByRole('textbox', { name: '消息', exact: true })
    await composer.fill(text)
    const saved = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/messages'))
    await composer.press('Enter')
    assert.equal((await saved).status(), 202)
    await page.getByText(text, { exact: true }).first().waitFor()
    if (legacy || acp) {
      await until(() => page.evaluate(async ({ userText, marker, live }) => {
        const { threads } = await (await fetch('/api/v1/threads')).json()
        const main = threads.find((t: { kind: string }) => t.kind === 'main')
        if (!main) return false
        const { messages } = await (await fetch(`/api/v1/threads/${main.id}/messages`)).json()
        return messages.some((m: { role: string; content: string }) => m.role === 'assistant' && m.content.includes(marker) && (live || m.content.includes(userText)))
      }, { userText: text, marker: liveModel ? '本机推理已连通' : acp ? 'ACP 本机模型链路确认' : '（模拟模型）收到：', live: !!liveModel }), 'the persisted supervisor reply', 90000)
      await page.waitForFunction(() => document.documentElement.dataset.checkedReply === 'yes')
    } else {
      assert.equal(await page.getByText('正在思考', { exact: true }).count(), 0, 'unbound workspace cannot be thinking')
    }
    console.log('Passkey registration, browser chat and persisted acknowledgement OK')

    await page.getByRole('button', { name: '新建或登记项目', exact: true }).click()
    const projectForm = page.getByRole('dialog', { name: '登记项目' })
    await projectForm.getByRole('textbox', { name: '名字' }).fill('本机验收项目')
    await projectForm.getByRole('button', { name: '保存', exact: true }).click()
    await page.getByRole('heading', { name: '本机验收项目', exact: true }).first().waitFor()
    await page.getByRole('button', { name: '新任务', exact: true }).first().click()
    await page.getByRole('textbox', { name: '标题', exact: true }).fill('本机手动任务')
    await page.getByRole('button', { name: '只创建', exact: true }).click()
    await page.getByText('本机手动任务', { exact: true }).first().waitFor()
    await page.goto(origin)
    console.log('Create project and task without choosing a machine OK')
    if (acp) {
      await page.locator('[title="在线"]').first().waitFor()
      await composer.fill(liveModel ? '请用 task_create 创建一条明确的待分配任务，标题是「本机模型草稿」，无需指定项目，只登记，不派发。' : '只登记验收草稿')
      await composer.press('Enter')
      if (!liveModel) await until(() => page.evaluate(async () => {
        const {threads} = await (await fetch('/api/v1/threads')).json()
        const main = threads.find((t: {kind: string}) => t.kind === 'main')
        const {messages} = await (await fetch(`/api/v1/threads/${main.id}/messages`)).json()
        return messages.some((m: {role: string; content: string}) => m.role === 'assistant' && m.content === 'ACP 草稿操作已完成。')
      }), 'the completed ACP tool turn')
      const title = liveModel ? '本机模型草稿' : 'ACP 实际登记的任务'
      savedDraft = await until(() => page.evaluate(async (title) => {
        const { tasks } = await (await fetch('/api/v1/tasks?status=inbox', { cache: 'no-store' })).json()
        if (tasks.some((t: {title: string}) => t.title === '不能创建或派发')) throw new Error('forbidden draft exists')
        const found = tasks.filter((t: {title: string}) => t.title === title)
        if (found.length > 1) throw new Error('draft was created more than once')
        return found[0]?.id as string | undefined || null
      }, title), 'the persisted inbox draft', 90000)
      if (!liveModel) assert.equal(savedDraft, draftID)
      if (!liveModel) {
        await composer.fill('长回复以便取消')
        await composer.press('Enter')
        const stopTurn = page.getByRole('button', { name: '停止本轮', exact: true })
        await stopTurn.waitFor()
        // Do not race cancellation against the prompt reaching the provider.
        for (let i = 0; !slowModelStarted && i < 150; i++) await Bun.sleep(25)
        assert(slowModelStarted, 'slow turn must have started at the model')
        const stopped = page.waitForResponse(r => r.url().endsWith('/supervisor/cancel') && r.request().method() === 'POST')
        await stopTurn.click()
        assert.equal((await stopped).status(), 202)
        await until(() => page.evaluate(async () => {
          const {threads} = await (await fetch('/api/v1/threads')).json()
          const main = threads.find((t: {kind: string}) => t.kind === 'main')
          const {messages} = await (await fetch(`/api/v1/threads/${main.id}/messages`)).json()
          return messages.some((m: {content: string}) => m.content.startsWith('已停止本轮'))
        }), 'the persisted cancellation')
      }
      agentMode = 'none'
      console.log(liveModel ? 'Real model completed a streamed reply and filed a draft via ACP and MCP' : 'First-party ACP process, streamed output, actual MCP draft, rejected dispatch, replay deduplication and cancel OK')
    }

    await stop()
    assert.equal(await start(), undefined, 'registered work area must not issue a new setup link')
    await page.reload()
    if (acp) {
      await page.getByText('未连接主管。消息会保存', { exact: false }).waitFor()
      const task = await page.evaluate(async (id) => {
        const response = await fetch(`/api/v1/tasks/${id}`, { cache: 'no-store' })
        return response.ok ? (await response.json()).task : null
      }, savedDraft)
      assert.equal(task?.id, savedDraft, 'switching or restarting must preserve the ACP draft')
    }
    await page.getByText(text, { exact: true }).first().waitFor()
    const second = await ctx.newPage()
    await second.goto(origin)
    await second.getByRole('textbox', { name: '消息', exact: true }).waitFor()
    await page.getByRole('button', { name: '设置', exact: true }).click()
    await page.getByRole('button', { name: '账号', exact: true }).click()
    await page.getByRole('button', { name: '锁定', exact: true }).click()
    await page.getByRole('heading', { name: 'Sous 本机工作区' }).waitFor()
    await second.getByRole('heading', { name: 'Sous 本机工作区' }).waitFor()
    await second.close()
    await page.getByRole('button', { name: '用 Passkey 解锁' }).click()
    await page.getByText(text, { exact: true }).first().waitFor()
    await page.setViewportSize({ width: 390, height: 844 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'narrow viewport must not overflow')
    assert.deepEqual(external, [], 'local workspace must not request cloud assets or APIs')
    console.log('Restart persistence, lock, Passkey sign-in and narrow layout OK')
  } finally {
    await browser.close()
  }
} finally {
  await stop()
  model?.stop(true)
  await rm(tmp, { recursive: true, force: true })
}
