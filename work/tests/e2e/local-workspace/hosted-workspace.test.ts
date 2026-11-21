import assert from 'node:assert/strict'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { createHash, randomBytes, randomUUID, X509Certificate } from 'node:crypto'
import { request as httpsRequest } from 'node:https'
import { chmod, copyFile, mkdtemp, readFile, realpath, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium, devices, type Browser, type BrowserContext, type Page } from 'playwright-core'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..')
const bin = process.env.SOUS_CORE_BIN || join(root, 'build/core', process.platform === 'win32' ? 'sous.exe' : 'sous')
const workerBin = process.env.SOUS_CLOUD_WORKER_BIN || bin
const web = process.env.SOUS_WEB_BUILD || join(root, 'build/web')
const temp = await mkdtemp(join(tmpdir(), 'sous-hosted-test-'))
const host = 'work.sous.test'
const probe = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch: ()=>new Response() })
const port = probe.port
probe.stop(true)
const origin = `https://${host}:${port}`
const config = join(temp, 'hosted.yaml'), cert = join(temp, 'local-cert.pem'), key = join(temp, 'local-key.pem')
const env = { ...process.env, SOUS_HOME: join(temp, 'unused-personal-home') }
const managed=process.env.SOUS_TEST_MANAGED_WORKER==='1'
const useWorker=managed||process.env.SOUS_TEST_CLOUD_WORKER==='1'
const tokenFile=join(temp,'worker-token')
const controllerToken=join(temp,'controller-token'), controllerSeed=join(temp,'controller-seed')
const providerFile=join(temp,'capacity-driver'), providerState=join(temp,'capacity-state.json')
const allowRetire=join(temp,'allow-retire')
const allocWorkerState=async()=>realpath(await mkdtemp(join('/tmp','sous-cw-')))
let workerState=useWorker?await allocWorkerState():''
let runtime=join(workerState,'vm')
const workerDirectories=workerState?[workerState]:[]
let generation=0

type DriverState={active?:{generation:number;reservation:string}; retiring?:string; retired:string[]}
const driverState=async()=>JSON.parse(await readFile(providerState,'utf8')) as DriverState
function controllerTick() {
  return execFileSync(bin,['cloud-controller','--once','--hub',origin,'--token-file',controllerToken,'--bootstrap-seed-file',controllerSeed,
    '--provider',providerFile,'--ca-file',cert,'--connect-loopback',`127.0.0.1:${port}`],
  {env:{...env,SOUS_TEST_CAPACITY_DIRECTORY:temp},encoding:'utf8',stdio:['ignore','pipe','pipe']})
}
let worker: ChildProcess | undefined
let server: ChildProcess | undefined
let serviceLog = ''
let browser: Browser | undefined
let localCA: Buffer

const externalMCP=process.env.SOUS_TEST_EXTERNAL_MCP==='1'
const mcpCallback='https://external-client.sous.test/callback'
const mcpClient='external-fixture'

let providerCalls=0
const mockACP = process.env.SOUS_TEST_HOSTED_ACP === '1' ? Bun.serve({hostname:'127.0.0.1',port:0,async fetch(req){
  assert.equal(new URL(req.url).pathname,'/chat/completions')
  const body=await req.json() as { messages:{role:string;content:string}[]; tools:{ function:{name:string} }[] }
  assert.deepEqual(body.tools.map(t=>t.function.name).sort(),['current_time','memory_search','project_list','task_create'])
  assert.equal(body.messages.findLast(v=>v.role==='user')?.content,'云工作区，个人电脑离线')
  providerCalls++
  return new Response(`data: ${JSON.stringify({choices:[{index:0,delta:{content:'云端 ACP 独立回复'}}]})}\n\ndata: [DONE]\n\n`,{headers:{'Content-Type':'text/event-stream'}})
}}) : undefined

function stop() {
  if (!server || server.exitCode !== null || server.signalCode !== null) return Promise.resolve()
  const p = server
  return new Promise<void>((resolve) => {
    const timer = setTimeout(()=>p.kill('SIGKILL'), 8000)
    p.once('exit', () => { clearTimeout(timer); resolve() })
    p.kill('SIGTERM')
  })
}

