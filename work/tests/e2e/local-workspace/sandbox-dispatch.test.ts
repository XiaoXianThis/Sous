import assert from 'node:assert/strict'
import { spawn, type ChildProcess } from 'node:child_process'
import { once } from 'node:events'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { createInterface } from 'node:readline'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const binary = process.env.SOUS_CORE_BIN || join(root, 'build/core/sous')
const helper = process.env.SOUS_SANDBOX_BIN || join(root, 'build/core/sous-sandbox')
const web = process.env.SOUS_WEB_BUILD || join(root, 'build/web')
const msb = process.env.MSB_PATH, fw = process.env.MSB_LIBKRUNFW_PATH, archive = process.env.SOUS_TEST_MSBOX_IMAGE_ARCHIVE
assert(msb && fw && archive, 'Supply verified MSB_PATH, MSB_LIBKRUNFW_PATH and SOUS_TEST_MSBOX_IMAGE_ARCHIVE')
const dir = await mkdtemp('/private/tmp/sd-e2e-')
const project = join(dir, 'project'), out = join(dir, 'results')
await mkdir(project); await mkdir(out)
await writeFile(join(project, 'note.txt'), 'host original\n')
const probe = Bun.serve({ port: 0, hostname: '127.0.0.1', fetch: () => new Response('probe') })
const port = probe.port
probe.stop(true)
const origin = `http://localhost:${port}`
const hub = spawn(binary, ['hub', '--home', dir, '--web-dir', web, '--listen', `127.0.0.1:${port}`, '--app-managed', '--desktop-sandbox-dispatch'], { stdio: ['pipe','pipe','pipe'] })
let logs = ''
hub.stderr.on('data', s => { logs = (logs + s).slice(-8192) })
const lineIterator = createInterface({input: hub.stdout,crlfDelay: Infinity})[Symbol.asyncIterator]()
let rpcID = 0
function timeout<T>(p: Promise<T>, ms: number, label: string) {
  let stop: ReturnType<typeof setTimeout>
  return Promise.race([p,new Promise<never>((_,reject)=>{ stop = setTimeout(()=>reject(new Error(`${label} timed out`)),ms) })]).finally(()=>clearTimeout(stop))
}
async function line() {
  const x = await timeout(lineIterator.next(), 10000, 'managed protocol')
  assert(!x.done, `unexpected hub exit: ${logs}`)
  return JSON.parse(x.value)
}
async function rpc(action: string, extra: Record<string, unknown> = {}) {
  const id = ++rpcID
  hub.stdin.write(JSON.stringify({id, action, ...extra}) + '\n')
  const r = await line()
  assert.equal(r.id, id)
  if (r.error) throw new Error(r.error)
  return r.data
}

const children = new Set<ChildProcess>()
function boot(script: string) {
  const proc = spawn(helper, ['--parent-managed','--runtime-home',join(dir,'vm'),'--source',project,'--output',out,'--msb',msb!,'--firmware',fw!,'--image-archive',archive!,'--','/bin/sh','-ec',script], {stdio:['pipe','pipe','pipe']})
  children.add(proc)
  let text = '', output = ''
  proc.stderr.on('data', b => { text += b.toString() })
  proc.stdout.on('data', b => { output += b.toString() })
  const done = once(proc,'close').then(([code])=>{children.delete(proc); return {code, text, report:output ? JSON.parse(output) : null}})
  return {proc, done, starts: async () => {
    for(let i=0;i<120;i++) {
      if(text.includes('正在 Guest 内运行命令'))return
      if(proc.exitCode !== null) break
      await Bun.sleep(250)
    }
    throw new Error(`Guest did not start: ${text}`)
  }}
}

