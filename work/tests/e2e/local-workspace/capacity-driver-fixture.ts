#!/usr/bin/env bun
// Synthetic capacity driver. Never creates or stops a real cloud resource.
import assert from 'node:assert/strict'
import { readFile, rename, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const directory=process.env.SOUS_TEST_CAPACITY_DIRECTORY
assert(directory,'This fixture requires an isolated test directory')
const stateFile=join(directory,'capacity-state.json'), bootFile=join(directory,'worker-token')
const body=await new Response(Bun.stdin.stream()).json() as {
  action:'ensure'|'retire'; scope:string; generation:number; reservation:string;
  hub:string; bootstrap?:string; bootstrap_until?:number
}
assert.match(body.scope,/^[a-f0-9]{64}$/)
assert(body.generation>0 && Number.isSafeInteger(body.generation))
assert.match(body.reservation,/^[0-9A-Z]{26}$/)
const before=await Bun.file(stateFile).exists()? JSON.parse(await readFile(stateFile,'utf8')) as State : { retired:[] }
type State={ active?:{generation:number; reservation:string; scope:string}; retiring?:string; retired:string[] }
const state:State=before
let result:'present'|'pending'|'absent'
if(body.action==='ensure') {
  assert(body.bootstrap&&body.bootstrap_until&&body.bootstrap_until>Date.now())
  assert.match(body.bootstrap,/^[a-f0-9]{64}$/)
  assert(!state.retired.includes(body.reservation)&&!state.retiring,'an old reservation tried to launch again')
  const identity={generation:body.generation,reservation:body.reservation,scope:body.scope}
  if(state.active) {
    assert.deepEqual(state.active,identity,'a second instance would violate the slot quota')
    assert.equal((await readFile(bootFile,'utf8')).trim(),body.bootstrap,'restart changed the bootstrap')
  } else {
    await writeFile(bootFile,body.bootstrap+'\n',{mode:0o600})
    state.active=identity
  }
  result='present'
} else {
  assert.equal(body.action,'retire')
  assert(!body.bootstrap&&body.bootstrap_until===undefined,'retirement got credentials')
  if (state.active) assert.equal(state.active?.reservation,body.reservation)
  if ((await Bun.file(join(directory,'allow-retire')).exists())||state.retired.includes(body.reservation)) {
    if (!state.retired.includes(body.reservation)) state.retired.push(body.reservation)
    delete state.active
    delete state.retiring
    result='absent'
  } else {
    state.retiring=body.reservation
    result='pending'
  }
}
await writeFile(stateFile+'.partial',JSON.stringify(state),{mode:0o600})
await rename(stateFile+'.partial',stateFile)
// Simulate a provider operation that took effect but lost its reply.
if (await Bun.file(join(directory,'drop-'+body.action+'-ack')).exists()) process.exit(7)
console.log(JSON.stringify({state:result}))