async function start() {
  const p = spawn(bin, ['hosted-hub','--config',config], { env, stdio: ['ignore','pipe','pipe'] })
  server = p
  p.stdout!.on('data', chunk => { serviceLog += chunk })
  p.stderr!.on('data', chunk => { serviceLog += chunk })
  let spawnError: Error | undefined
  p.once('error', e => { spawnError = e })
  // Connect directly to the socket for readiness without trusting caller-local DNS.
  const until = performance.now()+10000
  while (performance.now()<until) {
    if (spawnError) throw spawnError
    if (p.exitCode!==null) throw new Error(`Hub startup failed: ${serviceLog}`)
    try {
      if (await new Promise<boolean>((resolve,reject)=>{
        const request=httpsRequest({hostname:'127.0.0.1',port,servername:host,ca:localCA,path:'/healthz',headers:{Host:`${host}:${port}`},timeout:500}, response=>{
          response.resume()
          resolve(response.statusCode===200)
        })
        request.on('error',reject)
        request.on('timeout',()=>request.destroy(new Error('TLS probe timeout')))
        request.end()
      })) return
    } catch { /* still starting */ }
    await Bun.sleep(50)
  }
  throw new Error(`Hub readiness timed out: ${serviceLog}`)
}

async function authenticateFixture(context: BrowserContext) {
  const page = await context.newPage()
  const cdp = await context.newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options:{protocol:'ctap2',transport:'internal',hasResidentKey:true,hasUserVerification:true,isUserVerified:true,automaticPresenceSimulation:true} })
  return {page,cdp}
}

async function stopWorker() {
  const proc=worker
  if (!proc || proc.exitCode!==null || proc.signalCode!==null) return
  await new Promise<void>((resolve)=>{
    const timer=setTimeout(()=>proc.kill('SIGKILL'),42000)
    proc.once('exit',()=>{clearTimeout(timer);resolve()})
    proc.kill('SIGTERM')
  })
}

function workerOnce(dialPort=port, image=process.env.SOUS_TEST_MSBOX_IMAGE_ARCHIVE) {
  const msb=process.env.MSB_PATH, fw=process.env.MSB_LIBKRUNFW_PATH
  assert(msb&&fw&&image,'Supply pinned MSB_PATH, MSB_LIBKRUNFW_PATH and SOUS_TEST_MSBOX_IMAGE_ARCHIVE')
  const proc=spawn(workerBin,['cloud-worker','--once','--hub',origin,'--token-file',tokenFile,'--state',workerState,'--runtime-home',runtime,...(generation>0?['--generation',String(generation)]:[]),
    '--msb',msb,'--firmware',fw,'--image-archive',image,'--ca-file',cert,'--connect-loopback',`127.0.0.1:${dialPort}`],{env,stdio:['ignore','pipe','pipe']})
  worker=proc
  let log=''
  proc.stderr!.on('data',data=>{log=(log+data).slice(-16384)})
  const exit=new Promise<number|null>((resolve,reject)=>{proc.once('exit',resolve);proc.once('error',reject)})
  return {
    async done(expected=0){assert.equal(await exit,expected,log);return log},
    async crash(){proc.kill('SIGKILL');assert.equal(await exit,null,log);assert.equal(proc.signalCode,'SIGKILL')},
    async started(){
      const deadline=Date.now()+15000
      while(!log.includes('正在 Guest 内运行命令')) {
        assert(proc.exitCode===null && Date.now()<deadline,log||'Guest did not start')
        await Bun.sleep(40)
      }
    },
  }
}

