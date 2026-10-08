import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, open, readFile, readdir, unlink } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { callbackJoin, envelope, sha, prerequisiteAdmission, type Receiver, type Tuple } from './evidence.mts';
type Artifact = {source: string; helperSHA256: string; binarySHA256: string; helperExecuted: boolean; installedQualified: boolean};
type Ledger = {Generation: number; WindowsRetained?: {SnapshotPath: string; SHA256: string}[]};
const env = process.env;
assert.equal(env.GITHUB_ACTIONS,'true'); assert.equal(env.GITHUB_EVENT_NAME,'workflow_dispatch');
assert.equal(env.GITHUB_RUN_ATTEMPT,'1'); assert.equal(env.TEST_INSTALLED_ADMISSION,'one-fresh-disposable-installed-callback');
assert.equal(env.GITHUB_REPOSITORY,'777genius/agent-notifications'); assert.equal(process.platform,'win32');
const source = env.GITHUB_SHA!; assert(/^[a-f0-9]{40}$/.test(source));
const scenario = env.TEST_INSTALLED_SCENARIO; assert(scenario === 'basic' || scenario === 'retained');
const phase = env.TEST_INSTALLED_PHASE; assert(phase === 'archive_intake' || phase === 'installed');
const tuple: Tuple = {source,nonce:randomBytes(16).toString('hex')};
const root = path.join(env.RUNNER_TEMP!,'TEST-installed-'+tuple.nonce);
const artifact = path.resolve(env.TEST_INSTALLED_ARTIFACT!);
const observer = path.resolve(env.TEST_INSTALLED_OBSERVER!);
const entry = 'claude-notifications-windows-amd64.exe';
const original = path.join(artifact,entry);
const stage = path.join(root,'stage'), runtime = path.join(root,'runtime'), bin = path.join(runtime,'bin');
const control = path.join(root,'control'), global = path.join(root,'TEST-global.json');
const title = 'Navigation TEST '+tuple.nonce, body = 'Cold callback TEST '+tuple.nonce;
const thread = 'TEST/'+tuple.nonce+'/percent%雪';
let count = 0, unknown = false, removed = false;
const childEnv = {...env,AGENT_NOTIFICATIONS_CONTROL_ROOT:control,AGENT_NOTIFICATIONS_CONFIG:global};
async function boundedRead(file: string, limit=65536): Promise<Buffer> {
  const h = await open(file,'r');
  try { const s = await h.stat(); assert(s.isFile() && s.nlink === 1 && s.size > 0 && s.size <= limit); return await h.readFile(); }
  finally { await h.close(); }
}
async function persist(leaf: string, value: unknown): Promise<void> {
  const data = Buffer.from(JSON.stringify(value)); assert(data.length <= 65536);
  const h = await open(path.join(root,leaf),'wx');
  try { await h.writeFile(data); await h.sync(); } finally { await h.close(); }
}
async function hashFile(file: string): Promise<string> {
  const h = createHash('sha256'); for await (const chunk of createReadStream(file)) h.update(chunk as Buffer); return h.digest('hex');
}
function machine(bytes: Buffer): number {
  assert(bytes.length >= 256 && bytes.subarray(0,2).toString() === 'MZ');
  const p = bytes.readUInt32LE(60); assert(p+6 <= bytes.length && bytes.subarray(p,p+4).equals(Buffer.from([80,69,0,0])));
  return bytes.readUInt16LE(p+4);
}
async function run(label: string, exe: string, args: string[], input='', timeout=150000): Promise<string> {
  const id = (++count).toString().padStart(2,'0');
  await persist(id+'-'+label+'.intent.json',{...tuple,exe,args,inputSHA:sha(Buffer.from(input)),timeout});
  return await new Promise<string>((resolve,reject) => {
    const child = spawn(exe,args,{env:childEnv,stdio:['pipe','pipe','pipe'],windowsHide:true});
    let bytes = 0, out = '', error = '', fault: Error | undefined;
    const stop = (e: Error): void => { fault ??= e; unknown = true; child.kill(); };
    const timer = setTimeout(() => stop(new Error('owned child deadline; effect/collection unknown')),timeout);
    const observe = (chunk: Buffer, stderr: boolean): void => {
      bytes += chunk.length;
      if (bytes > 65536) stop(new Error('owned output bound'));
      else if (stderr) error += chunk.toString('utf8'); else out += chunk.toString('utf8');
    };
    child.stdout.on('data',(b: Buffer) => observe(b,false)); child.stderr.on('data',(b: Buffer) => observe(b,true));
    child.on('error',(e: Error) => { fault ??= e; unknown = true; });
    child.stdin.on('error',(e: Error) => { fault ??= e; unknown = true; }); child.stdin.end(input);
    // close joins the owned process and both captured pipes. Exit alone is insufficient.
    child.once('close',(code,signal) => {
      clearTimeout(timer);
      void persist(id+'-'+label+'.collected.json',{...tuple,collected:child.pid !== undefined,pid:child.pid,code,signal,out,error,unknown:!!fault})
        .then(() => { if (fault || code !== 0 || signal) reject(fault ?? new Error(label+' failed '+code)); else resolve(out); },reject);
    });
    // An uncollected process/pipe never becomes success. The entire fresh CI job
    // is the retained resource boundary; no following effect/cleanup is admitted.
    setTimeout(() => { if (child.exitCode === null || child.stdout.readable || child.stderr.readable) {
      unknown = true; reject(new Error('actual collection unproved; retain until job teardown'));
    } },timeout+10000).unref();
  });
}
async function actor(mode: string): Promise<void> { await run(mode,observer,[mode,root,tuple.nonce,source],'',140000); }
async function ledger(): Promise<Ledger> { return JSON.parse((await boundedRead(path.join(control,'ownership.json'))).toString()) as Ledger; }
async function setup(operation: 'enable'|'disable', route=false): Promise<void> {
  const current = await ledger();
  const args = ['setup-notifications',operation,'--control-root',control,'--runtime-root',runtime,
    '--global-config',global,'--expected-generation',String(current.Generation),'--json'];
  if (route) args.push('--navigation','desktop_thread','--allow-unknown-caller','false','--allow-caller-asserted','true');
  await run(route ? 'setup-route' : operation === 'enable' ? 'preserved-route-readiness-and-policy-write' : 'disable',original,args);
}
async function acquire(): Promise<void> {
  const initial = 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix';
  let url = initial; const deadline = Date.now()+120000; const h = await open(path.join(root,'client.msix'),'wx');
  let total = 0;
  try {
    for (let redirects = 0; redirects <= 5; ++redirects) {
      assert(Date.now() < deadline);
      const response = await fetch(url,{redirect:'manual',signal:AbortSignal.timeout(deadline-Date.now())});
      if ([301,302,303,307,308].includes(response.status)) {
        assert(redirects < 5); const target = new URL(response.headers.get('location')!,url);
        assert(target.protocol === 'https:' && (target.hostname === 'persistent.oaistatic.com' || target.hostname.endsWith('.oaistatic.com')));
        await response.body?.cancel(); url = target.href; continue;
      }
      assert(response.ok && response.body); const reader = response.body.getReader();
      for (;;) { const item = await reader.read(); if (item.done) break;
        total += item.value.length; assert(total <= 1073741824 && Date.now() < deadline); await h.writeFile(item.value);
      }
      assert(total > 0); await h.sync(); break;
    }
  } finally { await h.close(); }
  await persist('acquisition.json',{...tuple,official_url:initial,actual_url:url,bytes:total,sha256:await hashFile(path.join(root,'client.msix'))});
}
async function routeA(): Promise<{binding: {SnapshotPath: string; SHA256: string}; snapshot: string[]; raw: Buffer}> {
  const l = await ledger(); assert.equal(l.WindowsRetained?.length,1); const binding = l.WindowsRetained[0]!;
  const raw = await boundedRead(binding.SnapshotPath); assert.equal(sha(raw),binding.SHA256);
  const snapshot = envelope(raw,1,10);
  assert.equal(snapshot[3],(JSON.parse((await boundedRead(path.join(artifact,'manifest.json'))).toString()) as Artifact).helperSHA256);
  assert.equal(snapshot[6],'OpenAI.Codex'); assert.equal(snapshot[7],'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B');
  assert.equal(snapshot[8],'OpenAI.Codex_2p2nqsd0c76g0');
  return {binding,snapshot,raw};
}
async function removeProducer(): Promise<void> {
  await setup('disable');
  await run('remove-producer',original,['internal-install-runtime','--remove','--target',bin,
    '--entry',entry,'--control-root',control,'--consumer','claude-hooks']);
  // This literal synthetic config is owned by this controller, never a project/client config.
  await unlink(global); removed = true;
  await persist('cleanup.json',{...tuple,producer_removed:true,config_removed:true,retained_until_job_teardown:true,
    generation_deletion:false,vendor_deletion:false,global_sdk_drain_claim:false});
}
async function main(): Promise<void> {
  await mkdir(root); await mkdir(stage); await mkdir(runtime); await mkdir(bin);
  const manifest = JSON.parse((await boundedRead(path.join(artifact,'manifest.json'))).toString()) as Artifact;
  assert.equal(manifest.source,source); assert.equal(manifest.helperExecuted,false); assert.equal(manifest.installedQualified,false);
  const binary = await boundedRead(original,32*1024*1024), helper = await boundedRead(path.join(artifact,'helper.exe'),16*1024*1024);
  assert.equal(machine(binary),0x8664); assert.equal(machine(helper),0x8664);
  assert.equal(sha(binary),manifest.binarySHA256); assert.equal(sha(helper),manifest.helperSHA256);
  assert(binary.includes(helper));
  await persist('scope.json',{...tuple,scenario,run_id:env.GITHUB_RUN_ID,run_attempt:env.GITHUB_RUN_ATTEMPT,
    observer_sha:await hashFile(observer),artifact:manifest,whole_job_fresh_profile:true});
  await actor('prerequisites');
  const prerequisite = prerequisiteAdmission(JSON.parse((await boundedRead(path.join(root,'prerequisites.json'))).toString()),tuple,'pre-setup');
  await acquire(); await actor('archive');
  const intake = JSON.parse((await boundedRead(path.join(root,'archive.json'))).toString()) as Tuple & {archive_sha: string; full_name: string};
  assert.equal(intake.source,source); assert.equal(intake.nonce,tuple.nonce);
  if (phase === 'archive_intake') {
    await persist('intake-only.json',{...tuple,outcome:'signed_archive_intake_only',intake,installed:false}); return;
  }
  // A separate reviewed intake freezes authority before even TEST package deployment.
  assert.equal(intake.archive_sha,env.TEST_EXPECTED_ARCHIVE_SHA);
  assert.equal(intake.full_name,env.TEST_EXPECTED_FULL_NAME);
  await actor('deploy');
  await actor('prerequisites-post-deploy');
  prerequisiteAdmission(JSON.parse((await boundedRead(path.join(root,'post-deployment-prerequisites.json'))).toString()),tuple,'pre-setup',prerequisite);
  const staged = await open(path.join(stage,entry),'wx'); try { await staged.writeFile(binary); await staged.sync(); } finally { await staged.close(); }
  await run('install',original,['internal-install-runtime','--stage',stage,'--target',bin,'--entry',entry,'--control-root',control]);
  const config = await open(global,'wx');
  try { await config.writeFile(JSON.stringify({notifications:{desktop:{enabled:true,sound:false,clickToFocus:true}}})); await config.sync(); }
  finally { await config.close(); }
  await setup('enable',true);
  await actor('prerequisites-post-setup');
  prerequisiteAdmission(JSON.parse((await boundedRead(path.join(root,'post-setup-prerequisites.json'))).toString()),tuple,'post-setup',prerequisite);
  const a = await routeA(); await setup('enable');
  await persist('context.json',{provider:'codex',session:thread,locality:'local',interface:'desktop'});
  const receiptRaw = await run('one-show',original,['notify','--context-file',path.join(root,'context.json')],
    JSON.stringify({title,body,category:'info',request_id:'TEST-'+tuple.nonce,navigation:'required'}));
  const receipt = JSON.parse(receiptRaw) as {status: string; replayed: boolean; request_id: string};
  assert.equal(receipt.status,'submitted'); assert.equal(receipt.replayed,false); assert.equal(receipt.request_id,'TEST-'+tuple.nonce);
  await persist('sender.json',{...tuple,collected:true,code:0,receipt});
  const generation = path.dirname(a.binding.SnapshotPath), attempts = path.join(generation,'attempts');
  const recordNames = await readdir(path.join(generation,'records')); assert.equal(recordNames.length,1);
  const record = envelope(await boundedRead(path.join(generation,'records',recordNames[0]!)),2,5);
  assert.equal(record[3],thread); assert.equal(record[4],a.binding.SHA256); assert.equal(record[0],a.snapshot[0]);
  const before = await readdir(attempts); assert(before.length <= 8);
  const shows = before.filter(n => n.endsWith('.intent')); assert.equal(shows.length,1);
  const show = (await boundedRead(path.join(attempts,shows[0]!))).toString();
  assert(new RegExp(`^WinShow1 [a-f0-9]{32} ${record[1]} ${a.binding.SHA256} [0-9]+\\n$`).test(show));
  assert.equal((await boundedRead(path.join(attempts,shows[0]!.replace('.intent','.result')))).toString(),'show_returned=1 effect_entered=1\n');
  await persist('route.json',{...tuple,snapshot_path:a.binding.SnapshotPath,snapshot_sha:a.binding.SHA256,reference:record[1],thread,title,body});
  if (scenario === 'retained') {
    await setup('enable',true); const b = await ledger(); assert.equal(b.WindowsRetained?.length,2);
    assert(b.WindowsRetained![1]!.SnapshotPath !== a.binding.SnapshotPath);
    await removeProducer(); assert((await boundedRead(a.binding.SnapshotPath)).equals(a.raw));
  }
  await actor('click');
  const receiver = JSON.parse((await boundedRead(path.join(root,'receiver.json'))).toString()) as Receiver;
  const names = new Set(await readdir(attempts)); assert(names.size <= 24);
  const callbacks = [...names].filter(n => n.endsWith('.intent') && !before.includes(n)); assert.equal(callbacks.length,1);
  const id = callbacks[0]!.replace('.intent','');
  const text = async (suffix: string): Promise<string> => (await boundedRead(path.join(attempts,id+suffix),4096)).toString();
  const joined = callbackJoin(tuple,receiver,a.snapshot,record,id,await text('.intent'),await text('.result'),await text('.drained'),
    await text('.query'),await text('.launch'),await text('.worker-returned'),names);
  assert((await boundedRead(a.binding.SnapshotPath)).equals(a.raw));
  if (!removed) await removeProducer();
  await persist('qualification.json',{...tuple,outcome:'scoped_installed_handoff',scenario,joined,receiver,
    uri_source:'derived_from_immutable_record',retained_until_job_teardown:true,
    rollback_qualified:false,old_floor4_refusal_qualified:false,rendered_chat_claim:false,authenticated_click_claim:false});
}
try { await main(); } catch (error) {
  // No automatic retry and no destructive cleanup after a possibly entered effect.
  if (await readFile(path.join(root,'scope.json')).catch(() => null))
    await persist('failure.json',{...tuple,error:String(error),unknown,retained_until_job_teardown:true}).catch(() => {});
  throw error;
}
