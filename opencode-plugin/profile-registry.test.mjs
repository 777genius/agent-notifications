import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import dc from 'node:diagnostics_channel';
import { createProcessRegistry } from './process-registry.mjs';

const pause = (ms = 10) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(check) { const end = Date.now() + 3500; while (!await check()) { assert.ok(Date.now() < end); await pause(); } }
const receipt = '{"protocol":1,"semantic":"unverified","generation":"none","resourceClosure":"reaped_or_not_started"}\n';
const fixture = `#!${process.execPath}
import fs from 'node:fs';
import { spawn } from 'node:child_process';
const mode = process.argv[1].split('/').at(-1).split('.')[0], root = process.cwd();
let input = ''; process.stdin.on('data', b => input += b);
process.stdin.on('end', () => {
 if (process.argv[2] !== 'opencode-runtime-profile') { setTimeout(() => process.exit(0), 300); return; }
 const d = JSON.parse(input);
 const matching = d.protocol === 1 && d.nativePID === process.ppid && d.hostExecutable === d.publicExecPath &&
  d.hostExecutable === process.env.AGENT_NOTIFICATIONS_HOST_EXECUTABLE &&
  String(d.nativePID) === process.env.AGENT_NOTIFICATIONS_NATIVE_PID && d.origin === process.env.AGENT_NOTIFICATIONS_ORIGIN &&
  d.controlRoot === process.env.AGENT_NOTIFICATIONS_CONTROL_ROOT && d.entry === 'serve' &&
  !process.env.PATH && !process.env.SECRET_TEST_TOKEN;
 fs.writeFileSync(root + '/descriptor', JSON.stringify(matching));
 if (mode === 'overflow') { process.stdout.write('x'.repeat(1025)); setInterval(() => {}, 20); return; }
 if (mode === 'malformed') { process.stdout.write(${JSON.stringify(receipt.slice(0,-2))}); return; }
 if (mode === 'duplicate') { process.stdout.write(${JSON.stringify(receipt.replace('"protocol":1', '"protocol":1,"protocol":1'))}); return; }
 if (mode === 'nonzero') { process.stdout.write(${JSON.stringify(receipt)}); process.exitCode = 1; return; }
 if (mode === 'force') { process.on('SIGTERM', () => fs.writeFileSync(root + '/term', '')); setInterval(() => {},20); return; }
 if (mode === 'abort') { process.on('SIGTERM', () => { process.stdout.write(${JSON.stringify(receipt)}); process.exit(0); }); setInterval(() => {},20); return; }
 const inner = spawn(process.execPath, ['-e', 'setTimeout(() => {}, 300)'], { stdio: ['ignore','pipe','pipe'], env: {} });
 fs.writeFileSync(root + '/inner-start', '');
 inner.once('close', () => {
  fs.writeFileSync(root + '/inner-close', ''); process.stdout.write(${JSON.stringify(receipt)});
  setTimeout(() => { fs.writeFileSync(root + '/outer-exit', ''); }, 100);
 });
});
`;
async function withFixture(mode, run) {
 const root = await fs.mkdtemp(path.join(os.tmpdir(), 'TEST-profile-'));
 const executable = path.join(root, `${mode}.mjs`), children = [];
 const observer = ({process: child}) => { const r={child,closed:false}; children.push(r); child.once('close',()=>r.closed=true); };
 await fs.writeFile(executable, fixture, {mode:0o700});
 const registry = createProcessRegistry({ executable, privateCwd:root, controlRoot:root, origin:'a'.repeat(64) });
 dc.subscribe('child_process', observer);
 const exists = async(name)=> { try { await fs.access(path.join(root,name)); return true; } catch{return false;} };
 try { await run({registry,root,children,exists}); }
 finally {
  await registry.dispose();
  for (const r of children) if (!r.closed) r.child.kill('SIGKILL');
  await until(()=>children.every(r=>r.closed));
  dc.unsubscribe('child_process',observer);
  await fs.rm(root,{recursive:true,force:true});
 }
}
const current = {isCurrent:()=>true};
// Red if a helper uses one slot, inherits PATH/auth, or releases before actual inner+outer close.
test('profile reserves two shared slots and valid inner proof is held through actual close', async()=>{
 await withFixture('normal', async({registry,root,children,exists})=>{
  let settled=false;
  const probe=registry.profile(current).then(r=>{settled=true;return r;});
  assert.equal(registry.status().occupied,2);
  const a=registry.event({...current,frame:Buffer.from('{}')}), b=registry.event({...current,frame:Buffer.from('{}')});
  assert.equal(registry.status().occupied,4);
  assert.equal((await registry.clock(current)).status,'capacity_suppressed');
  await until(()=>exists('inner-start'));
  assert.equal(JSON.parse(await fs.readFile(path.join(root,'descriptor'),'utf8')),true);
  assert.equal(settled,false);
  await until(()=>exists('inner-close'));
  assert.equal(settled,false);
  const result=await probe;
  assert.equal(result.status,'ok'); assert.equal(result.output.toString(),receipt);
  await Promise.all([a,b]); assert.equal(registry.status().occupied,0);
  assert.equal(children.length,3);
 });
});
// Red if truncation/duplicate/overflow/nonzero output falsely proves a nested resource close.
for (const mode of ['overflow','malformed','duplicate','nonzero']) test(`${mode} permanently retains both reservations after outer close`, async()=>{
 await withFixture(mode,async({registry})=>{
  assert.equal((await registry.profile(current)).status,'ipc_termination_unproved');
  assert.equal(registry.status().occupied,2); assert.equal(registry.status().accepting,false);
  assert.equal((await registry.clock(current)).status,'registry_unavailable');
  assert.equal((await registry.drain()).reaped,false);
 });
});
// Red if TERM completion is mistaken for KILL, or if KILL return claims inner reaping.
for (const mode of ['abort','force']) test(`${mode} waits actual close; only graceful complete inner proof releases`, async()=>{
 await withFixture(mode,async({registry,exists})=>{
  const controller=new AbortController(), probe=registry.profile({...current,signal:controller.signal});
  await until(()=>exists('descriptor')); controller.abort();
  assert.equal(registry.status().occupied,2);
  const result=await probe;
  assert.equal(result.output.length,0);
  assert.equal(result.status,mode==='abort'?'invalidated':'ipc_termination_unproved');
  assert.equal(registry.status().occupied,mode==='abort'?0:2);
 });
});
// Red if asynchronous start failure silently frees the reserved nested process slot.
test('profile ENOENT has no inner closure proof and disables the instance',async()=>{
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'TEST-profile-start-'));
 try {
  const registry=createProcessRegistry({executable:path.join(root,'missing'),privateCwd:root,controlRoot:root,origin:'a'.repeat(64)});
  assert.equal((await registry.profile(current)).status,'ipc_termination_unproved');
  assert.equal(registry.status().occupied,2); assert.equal(registry.status().accepting,false);
 } finally {await fs.rm(root,{recursive:true,force:true});}
});