async function checkCloudWorker(page: Page, ids:{task:string; project:string}) {
  async function createTask(title:string) {
    return await page.evaluate(async({title,project})=>{
      const prefix='__Host-sous_csrf='
      const csrf=document.cookie.split(/;\s*/).find(x=>x.startsWith(prefix))?.slice(prefix.length)??''
      const r=await fetch('/api/v1/tasks',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({title,project_id:project,kind:'general'})})
      if(!r.ok) throw new Error(`create task ${r.status}`)
      return (await r.json()).id as string
    },{title,project:ids.project})
  }
  const panel=page.getByRole('region',{name:'云工作机'})
  const submit=async (task:string,script:string,prior?:string)=>{
    await page.goto(`${origin}/#/task=${task}`)
    const sourceReply=prior?page.waitForResponse(r=>r.url().endsWith(`/projects/${ids.project}/cloud-work-results`),{timeout:8000}):undefined
    await panel.getByRole('button',{name:'授权云执行'}).tap()
    if (prior) {
      const sources=await sourceReply!
      const body=await sources.json()
      assert.equal(sources.status(),200,JSON.stringify(body))
      assert(body.results.some((job:{id:string})=>job.id===prior),'previous result was missing from the current project')
      await panel.getByLabel('起始材料').selectOption(prior)
    } else await panel.getByLabel('本次上传的文件').setInputFiles({name:'input.txt',mimeType:'text/plain',buffer:Buffer.from('approval-source\n')})
    await panel.getByLabel('云 Guest 命令').fill(script)
    const accepted=page.waitForResponse(r=>r.url().endsWith(`/tasks/${task}/cloud-work`)&&r.request().method()==='POST')
    await panel.getByRole('button',{name:'确认并排队'}).tap()
    const res=await accepted
    const body=await res.json()
    assert.equal(res.status(),202,JSON.stringify(body))
    await panel.getByText(managed?/^已授权，/:'已授权，等待可用的云工作机。',{exact:true}).waitFor()
    return body.job.id as string
  }
  const getOutput=(id:string)=>page.evaluate(async(id)=>{
    const r=await fetch(`/api/v1/cloud-work/${id}/results`)
    if(!r.ok)throw new Error(`result ${r.status}`)
    return (await r.json()).files as Record<string,string>
  },id)
  const cleaned=async()=>{
    await assert.rejects(stat(join(runtime,'.sous-active-run')), {code:'ENOENT'})
    await assert.rejects(stat(join(workerState,'pending.json')), {code:'ENOENT'})
  }
  if(managed) {
    controllerTick()
    assert.equal(await Bun.file(providerState).exists(),false,'ordinary workspace activity requested provider compute')
  } else {
    const idle=await workerOnce().done()
    assert(!idle.includes('启动隔离 Guest'),'a draft must not start compute')
    await cleaned()
  }
  const first=await submit(ids.task,`test "$(cat input.txt)" = approval-source; printf 'persistent-result\n' > result.txt`)
  if(managed) {
    const dropEnsure=join(temp,'drop-ensure-ack')
    await writeFile(dropEnsure,'yes',{mode:0o600})
    assert.throws(controllerTick,/provider ensure not confirmed/)
    await rm(dropEnsure)
    const initial=await driverState()
    assert.equal(initial.active?.generation,1)
    generation=initial.active!.generation
    controllerTick() // new process: same reservation and same one-use boot secret
    assert.deepEqual(await driverState(),initial)
    await panel.getByText(/云工作机正在启动并检查运行环境/).waitFor()
  }
  const assertUnclaimed=async()=>{
    const current=await page.evaluate(async(task)=>{
      const r=await fetch(`/api/v1/tasks/${task}/cloud-work`)
      if(!r.ok)throw new Error(`job state ${r.status}`)
      return (await r.json()).job
    },ids.task)
    assert.equal(current.id,first)
    assert.equal(current.status,'queued','failed worker preflight consumed a valid authorization')
  }
  const invalid=await workerOnce(port,join(temp,'nonexistent-image.tar')).done(1)
  assert.match(invalid,/云执行领取前预检失败/)
  await assertUnclaimed()
  await cleaned()
  const journal=join(runtime,'.sous-active-run')
  const marker='sous-run-testfixture'
  await writeFile(journal,marker,{mode:0o600,flag:'wx'})
  try {
    const retained=await workerOnce().done(1)
    assert.match(retained,/上次 Guest 需要对账/)
    assert.equal(await readFile(journal,'utf8'),marker)
    await assertUnclaimed()
  } finally { await rm(journal) }
  console.log('Invalid image and unreconciled Guest preflights failed without consuming the authorized job')
  // Route only the worker through a private TLS hop. The real Hub commits
  // the result, then we withhold its reply and stop the worker mid-ack.
  let committed!: () => void
  const acknowledged=new Promise<void>(resolve=>{committed=resolve})
  const edge=Bun.serve({hostname:'127.0.0.1',port:0,tls:{cert:Bun.file(cert),key:Bun.file(key)},async fetch(req){
    const path=new URL(req.url).pathname
    if (!['claim','heartbeat','complete','ready'].some(a=>path===`/api/v1/cloud-worker/${a}`)) return new Response(null,{status:404})
    const body=Buffer.from(await req.arrayBuffer())
    const reply=await new Promise<{status:number;raw:Buffer}>((resolve,reject)=>{
      const outgoing=httpsRequest({hostname:'127.0.0.1',port,servername:host,ca:localCA,path,method:'POST',headers:{'Host':`${host}:${port}`,
        'Authorization':req.headers.get('Authorization')??'',
        ...(managed?{'X-Sous-Worker-Generation':req.headers.get('X-Sous-Worker-Generation')??''}:{}), 'Content-Type':'application/json','Content-Length':body.length}},res=>{
        const chunks:Buffer[]=[];res.on('data',c=>chunks.push(c));res.on('error',reject)
        res.on('end',()=>resolve({status:res.statusCode??500,raw:Buffer.concat(chunks)}))
      })
      outgoing.on('error',reject);outgoing.end(body)
    })
    if (path.endsWith('/complete')&&reply.status===200) {
      committed()
      await new Promise<void>(resolve=>{
        if (req.signal.aborted) resolve()
        else req.signal.addEventListener('abort',()=>resolve(),{once:true})
        setTimeout(resolve,7000).unref()
      })
      return new Response(null,{status:502})
    }
    return new Response(reply.raw,{status:reply.status,headers:{'Content-Type':'application/json'}})
  }})
  try {
    const work=workerOnce(edge.port!)
    const wait=AbortSignal.timeout(25000)
    await Promise.race([acknowledged,new Promise<never>((_,reject)=>wait.addEventListener('abort',()=>reject(new Error('No durable result reached Hub before fault injection')),{once:true}))])
    await stat(join(workerState,'pending.json'))
    await assert.rejects(stat(join(runtime,'.sous-active-run')),{code:'ENOENT'})
    await work.crash()
  } finally {edge.stop(true)}
  // Model the volatile /run bootstrap disappearing across an EC2 reboot.
  // Durable worker identity must be sufficient to replay the checkpoint.
  if(managed) await rm(tokenFile)
  const restarted=await workerOnce().done()
  assert(!restarted.includes('启动隔离 Guest'),'restarting after a lost ack must not rerun the command')
  await panel.getByText(/已保存成果，Guest 已回收/).waitFor()
  await cleaned()
  assert.equal(Buffer.from((await getOutput(first))['result.txt'],'base64').toString(),'persistent-result\n')
  await panel.getByRole('button',{name:'查看成果文件'}).tap()
  await panel.getByRole('button',{name:'下载 result.txt'}).waitFor()
  console.log('Mobile authorization + real offline Guest: committed result survived lost ack and worker SIGKILL; restarted worker only replayed the receipt')

  await stop();await start()
  let retiring:string|undefined
  if(managed) {
    // Completion/replay did not itself tell the Hub that the worker is now
    // idle. Simulate a running controller, then a single clean idle poll.
    controllerTick()
    assert.equal((await driverState()).retiring,undefined)
    await workerOnce().done()
    await Bun.sleep(5200)
    controllerTick()
    const draining=await driverState()
    retiring=draining.active?.reservation
    assert(retiring)
    assert.equal(draining.retiring,retiring)
    assert.match(await workerOnce().done(1),/worker 注册未确认/)
  }
  const another=await createTask('用已保存成果再次启动')
  const next=await submit(another,`test "$(cat result.txt)" = persistent-result; printf 'recovered\n' > resumed.txt`,first)
  assert.notEqual(first,next)
  if(managed) {
    await panel.getByText(/上一台云工作机正在回收，任务会留在队列中/).waitFor()
    controllerTick()
    assert.equal((await driverState()).active?.reservation,retiring,'queued work bypassed pending retirement')
    await writeFile(allowRetire,'yes',{mode:0o600})
    const dropRetire=join(temp,'drop-retire-ack')
    await writeFile(dropRetire,'yes',{mode:0o600})
    assert.throws(controllerTick,/provider retire not confirmed/)
    await rm(dropRetire); await rm(allowRetire)
    controllerTick() // prove repeated absence can settle a lost reply
    const empty=await driverState()
    assert(!empty.active&&empty.retired.includes(retiring!),'provider did not confirm retirement of the old reservation')
    controllerTick()
    const fresh=await driverState()
    assert.equal(fresh.active?.generation,2)
    assert.notEqual(fresh.active?.reservation,retiring)
    generation=fresh.active!.generation
    workerState=await allocWorkerState(); runtime=join(workerState,'vm'); workerDirectories.push(workerState)
  }
  await workerOnce().done()
  await panel.getByText(/已保存成果，Guest 已回收/).waitFor()
  await cleaned()
  assert.equal(Buffer.from((await getOutput(next))['resumed.txt'],'base64').toString(),'recovered\n')
  console.log(managed?'Controller restarted, awaited driver-confirmed retirement and issued a new worker generation; fresh Guest restored the preceding Hub result':'Fresh worker and Guest reused first execution’s persisted output after Hub restart')

  const cancelTask=await createTask('取消长时云执行')
  await submit(cancelTask,'sleep 60; printf wrong > unwanted.txt')
  const long=workerOnce()
  await long.started()
  await panel.getByText('云工作机已领取，正在准备或执行。',{exact:true}).waitFor()
  await panel.getByRole('button',{name:'请求停止'}).tap()
  await long.done()
  await panel.getByText('已取消。没有发布部分结果。',{exact:true}).waitFor()
  await cleaned()
  console.log('Mobile cancellation reclaimed running Guest and published no partial result')
}