try {
  const ready = await line()
  assert.equal(ready.origin,origin)
  assert.equal(ready.sandbox_dispatch,1)
  assert(ready.setup_url)
  const browser = await chromium.launch({executablePath:process.env.SOUS_TEST_CHROMIUM,headless:true})
  try {
    const context = await browser.newContext({viewport:{width:1150,height:820}, serviceWorkers:'block'})
    await context.route('**/*', route => new URL(route.request().url()).origin===origin ? route.continue() : route.abort())
    const page = await context.newPage()
    const cdp = await context.newCDPSession(page)
    await cdp.send('WebAuthn.enable')
    await cdp.send('WebAuthn.addVirtualAuthenticator',{options:{protocol:'ctap2',transport:'internal',hasResidentKey:true,hasUserVerification:true,isUserVerified:true,automaticPresenceSimulation:true}})
    await page.goto(ready.setup_url)
    await page.getByRole('button',{name:'为本机工作区创建 Passkey'}).click()
    await page.getByRole('textbox',{name:'消息',exact:true}).waitFor()
    const task = await page.evaluate(async () => {
      const csrf=document.cookie.split(/;\s*/).find(x=>x.startsWith('sous_csrf='))?.slice('sous_csrf='.length) ?? ''
      const res=await fetch('/api/v1/tasks',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({title:'Dispatch boundary fixture',detail:'This is task data only, not a shell command.',kind:'general'})})
      return res.json()
    })
    assert(task.id)
    await page.goto(`${origin}/#/task=${task.id}`)
    const panel = page.getByRole('region',{name:'本机沙盒'})
    await panel.getByRole('button',{name:'请求本机沙盒'}).click()
    await panel.getByText('刷新请求并选择', {exact:false}).waitFor()
    const jobs=await rpc('list')
    assert.equal(jobs.length,1)
    const job=jobs[0]
    assert.equal(job.task_id,task.id)
    assert.equal(job.detail,'This is task data only, not a shell command.')
    assert.deepEqual(await readdir(out),[])
    const browserFields=await page.evaluate(async id=>{
      const data=await (await fetch(`/api/v1/tasks/${id}/sandbox-dispatch`)).json()
      return Object.keys(data.dispatch).sort()
    },task.id)
    assert.deepEqual(browserFields,['changes','expires_at','id','status','task_id','updated_at'])
    const lease=await rpc('claim',{dispatch_id:job.id})
    assert.match(lease,/^[0-9a-f]{64}$/)
    const run=boot(`test "$(cat note.txt)" = 'host original'; printf 'VM result\n' > out.txt`)
    const result=await timeout(run.done,90000,'run completion')
    assert.equal(result.code,0,result.text)
    const manifest=JSON.parse(await readFile(join(result.report.directory,'manifest.json'),'utf8'))
    assert.equal(manifest.changes.length,1)
    assert.equal(manifest.changes[0].path,'out.txt')
    assert.equal(await readFile(join(project,'note.txt'),'utf8'),'host original\n')
    assert.deepEqual(await readdir(project),['note.txt'])
    await rpc('complete',{dispatch_id:job.id,lease,outcome:'ready',changes:1})
    await panel.getByText('1 项变更',{exact:false}).waitFor()
    console.log('Passkey and actual browser → durable intent → private pipe → real offline VM → browser ready; no implicit host writeback')

    await panel.getByRole('button',{name:'重新请求'}).click()
    await panel.getByText('刷新请求并选择',{exact:false}).waitFor()
    const [retry]=await rpc('list')
    assert.notEqual(retry.id,job.id)
    const nextLease=await rpc('claim',{dispatch_id:retry.id})
    const slow=boot('/bin/sleep 90')
    await slow.starts()
    await panel.getByRole('button',{name:'请求停止'}).click()
    await page.waitForFunction(async id => {
      const dispatch = (await (await fetch(`/api/v1/tasks/${id}/sandbox-dispatch`)).json()).dispatch
      return dispatch?.status === 'cancel_requested'
    }, task.id)
    assert.equal(await rpc('heartbeat',{dispatch_id:retry.id,lease:nextLease}),true)
    slow.proc.stdin?.end()
    const cancelled=await timeout(slow.done,25000,'Guest cleanup')
    assert.notEqual(cancelled.code,0)
    assert.deepEqual(cancelled.report,{cancelled:true,cleanup_confirmed:true})
    await rpc('complete',{dispatch_id:retry.id,lease:nextLease,outcome:'cancelled'})
    await panel.getByText('已撤回本机执行请求。',{exact:true}).waitFor()
    assert.deepEqual(await readdir(out),[result.report.directory.split('/').at(-1)])
    console.log('Browser cancellation reached the owning lease and real Guest teardown; no incomplete report was published')
  } finally { await browser.close() }
} finally {
  const closing = [...children].map(async child=>{const done=once(child,'close');child.stdin?.end();await timeout(done,45000,'child cleanup').catch(()=>child.kill('SIGTERM'))})
  await Promise.all(closing)
  if (hub.exitCode===null && hub.signalCode===null) { const done=once(hub,'close');hub.stdin.end();await timeout(done,8000,'Hub exit').catch(()=>hub.kill('SIGTERM')) }
  await rm(dir,{recursive:true,force:true})
}
