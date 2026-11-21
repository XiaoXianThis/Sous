import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { chromium } from 'playwright-core'

// The actual Desktop process owns the Hub, dispatch pipe, VM and writeback.
// This driver controls only the test browser and observes a synthetic project.
const setupFile = process.env.SOUS_TEST_DESKTOP_SETUP_FILE
const project = process.env.SOUS_TEST_DESKTOP_PROJECT
assert(setupFile && project, 'Use a disposable Desktop workspace and a synthetic project; see README')
const setup = new URL((await readFile(setupFile, 'utf8')).trim())
assert.equal(setup.origin, 'http://localhost:8787')
assert.equal(setup.pathname, '/setup')
assert(new URLSearchParams(setup.hash.slice(1)).get('token'), 'Expected the isolated Desktop setup URL')
const original = 'untouched original\n'
const output = join(project, 'native-result.txt')
assert.deepEqual(await readdir(project), ['note.txt'])
assert.equal(await readFile(join(project, 'note.txt'), 'utf8'), original)
const manualTimeout = 8 * 60 * 1000
const browser = await chromium.launch({ executablePath: process.env.SOUS_TEST_CHROMIUM, headless: true })
try {
  const context = await browser.newContext({ serviceWorkers: 'block' })
  const page = await context.newPage()
  const cdp = await context.newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: {
    protocol: 'ctap2', transport: 'internal', hasResidentKey: true,
    hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
  } })
  await page.goto(setup.href)
  await page.getByRole('button', { name: '为本机工作区创建 Passkey' }).click()
  await page.getByRole('textbox', { name: '消息', exact: true }).waitFor()
  const taskID = await page.evaluate(async () => {
    const csrf = document.cookie.split(/;\s*/).find(x => x.startsWith('sous_csrf='))?.slice('sous_csrf='.length) ?? ''
    const res = await fetch('/api/v1/tasks', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ title: 'Native Desktop dispatch fixture', detail: 'Task description only; never execute this text.', kind: 'general' }),
    })
    if (!res.ok) throw new Error(`Creating test task: ${res.status}`)
    return (await res.json()).id as string
  })
  assert(taskID)
  await page.goto(`${setup.origin}/#/task=${taskID}`)
  const panel = page.getByRole('region', { name: '本机沙盒' })
  await panel.getByRole('button', { name: '请求本机沙盒' }).click()
  await panel.getByText('刷新请求并选择', { exact: false }).waitFor()
  console.log("Request queued. In Desktop refresh, select the task, authorize the fixture, then run: test \"$(cat note.txt)\" = 'untouched original'; printf 'reviewed in Desktop\\n' > native-result.txt")
  await panel.getByText('1 项变更', { exact: false }).waitFor({ timeout: manualTimeout })
  assert.deepEqual(await readdir(project), ['note.txt'])
  console.log('Browser saw ready; confirmed the host is unchanged. Inspect the Desktop preview, then explicitly write back native-result.txt.')
  const until = Date.now() + manualTimeout
  while (!(await readdir(project)).includes('native-result.txt')) {
    assert(Date.now() < until, 'Timed out waiting for native writeback approval')
    await delay(300)
  }
  assert.equal(await readFile(output, 'utf8'), 'reviewed in Desktop\n')
  await panel.getByRole('button', { name: '重新请求' }).click()
  console.log('Second request queued. Refresh, select it, authorize the same fixture again and run: sleep 120; touch should-not-exist.txt')
  await panel.getByRole('button', { name: '请求停止' }).waitFor({ timeout: manualTimeout })
  await panel.getByRole('button', { name: '请求停止' }).click()
  await panel.getByText('已撤回本机执行请求。', { exact: true }).waitFor({ timeout: 35000 })
  assert.deepEqual((await readdir(project)).sort(), ['native-result.txt', 'note.txt'])
  assert.equal(await readFile(join(project, 'note.txt'), 'utf8'), original)
  const task = await page.evaluate(id => fetch(`/api/v1/tasks/${id}`).then(res => res.json()), taskID)
  assert.equal(task.task?.status, 'inbox', 'A sandbox receipt must not change the task workflow')
  console.log('PASS: actual Desktop consumed browser intent, ran Microsandbox, wrote back only after review and acknowledged a second run’s cancellation.')
} finally {
  await browser.close()
}