async function checkExternalMCP(page: Page, project: string) {
  const resource=origin+'/mcp'
  function native(path:string,method='GET',body='',headers:Record<string,string>={}) {
    return new Promise<{status:number;headers:Record<string,unknown>;raw:string}>((resolve,reject)=>{
      const r=httpsRequest({hostname:'127.0.0.1',port,servername:host,ca:localCA,path,method,
        headers:{Host:`${host}:${port}`,...(body?{'Content-Length':Buffer.byteLength(body)}:{}),...headers}},res=>{
        const chunks:Buffer[]=[]; res.on('data',c=>chunks.push(c));res.on('error',reject)
        res.on('end',()=>resolve({status:res.statusCode??500,headers:res.headers,raw:Buffer.concat(chunks).toString('utf8')}))
      })
      r.on('error',reject);r.end(body)
    })
  }
  const discovery=await native('/mcp')
  assert.equal(discovery.status,401)
  assert.match(String(discovery.headers['www-authenticate']),/oauth-protected-resource\/mcp/)
  assert.equal(JSON.parse((await native('/.well-known/oauth-protected-resource/mcp')).raw).resource,resource)
  const verifier=randomBytes(32).toString('base64url'), state=randomBytes(16).toString('hex')
  const starting=new URLSearchParams({client_id:mcpClient,redirect_uri:mcpCallback,resource,state,response_type:'code',
    code_challenge:createHash('sha256').update(verifier).digest('base64url'),code_challenge_method:'S256',scope:'project.read drafts.write'})
  const bad=new URLSearchParams(starting);bad.set('redirect_uri','https://unregistered.sous.test/socket')
  const rejected=await native('/oauth/authorize?'+bad)
  assert.equal(rejected.status,400);assert.equal(rejected.headers.location,undefined)
  const callbackPattern=/^https:\/\/external-client\.sous\.test\/callback\?/
  await page.route(callbackPattern,route=>route.fulfill({body:'External client received callback (isolated fixture)'}))
  await page.goto(`${origin}/oauth/authorize?${starting}`)
  await page.getByRole('heading',{name:'允许外部客户端连接？'}).waitFor()
  await page.getByLabel('授权项目').selectOption(project)
  const check=page.getByRole('checkbox',{name:'允许创建任务草稿'})
  assert.equal(await check.isChecked(),false,'external write scope must not be preselected')
  await check.check()
  const callback=page.waitForURL(callbackPattern)
  await page.getByRole('button',{name:'允许连接'}).tap()
  await callback
  const received=new URL(page.url()).searchParams
  assert.equal(received.get('state'),state)
  const code=received.get('code');assert(code,'expected a one-time OAuth code')
  const grant=new URLSearchParams({client_id:mcpClient,redirect_uri:mcpCallback,resource,grant_type:'authorization_code',code,code_verifier:verifier})
  const token=await native('/oauth/token','POST',grant.toString(),{'Content-Type':'application/x-www-form-urlencoded'})
  assert.equal(token.status,200)
  const credentials=JSON.parse(token.raw) as {access_token:string;scope:string}
  assert.equal(credentials.scope,'project.read drafts.write')
  assert.equal((await native('/oauth/token','POST',grant.toString(),{'Content-Type':'application/x-www-form-urlencoded'})).status,400)
  let version:string|undefined, serial=0
  async function rpc(method:string,params:unknown={}) {
    return native('/mcp','POST',JSON.stringify({jsonrpc:'2.0',id:++serial,method,params}),{
      'Authorization':`Bearer ${credentials.access_token}`,'Content-Type':'application/json','Accept':'application/json, text/event-stream',
      ...(version?{'MCP-Protocol-Version':version}:{}),
    })
  }
  const init=await rpc('initialize',{protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'mobile-external-fixture',version:'1'}})
  assert.equal(init.status,200);version=JSON.parse(init.raw).result.protocolVersion
  const list=JSON.parse((await rpc('tools/list')).raw).result.tools
  assert.deepEqual(list.map((t:{name:string})=>t.name).sort(),['project_get','task_create','task_get','task_list'])
  const saved=await rpc('tools/call',{name:'task_create',arguments:{request_id:randomUUID(),title:'外部客户端验收草稿',detail:'通过独立 MCP 创建，未派发。'}})
  const draft=JSON.parse(saved.raw).result?.structuredContent
  assert(draft && draft.status==='inbox','external tool must file an inbox draft')
  await page.goto(`${origin}/#/task=${draft.id}`)
  await page.getByText('外部客户端验收草稿',{exact:true}).first().waitFor()
  await page.getByRole('button',{name:'返回任务列表',exact:true}).tap()
  await page.getByRole('button',{name:'返回工作台',exact:true}).tap()
  await page.getByRole('navigation',{name:'主导航'}).getByRole('button',{name:'设置',exact:true}).tap()
  await page.getByRole('button',{name:'外部连接',exact:true}).tap()
  await page.getByRole('button',{name:'撤销 外部客户端测试 对 云端验收项目 的授权',exact:true}).tap()
  await page.getByText(/已撤销 外部客户端测试/).waitFor()
  const denied=await rpc('tools/list')
  assert.equal(denied.status,401,'an old tool session was still accepted after UI revoke')
  await page.goto(origin+'/')
  await page.getByRole('navigation',{name:'主导航'}).getByRole('button',{name:'对话',exact:true}).waitFor()
  console.log('Hosted external MCP: real Passkey consent, exact callback and PKCE, scoped draft visible in WebUI and UI revocation stopped the live client')
}

