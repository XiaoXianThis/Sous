import assert from 'node:assert/strict'
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

assert(process.platform==='darwin'&&process.arch==='arm64','This guest smoke test uses the Apple Silicon Microsandbox build')
const root=resolve(dirname(fileURLToPath(import.meta.url)),'../../../..')
const msb=process.env.MSB_PATH, firmware=process.env.MSB_LIBKRUNFW_PATH, image=process.env.SOUS_TEST_MSBOX_IMAGE_ARCHIVE
assert(msb&&firmware&&image,'Supply verified MSB_PATH, MSB_LIBKRUNFW_PATH and SOUS_TEST_MSBOX_IMAGE_ARCHIVE')
const bin=process.env.SOUS_CORE_LINUX_ARM64||join(root,'build/core/linux-arm64/sous')
const runner=process.env.SOUS_SANDBOX_BIN||join(root,'build/core/sous-sandbox')
const builtWeb=process.env.SOUS_WEB_BUILD||join(root,'build/web')
const qa=await mkdtemp('/private/tmp/sous-lg-')
const source=join(qa,'input'), output=join(qa,'results'), home=join(qa,'runtime')
try {
  await mkdir(source); await mkdir(output)
  const compiled=await readFile(bin)
  assert(compiled.length<=30*1024*1024,'Use the stripped Linux arm64 build')
  assert.equal(compiled.subarray(0,4).toString(),'\u007fELF')
  assert.equal(compiled.readUInt16LE(18),183,'Expected AArch64 binary')
  const size=1024*1024
  for(let at=0;at<compiled.length;at+=size) await writeFile(join(source,`binary-${String(at/size).padStart(3,'0')}`), compiled.subarray(at,at+size))
  await copyFile(join(builtWeb,'index.html'),join(source,'template.html'))
  const smoke=`
    test "$(uname -s)" = Linux
    test "$(uname -m)" = aarch64
    umask 077
    ws=/tmp/isolated-sous
    mkdir -p "$ws/web" "$ws/state"
    cat binary-* > "$ws/sous"
    chmod 700 "$ws/sous"
    cp template.html "$ws/web/index.html"
    printf '%s\\n' 'listen: 127.0.0.1:8787' 'public_url: https://work.sous.test' 'data_dir: state' 'web_dir: web' > "$ws/hosted.yaml"
    "$ws/sous" hosted-hub --config "$ws/hosted.yaml" --setup-link-file "$ws/initial"
    grep -q '^https://work.sous.test/setup#token=' "$ws/initial"
    fetch() {
      printf 'GET %s HTTP/1.1\\r\\nHost: work.sous.test\\r\\nConnection: close\\r\\n\\r\\n' "$1" | timeout 2 nc 127.0.0.1 8787
    }
    start() {
      "$ws/sous" hosted-hub --config "$ws/hosted.yaml" > "$ws/service.log" 2>&1 &
      service=$!
      trap 'kill -TERM "$service" 2>/dev/null || true' EXIT
      i=0
      until fetch /healthz | grep -q '200 OK'; do
        if ! kill -0 "$service" 2>/dev/null || [ "$i" -ge 30 ]; then cat "$ws/service.log"; return 1; fi
        i=$((i+1))
        sleep 0.15
      done
    }
    stop() {
      kill -TERM "$service"
      wait "$service"
      trap - EXIT
    }
    start
    fetch /api/v1/me | grep -q '"hosted_workspace":true'
    fetch /api/v1/tasks | grep -q '401 Unauthorized'
    fetch /node | grep -q '404 Not Found'
    fetch / | grep -q 'content="hosted"'
    stop
    start
    fetch /api/v1/me | grep -q '"logged_in":false'
    stop
    if grep -q 'setup#token=' "$ws/service.log"; then echo 'unexpected enrollment token in service log'; exit 1; fi
    echo 'Linux arm64 hosted Hub guest: started, public-origin/legacy-route boundaries checked, stopped and restarted'
  `
  const proc=Bun.spawn([runner,'--source',source,'--output',output,'--msb',msb,'--firmware',firmware,'--runtime-home',home,'--image-archive',image,'--timeout','75s','--','/bin/sh','-ec',smoke],{stdout:'pipe',stderr:'pipe'})
  const [code,stdout,stderr]=await Promise.all([proc.exited,new Response(proc.stdout).text(),new Response(proc.stderr).text()])
  const report=stdout.trim()?JSON.parse(stdout):null
  const log=report?.directory?await readFile(join(report.directory,'guest.log'),'utf8'):''
  assert.equal(code,0,[stdout,stderr,log].join('\n'))
  assert.match(log,/Linux arm64 hosted Hub guest: started/)
  console.log(log.trim())
} finally {
  // The runner confirms guest termination before returning, including errors.
  // If its recovery journal remains, retain the isolated fixture for recovery.
  const recovered=await Bun.file(join(home,'.sous-active-run')).exists()
  if (recovered) console.error('Retained unfinished Microsandbox runtime at '+home)
  else await rm(qa,{recursive:true,force:true})
}