try {
  const opensslConfig=join(temp,'openssl.cnf')
  await writeFile(opensslConfig,`[req]\ndistinguished_name=dn\nprompt=no\nx509_extensions=cert_ext\n[dn]\nCN=${host}\n[cert_ext]\nsubjectAltName=DNS:${host}\n`)
  execFileSync('openssl',['req','-new','-x509','-nodes','-days','1','-newkey','rsa:2048','-keyout',key,'-out',cert,'-config',opensslConfig],{stdio:'ignore'})
  localCA=await readFile(cert)
  if (mockACP) await writeFile(join(temp,'model.yaml'),`kind: openai-chat\nmodel: hosted-model-fixture\nbase_url: http://127.0.0.1:${mockACP.port}\n`,{mode:0o600})
  if (managed) {
    for(const file of [controllerToken,controllerSeed]) await writeFile(file,randomBytes(32).toString('hex')+'\n',{mode:0o600})
    await copyFile(fileURLToPath(new URL('./capacity-driver-fixture.ts',import.meta.url)),providerFile)
    await chmod(providerFile,0o700)
  } else if (useWorker) await writeFile(tokenFile,randomBytes(32).toString('hex')+'\n',{mode:0o600})
  const workerConfig=managed?'cloud_controller_token_file: controller-token\ncloud_idle_seconds: 5\n':(useWorker?'cloud_worker_token_file: worker-token\n':'')
  const agentConfig=mockACP?'agent_config: model.yaml\n':''
  const externalConfig=externalMCP?`external_mcp_clients:\n  - id: ${mcpClient}\n    name: 外部客户端测试\n    redirect_uri: ${mcpCallback}\n`:''
  await writeFile(config,`listen: 127.0.0.1:${port}\npublic_url: ${origin}\ndata_dir: data\nweb_dir: ${JSON.stringify(web)}\ntls_cert: local-cert.pem\ntls_key: local-key.pem\n${agentConfig}${workerConfig}${externalConfig}`,{mode:0o600})
  const setupFile=join(temp,'enroll-link')
  const output=execFileSync(bin,['hosted-hub','--config',config,'--setup-link-file',setupFile],{env,encoding:'utf8'})
  const setup=(await readFile(setupFile,'utf8')).trim()
  const setupURL=new URL(setup)
  assert.equal(setupURL.origin,origin)
  assert(setupURL.hash.startsWith('#token='))
  assert(!output.includes(setupURL.hash))
  if (process.platform!=='win32') assert.equal((await stat(setupFile)).mode&0o777,0o600)
  await start()

  // This Chromium context accepts ONLY the synthetic leaf's key, not arbitrary
  // certificates. Real deployments use a certificate from a trusted CA.
  const publicKey=new X509Certificate(await readFile(cert)).publicKey.export({type:'spki',format:'der'})
  const pin=createHash('sha256').update(publicKey).digest('base64')
  browser=await chromium.launch({headless:true,executablePath:process.env.SOUS_TEST_CHROMIUM,args:[`--host-resolver-rules=MAP ${host} 127.0.0.1`,`--ignore-certificate-errors-spki-list=${pin}`,'--no-proxy-server']})
  const ctx=await browser.newContext({...devices['Pixel 7'],serviceWorkers:'block'})
  const second=await browser.newContext({...devices['Pixel 7'],serviceWorkers:'block'})
  const fixtures=[await authenticateFixture(ctx),await authenticateFixture(second)]
  const pages=fixtures.map(f=>f.page)
  const outside:string[]=[]
  for (const context of [ctx,second]) await context.route('**/*',route=> {
    const url = route.request().url()
    if (new URL(url).origin===origin) return route.continue()
    outside.push(url)
    return route.abort()
  })
  // Both ceremonies must successfully begin before either browser may finish.
  let finishing=0
  let unblock!:()=>void
  const barrier=new Promise<void>(resolve=>{unblock=resolve})
  for (const page of pages) await page.route('**/api/v1/auth/passkey/register/finish',async route=>{
    if (++finishing===2) unblock()
    await barrier
    await route.continue()
  })
  await Promise.all(pages.map(p=>p.goto(setup)))
  await Promise.all(pages.map(p=>p.getByRole('heading',{name:'Sous 云工作区'}).waitFor()))
  const finished=pages.map(p=>p.waitForResponse(r=>r.url().endsWith('/register/finish')))
  await Promise.all(pages.map(p=>p.getByRole('button',{name:'为云工作区创建 Passkey'}).click()))
  const results=await Promise.all(finished)
  assert.equal(finishing,2)
  assert.deepEqual(results.map(r=>r.status()).sort(),[200,403])
  const winner=results[0].ok()?0:1
  const page=pages[winner], activeContext=winner===0?ctx:second
  assert(!serviceLog.includes(setupURL.hash))
  console.log('Real TLS + mobile viewport + two parallel WebAuthn challenges: one owner enrollment, no link in HTML or service logs')

  const sockets:string[]=[]
  page.on('websocket',s=>{sockets.push(s.url())})
  await page.getByRole('navigation',{name:'主导航'}).getByRole('button',{name:'对话',exact:true}).click()
  const composer=page.getByRole('textbox',{name:'消息',exact:true})
  await composer.fill('云工作区，个人电脑离线')
  const saved=page.waitForResponse(r=>r.url().endsWith('/messages')&&r.request().method()==='POST')
  await page.getByRole('button',{name:'发送',exact:true}).tap()
  assert.equal((await saved).status(),202)
  assert(!page.url().includes('token='))
  const cookies=await activeContext.cookies(origin)
  assert(cookies.some(c=>c.name==='__Host-sous_session'&&c.secure&&c.httpOnly&&c.path==='/'))
  assert(cookies.some(c=>c.name==='__Host-sous_csrf'&&c.secure&&!c.httpOnly))

  await page.getByRole('button',{name:'收起键盘',exact:true}).tap()
  await page.getByRole('navigation',{name:'主导航'}).getByRole('button',{name:'工作台',exact:true}).tap()
  await page.getByRole('button',{name:'新建或登记项目',exact:true}).click()
  const form=page.getByRole('dialog',{name:'登记项目'})
  await form.getByRole('textbox',{name:'名字'}).fill('云端验收项目')
  await form.getByRole('button',{name:'保存',exact:true}).click()
  await page.getByRole('heading',{name:'云端验收项目',exact:true}).first().waitFor()
  await page.getByRole('button',{name:'新任务',exact:true}).first().click()
  await page.getByRole('textbox',{name:'标题',exact:true}).fill('云端验收任务')
  await page.getByRole('button',{name:'只创建',exact:true}).click()
  await page.getByText('云端验收任务',{exact:true}).first().waitFor()
  const state=await page.evaluate(async()=>{
    const k='__Host-sous_csrf='
    const csrf=document.cookie.split(/;\s*/).find(x=>x.startsWith(k))?.slice(k.length)??''
    async function get(p:string){return (await fetch('/api/v1'+p)).json()}
    const resp=await fetch('/api/v1/memories',{method:'POST',headers:{'X-CSRF-Token':csrf,'Content-Type':'application/json'},body:JSON.stringify({kind:'note',content:'独立云工作区验收记忆'})})
    if(!resp.ok) throw new Error(`memory ${resp.status}`)
    const {threads}=await get('/threads'),{tasks}=await get('/tasks'),{projects}=await get('/projects')
    return {thread:threads.find((t:{kind:string})=>t.kind==='main').id,task:tasks.find((t:{title:string})=>t.title==='云端验收任务').id,
      project:projects.find((p:{name:string})=>p.name==='云端验收项目').id,memory:(await resp.json()).id}
  })
  if (mockACP) {
    await page.waitForFunction(async id=>{
      const {messages}=await(await fetch(`/api/v1/threads/${id}/messages`)).json()
      return messages.some((m:{role:string,content:string})=>m.role==='assistant'&&m.content==='云端 ACP 独立回复')
    },state.thread)
    assert.equal(providerCalls,1)
  }
  console.log(`Mobile WebUI saved dialogue, project, task and memory without personal Node or Relay${mockACP?' and received the in-service ACP reply':' or a model'}`)

  await stop()
  await start()
  // On cached HTML, a failing health handshake must never open the Relay UI.
  await page.route('**/api/v1/me',async route=>route.fulfill({status:503,contentType:'application/json',body:'{}'}),{times:1})
  await page.goto(origin)
  await page.getByRole('heading',{name:'暂时连不上云工作区'}).waitFor()
  await page.getByRole('button',{name:'重新连接'}).click()
  await page.getByRole('navigation',{name:'主导航'}).getByRole('button',{name:'对话',exact:true}).click()
  assert(sockets.some(url=>url.startsWith(`wss://${host}:${port}/ws?since=`)))
  const after=await page.evaluate(async state=>{
    async function get(p:string) { const r=await fetch('/api/v1'+p); assertOK(r); return r.json() }
    function assertOK(r:Response) {if(!r.ok)throw new Error(`GET ${r.status}`)}
    const {messages}=await get(`/threads/${state.thread}/messages`),{tasks}=await get('/tasks'),{projects}=await get('/projects'),{memories}=await get('/memories')
    return [messages.some((v:{content:string})=>v.content==='云工作区，个人电脑离线'), tasks.some((v:{id:string})=>v.id===state.task),projects.some((v:{id:string})=>v.id===state.project),memories.some((v:{id:string})=>v.id===state.memory)]
  },state)
  assert.deepEqual(after,[true,true,true,true])
  // A browser owner cookie cannot stand in for an external resource token.
  // Without a registered client, the endpoint does not serve an app shell.
  const mcpFromBrowser=await page.evaluate(async()=>(await fetch('/mcp')).status)
  assert.equal(mcpFromBrowser,externalMCP?403:404)
  if (useWorker) await checkCloudWorker(page,state)
  if (externalMCP) await checkExternalMCP(page,state.project)
  if (process.env.SOUS_TEST_HOSTED_LATENCY === '1' && !mockACP) {
    const cdp=fixtures[winner].cdp
    await cdp.send('Network.enable')
    const percentile=(v:number[],p:number)=>[...v].sort((a,b)=>a-b)[Math.ceil(v.length*p)-1]
    for (const latency of [0,80,180]) {
      await cdp.send('Network.emulateNetworkConditions',{offline:false,latency,downloadThroughput:1_000_000,uploadThroughput:256_000,connectionType:'wifi'})
      const samples=await page.evaluate(async({thread,latency})=>{
        const k='__Host-sous_csrf='
        const csrf=document.cookie.split(/;\s*/).find(x=>x.startsWith(k))?.slice(k.length)??''
        const data:{ack:number,persist:number}[]=[]
        for (let i=0;i<33;i++) {
          const from=performance.now()
          const res=await fetch(`/api/v1/threads/${thread}/messages`,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({content:`Synthetic acknowledgement ${latency}/${i}`})})
          const ack=performance.now()-from
          if(res.status!==202 || !(await res.json()).seq) throw new Error(`Invalid durable acknowledgement: ${res.status}`)
          const timing=res.headers.get('Server-Timing')??''
          const m=/^hub_persist;dur=([0-9.]+)$/.exec(timing)
          if(!m) throw new Error('No Hub persistence timing')
          if (i>=3) data.push({ack,persist:Number(m[1])})
        }
        return data
      },{thread:state.thread,latency})
      const ack=samples.map(x=>x.ack),persist=samples.map(x=>x.persist)
      const result={addedLatencyMS:latency,count:samples.length,ackP50:percentile(ack,.5),ackP95:percentile(ack,.95),hubP50:percentile(persist,.5),hubP95:percentile(persist,.95)}
      console.log('Warm HTTPS durable acknowledgement',JSON.stringify(result))
      if(latency>0) assert(result.ackP50>=latency*.8,'network latency fixture did not apply')
      if (latency===80) assert(result.ackP95<=300,'80 ms local emulator warm acknowledgement exceeded 300 ms')
    }
    await cdp.send('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:-1,uploadThroughput:-1})
  }
  const logout=await page.evaluate(async()=>{
    const cookie='__Host-sous_csrf='
    const csrf=document.cookie.split(/;\s*/).find(x=>x.startsWith(cookie))?.slice(cookie.length)??''
    return (await fetch('/api/v1/auth/logout',{method:'POST',headers:{'X-CSRF-Token':csrf}})).status
  })
  assert.equal(logout,200)
  await page.getByRole('button',{name:'用 Passkey 解锁'}).waitFor()
  const loggedIn=page.waitForResponse(r=>r.url().endsWith('/passkey/login/finish'))
  await page.getByRole('button',{name:'用 Passkey 解锁'}).tap()
  assert.equal((await loggedIn).status(),200)
  await page.getByRole('navigation',{name:'主导航'}).getByRole('button',{name:'对话',exact:true}).waitFor()
  assert.deepEqual(outside,[])
  assert(!serviceLog.includes(setupURL.hash))
  console.log('Restart preserved IDs and history; offline error did not switch workspace; WSS revocation and Passkey login verified')
} finally {
  await browser?.close()
  await stopWorker()
  await stop()
  mockACP?.stop(true)
  await rm(temp,{recursive:true,force:true})
  for(const directory of workerDirectories) {
    if (await Bun.file(join(directory,'vm','.sous-active-run')).exists()) console.error('Retained test worker runtime for reconciliation: '+directory)
    else await rm(directory,{recursive:true,force:true})
  }
}
