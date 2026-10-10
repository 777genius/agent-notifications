// Offline release smoke, adapted from the reviewed v1.48.5 observer. No provider credentials, production project, trust bypass or synthetic Stop.
// Protocol: openai/codex rust-v0.152.0 core/tests/common/responses.rs;
// app-server-protocol/schema/typescript/v2/{HooksListParams,HookMetadata}.ts.
import { strict as assert } from 'node:assert';
import { createHash, randomUUID } from 'node:crypto';
import { appendFile, chmod, cp, lstat, mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { createServer, type IncomingMessage, type ServerResponse, type Server } from 'node:http';
import { basename, join, resolve } from 'node:path';
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { createInterface } from 'node:readline';
import { gunzipSync, inflateSync, zstdDecompressSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';
import { portableEntries, nativeEntries } from './zip-contract.mts';
import type { InitializeParams } from './schema-0.162/InitializeParams.js';
import type { HooksListResponse } from './schema-0.162/v2/HooksListResponse.js';

// Every phase, including the native Stop observer, carries immutable custody inputs.
// Reject missing, duplicate and unknown options before filesystem/process access.
const commandLine = process.argv.slice(2);
const immutableKeys = ['--candidate-sha','--operator-sha','--version','--test-thread','--portable-zip-sha256','--native-zip-sha256','--native-zip','--native-custody-json','--native-custody-sha256','--signing-attempt','--signing-run','--asset-sha256','--binary-sha256','--run-json-sha256'] as const;
const options = new Map<string,string>(); const positional: string[] = [];
for (let index=0; index<commandLine.length; index++) {
  const arg=commandLine[index]!;
  if (!arg.startsWith('--')) { positional.push(arg); continue; }
  assert(immutableKeys.some(key=>key===arg) && !options.has(arg), 'unknown_or_duplicate_custody_option');
  const value=commandLine[++index]; assert(value && !value.startsWith('--'), 'custody_option_value_required');
  options.set(arg,value);
}
for (const key of immutableKeys) assert(options.has(key), 'all_immutable_custody_options_required_before_IO');
const candidate = options.get('--candidate-sha')!;
const operator = options.get('--operator-sha')!;
const version = options.get('--version')!;
const approvedTESTThread = options.get('--test-thread')!;
const portableZipSHA256 = options.get('--portable-zip-sha256')!;
const nativeZipSHA256 = options.get('--native-zip-sha256')!;
const nativeZipInput = options.get('--native-zip')!;
const nativeCustodyInput=options.get('--native-custody-json')!;
const nativeCustodySHA256=options.get('--native-custody-sha256')!;
const signingAttempt=options.get('--signing-attempt')!;
assert(/^[1-9][0-9]*$/.test(signingAttempt)&&nativeCustodyInput.startsWith('/'),'actual_signing_attempt_and_custody_path');
assert(/^[a-f0-9]{40}$/.test(candidate) && /^[a-f0-9]{40}$/.test(operator),'full_final_source_and_operator_SHA_required');
assert(/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version),'stable_release_version_required');
assert(/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(approvedTESTThread),'actual_TEST_thread_UUID_required');
assert(nativeZipInput.startsWith('/'),'absolute_native_draft_zip_required');
const signingRun = options.get('--signing-run')!;
const draftArmAssetSHA256 = options.get('--asset-sha256')!;
const draftArmBinarySHA256 = options.get('--binary-sha256')!;
const signingRunCustodySHA256 = options.get('--run-json-sha256')!;
assert(/^[1-9][0-9]*$/.test(signingRun), 'exact_signing_run_id_required_before_IO');
assert([draftArmAssetSHA256,draftArmBinarySHA256,signingRunCustodySHA256,portableZipSHA256,nativeZipSHA256,nativeCustodySHA256].every(value=>/^[a-f0-9]{64}$/.test(value)),
  'verified_ARM_asset_binary_and_signing_custody_required_before_IO');
assert.equal(draftArmAssetSHA256,draftArmBinarySHA256,'raw_draft_asset_is_exact_executable');
assert.equal(basename(nativeZipInput),'ClaudeNotifier.app.zip','exact_native_asset_name');
const custodyArguments=immutableKeys.flatMap(key=>[key,options.get(key)!]);
type Handler = { type: string; command: string; timeout?: number };
type Hooks = { hooks: Record<string, { matcher?: string; hooks: Handler[] }[]> };
type Meta = { root: string; source: string; bundle: string; sha256: string; codex: string;
  candidate: string; version: string; signingRun: string; operator: string; approvedTESTThread: string; draftAssetSHA256: string; signingCustodySHA256: string; portableZipSHA256: string; nativeZipSHA256: string; nativeCustodySHA256:string;signingAttempt:string;
  harnessHashes: Record<string,string>; codexSHA256:string; node: string; nonce: string; modelPort: number; webhookPort: number; hooksHash: string; command: string; observer: string };
type Hook = { eventName: string; command?: string; key: string; enabled: boolean; sourcePath: string;
  trustStatus: string; currentHash: string; isManaged: boolean; handlerType: string };
type Json = Record<string, unknown>;
const [phase, rawRoot, ...args] = positional;
assert(rawRoot && phase && ['prepare','server','tui','turn','tap','verify'].includes(phase), 'phase and TEST root required');
assert.equal(args.length,phase==='prepare'?8:0,'exact_phase_argument_count');
assert(process.platform==='darwin' && process.arch==='arm64','native_macOS_arm64_observer_required');
const root = resolve(rawRoot).replace(/^\/tmp\//, '/private/tmp/');
assert(/^\/private\/tmp\/TEST-an-codex-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(root), 'only fresh /private/tmp/TEST-an-codex-UUID');
const p = (name: string) => join(root, name);
const hash = (b: string | Buffer) => createHash('sha256').update(b).digest('hex');
const json = async <T,>(name: string): Promise<T> => JSON.parse(await readFile(p(name), 'utf8')) as T;
const save = (name: string, value: unknown) => writeFile(p(name), JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
const wait = (ms: number) => new Promise<void>(r => setTimeout(r, ms));
const shellQuote = (v: string) => "'" + v.replaceAll("'", "'\\''") + "'";
function env(_m: Meta): NodeJS.ProcessEnv {
  return { PATH: '/usr/bin:/bin:/usr/sbin:/sbin', HOME: p('home'), USERPROFILE: p('home'), CODEX_HOME: p('codex'),
    CLAUDE_HOME: p('claude'), CLAUDE_CONFIG_DIR: p('claude'), APPDATA: p('appdata'), LOCALAPPDATA: p('localappdata'),
    XDG_CONFIG_HOME: p('config'), XDG_CACHE_HOME: p('cache'), XDG_DATA_HOME: p('data'), XDG_STATE_HOME: p('state'),
    XDG_RUNTIME_DIR: p('run'), XDG_CONFIG_DIRS: p('config-dirs'), XDG_DATA_DIRS: p('data-dirs'),
    TMPDIR: p('tmp'), TMP: p('tmp'), TEMP: p('tmp'), TERM: 'xterm-256color', LANG: 'en_US.UTF-8' };
}
async function run(m: Meta, executable: string, argv: string[], cwd = p('TEST-project')) {
  const commandEnv = env(m);
  if (executable === '/bin/bash' && argv[0] === p('source-tree/swift-notifier/scripts/verify-signing.sh'))
    commandEnv.PATH = '/usr/bin:/bin:/usr/sbin:/sbin'; // spctl is a system verification tool in /usr/sbin.
  const child = spawn(executable, argv, { cwd, env: commandEnv, detached:true, stdio: ['ignore', 'pipe', 'pipe'] });
  let stdout = '', stderr = '', timedOut=false, spawnError:Error|undefined;
  const signal=(value:NodeJS.Signals)=>{if(child.pid){try{process.kill(-child.pid,value);}catch{}}};
  child.stdout.on('data', b => { stdout += b; if(stdout.length>8<<20)signal('SIGKILL'); });
  child.stderr.on('data', b => { stderr += b; if(stderr.length>8<<20)signal('SIGKILL'); });
  child.once('error',error=>{spawnError=error;});
  const closed=new Promise<number|null>(r=>child.once('close',r));
  const timer = setTimeout(() => {timedOut=true;signal('SIGKILL');}, 90_000);
  try { const code = await closed;
    if(m.root===root)await appendFile(p('closed-children.jsonl'),JSON.stringify({phase,kind:'command',processGroupManaged:true,pid:child.pid,executable,argv,closeObserved:true,code,signal:child.signalCode,timedOut,spawnError:spawnError?.message})+'\n',{mode:0o600});
    assert(!spawnError && !timedOut && stdout.length<=8<<20 && stderr.length<=8<<20,'command_spawn_timeout_or_output_bound');
    if (code !== 0) await save('failed-command.json', { executable, argv, code, stdout, stderr }).catch(() => undefined);
    assert.equal(code, 0, `${executable}: ${stderr}`); return { stdout, stderr, code, argv };
  } finally { clearTimeout(timer); }
}
async function hookList(m: Meta): Promise<Hook[]> {
  const child: ChildProcessWithoutNullStreams = spawn(m.codex, ['app-server'], { cwd: p('TEST-project'), env: env(m), detached:true });
  let closeObserved=false;const closed=new Promise<void>(r=>child.once('close',()=>{closeObserved=true;r();}));
  const lines = createInterface({ input: child.stdout }); let serial = 0;
  const pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  let stderr = ''; child.stderr.on('data', b => { stderr += b; });
  const failure = (error: Error) => { for (const request of pending.values()) request.reject(error); pending.clear(); };
  lines.on('line', line => { try{const r = JSON.parse(line) as { id?: number; result?: unknown; error?: unknown };
    if (r.id === undefined) return; const request = pending.get(r.id); if (!request) return;
    pending.delete(r.id); r.error ? request.reject(new Error(JSON.stringify(r.error))) : request.resolve(r.result);
  }catch(error){failure(error instanceof Error?error:new Error(String(error)));} });
  child.on('error', failure); child.on('exit', () => failure(new Error(`app-server exited: ${stderr}`)));
  const timer = setTimeout(() => failure(new Error('hooks/list timeout')), 20_000);
  const rpc = (method: string, params: unknown) => new Promise<unknown>((r, j) => {
    const id = ++serial; pending.set(id, { resolve: r, reject: j }); child.stdin.write(JSON.stringify({ id, method, params }) + '\n'); });
  try {
    const initialize:InitializeParams={clientInfo:{name:'TEST-an-codex-smoke',title:null,version:'1'},capabilities:{experimentalApi:true,requestAttestation:false,explicitGatewayOauth:true}};
    const init = await rpc('initialize',initialize);
    assert.equal((init as { codexHome: string }).codexHome, await realpath(p('codex')));
    child.stdin.write('{"method":"initialized"}\n');
    const result = await rpc('hooks/list', { cwds: [p('TEST-project')] }) as HooksListResponse;
    assert(result.data.length===1&&result.data[0].cwd===p('TEST-project'),'one_actual_TEST_hook_scope');
    assert(result.data.every(entry=>entry.errors.length===0&&entry.warnings.length===0),'hook_list_errors_or_warnings');
    await save('hooks-list.json', result); return result.data.flatMap(x => x.hooks);
  } finally { clearTimeout(timer); lines.close(); child.stdin.end();
    const signal=(value:NodeJS.Signals)=>{if(child.pid){try{process.kill(-child.pid,value);}catch{}}};
    signal('SIGTERM');
    await Promise.race([closed,wait(2000)]);
    if(!closeObserved)signal('SIGKILL');
    await closed;
    await appendFile(p('closed-children.jsonl'),JSON.stringify({phase,kind:'hooks-list',processGroupManaged:true,pid:child.pid,executable:m.codex,closeObserved:true,code:child.exitCode,signal:child.signalCode})+'\n',{mode:0o600});
  }
}
async function body(req: IncomingMessage): Promise<Buffer> {
  const chunks: Buffer[] = []; let bytes = 0;
  for await (const chunk of req) { bytes += chunk.length; assert(bytes <= 2 << 20, 'request limit'); chunks.push(Buffer.from(chunk)); }
  const raw = Buffer.concat(chunks); const encoding = req.headers['content-encoding'];
  return encoding === 'gzip' ? gunzipSync(raw) : encoding === 'deflate' ? inflateSync(raw) :
    encoding === 'zstd' ? zstdDecompressSync(raw) : (assert(!encoding || encoding === 'identity', `encoding ${encoding}`), raw);
}

async function regularFile(path: string, maximum: number): Promise<Buffer> {
  const st=await lstat(path); assert(st.isFile()&&!st.isSymbolicLink()&&st.size<=maximum,'bounded_regular_input_required');
  return readFile(path);
}
// Parse the actual ZIP central directory; extract only five exact regular leaves.
// No external ZIP tool gets to interpret attacker-controlled paths or symlinks.
if (phase === 'prepare') {
  const [sourceInput, bundleInput, sha256, modelPortInput, webhookPortInput, codexInput, draftAssetInput, custodyInput] = args;
  assert(sourceInput && bundleInput && sha256 && modelPortInput && webhookPortInput && codexInput && draftAssetInput && custodyInput,
    'prepare ROOT SOURCE PORTABLE_ZIP DOWNLOADED_BINARY_SHA256 MODEL_PORT WEBHOOK_PORT CODEX_EXECUTABLE DRAFT_ARM_ASSET SIGNING_RUN_JSON');
  assert(/^[a-f0-9]{64}$/.test(sha256));
  assert.equal(sha256,draftArmBinarySHA256);
  assert.equal(hash(await regularFile(await realpath(draftAssetInput),80<<20)),draftArmAssetSHA256);
  assert.equal(basename(draftAssetInput),'claude-notifications-darwin-arm64','exact_ARM_asset_name');
  assert.equal(basename(bundleInput),'agent-notify-portable-darwin-arm64.zip','exact_portable_asset_name');
  const custodyBytes=await regularFile(await realpath(custodyInput),2<<20); assert.equal(hash(custodyBytes),signingRunCustodySHA256);
  const custody=JSON.parse(custodyBytes.toString()) as {id:number;head_sha:string;conclusion:string;event:string;head_branch:string;
    actor:{login:string};triggering_actor:{login:string};path:string;run_attempt:number};
  assert(String(custody.id)===signingRun && String(custody.run_attempt)===signingAttempt && custody.head_sha===candidate && custody.conclusion==='success'
    && custody.event==='workflow_dispatch' && custody.head_branch==='release/macos-signing'
    && custody.path==='.github/workflows/macos-qualification.yml'
    && custody.actor.login==='777genius' && custody.triggering_actor.login==='777genius','exact_successful_owner_signing_run_custody');
  const source = await realpath(sourceInput), codex = await realpath(codexInput);
  assert((await lstat(codex)).isFile(),'actual_regular_Codex_executable_required');
  const portableZip=await regularFile(await realpath(bundleInput),96<<20); assert.equal(hash(portableZip),portableZipSHA256);
  const portable=portableEntries(portableZip);
  const portableManifest=JSON.parse(portable.get('plugin.json')!.toString()) as unknown;
  assert.deepEqual(portableManifest,{$schema:'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json',name:'agent-notify',version},'portable_version_identity');
  assert.deepEqual(JSON.parse(portable.get('mcp.json')!.toString()),{$schema:'https://agent-plugins.org/schemas/1.0.0/mcp.schema.json',mcpServers:{'agent-notify':{type:'stdio',command:'./bin/claude-notifications',args:[],env:{}}}},'portable_exact_MCP_contract');
  assert.equal(hash(portable.get('bin/claude-notifications')!),sha256,'portable_matches_raw_draft_binary');
  const nativeZip=await regularFile(nativeZipInput,32<<20);assert.equal(hash(nativeZip),nativeZipSHA256,'native_draft_ZIP_identity');
  const native=nativeEntries(nativeZip);
  const nativeCustodyBytes=await regularFile(nativeCustodyInput,64<<10);assert.equal(hash(nativeCustodyBytes),nativeCustodySHA256,'native_custody_receipt_SHA');
  const nativeCustody=JSON.parse(nativeCustodyBytes.toString()) as {schemaVersion:number;archiveSHA256:string;executableSHA256:string;attestationSHA256:string;sourceSHA:string;workflowSHA:string;workflowRef:string;runID:string;runAttempt:string;signatureVerified:boolean;signingTeam:string;bundleID:string};
  assert(nativeCustody.schemaVersion===1&&nativeCustody.archiveSHA256===nativeZipSHA256&&nativeCustody.executableSHA256===hash(native.get('ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern')!)&&nativeCustody.attestationSHA256===hash(native.get('ClaudeNotifier.app.managed-runtime.json')!),'native_custody_archive_executable_attestation');
  assert(nativeCustody.sourceSHA===candidate&&nativeCustody.workflowSHA===candidate&&nativeCustody.workflowRef==='777genius/agent-notifications/.github/workflows/macos-qualification.yml@refs/heads/release/macos-signing'&&nativeCustody.runID===signingRun&&nativeCustody.runAttempt===signingAttempt,'native_custody_exact_source_run_attempt');
  assert(nativeCustody.signatureVerified===true&&nativeCustody.signingTeam==='86399583GS'&&nativeCustody.bundleID==='com.777genius.agent-notifications','native_custody_publisher_identity');
  assert.equal((await run({} as Meta, '/usr/bin/git', ['-C', source, 'rev-parse', 'HEAD'], source)).stdout.trim(), candidate);
  await run({} as Meta, '/usr/bin/git', ['-C', source, 'diff', '--exit-code', 'HEAD'], source);
  assert(Number(process.versions.node.split('.')[0])>=24,'Node_24_or_newer_required');
  await regularFile('/usr/sbin/spctl',32<<20);
  const modelPort = Number(modelPortInput), webhookPort = Number(webhookPortInput);
  assert(modelPort !== webhookPort && [modelPort, webhookPort].every(n => Number.isInteger(n) && n > 1024 && n < 65536));
  await mkdir(root, { mode: 0o700 });
  assert.equal((await lstat(root)).mode&0o777,0o700,'private_TEST_root_required');
  assert.equal(await realpath(root),root,'physical fresh TEST root required');
  for (const dir of ['home', 'codex', 'claude', 'appdata', 'localappdata', 'config', 'cache', 'data', 'state', 'run',
    'config-dirs', 'data-dirs', 'tmp', 'TEST-project', 'bundle', 'source-tree', 'native-input', 'portable-input', 'home/.claude/claude-notifications-go']) await mkdir(p(dir), { recursive: true, mode: 0o700 });
  const preflightMeta={root,codex} as Meta;
  const actualCodex=await run(preflightMeta,codex,['--version'],root);
  assert.equal(actualCodex.stdout.trim(),'codex-cli 0.162.0','review_new_protocol_before_using_another_Codex_version');
  const protocol=await run(preflightMeta,codex,['app-server','--help'],root);
  assert(protocol.stdout.includes('generate-ts'),'supported_actual_app_server_protocol_required');
  await save('preflight.json',{node:process.execPath,nodeVersion:process.versions.node,arch:process.arch,codex,codexVersion:actualCodex.stdout.trim(),childPATH:env(preflightMeta).PATH,spctl:'/usr/sbin/spctl'});
  // All wrapper/config bytes come from the exact committed source, never dirty working files.
  await run({} as Meta,'/usr/bin/git',['-C',source,'archive','--format=tar','--output',p('candidate-source.tar'),candidate],source);
  await run({} as Meta,'/usr/bin/tar',['-xf',p('candidate-source.tar'),'-C',p('source-tree')],root);
  for (const dir of ['bin','sounds','config','skills','.claude-plugin'])
    await cp(p('source-tree/'+dir),p('bundle/'+dir),{recursive:true,dereference:false,verbatimSymlinks:true});
  for(const [name,bytes] of portable){const output=p('portable-input/'+name);await mkdir(join(output,'..'),{recursive:true,mode:0o700});await writeFile(output,bytes,{mode:name==='bin/claude-notifications'?0o700:0o600});}
  await cp(p('portable-input/bin/claude-notifications'),p('bundle/bin/claude-notifications'));
  await chmod(p('bundle/bin/claude-notifications'),0o700);
  await chmod(p('bundle/bin'),0o700); // The provenance intent must share this private executable stage.
  for(const [name,bytes] of native){const output=p('native-input/'+name);await mkdir(join(output,'..'),{recursive:true,mode:0o700});await writeFile(output,bytes,{mode:name==='ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern'?0o700:0o600});}
  await run({} as Meta,'/bin/bash',[p('source-tree/swift-notifier/scripts/verify-signing.sh'),p('native-input/ClaudeNotifier.app'),'--notarized'],root);
  await cp(p('native-input/ClaudeNotifier.app'),p('bundle/bin/ClaudeNotifier.app'),{recursive:true,dereference:false,verbatimSymlinks:true});
  await cp(p('native-input/ClaudeNotifier.app.managed-runtime.json'),p('bundle/bin/ClaudeNotifier.app.managed-runtime.json'));
  const claudeManifest=JSON.parse(await readFile(p('bundle/.claude-plugin/plugin.json'),'utf8')) as {version:string};
  assert.equal(claudeManifest.version,version,'source_manifest_version_matches_draft');
  const m: Meta = { root, source, bundle: p('bundle'), sha256, codex, candidate, version, signingRun, operator, approvedTESTThread,
    draftAssetSHA256:draftArmAssetSHA256,signingCustodySHA256:signingRunCustodySHA256,portableZipSHA256,nativeZipSHA256,nativeCustodySHA256,signingAttempt,
    harnessHashes:{},codexSHA256:hash(await readFile(codex)),node: process.execPath, nonce: `TEST${randomUUID().replaceAll('-', '')}`,
    modelPort, webhookPort, hooksHash: '', command: '', observer: '' };
  assert.equal(hash(await readFile(p('bundle/bin/claude-notifications'))), sha256);
  await save('native-custody.json',nativeCustody);await save('signing-run.json',custody);
  await writeFile(p('home/.claude/settings.json'), '{"TEST_existing_claude_canary":true}\n');
  await writeFile(p('home/.claude/claude-notifications-go/config.json'), JSON.stringify({ notifications: { desktop: { enabled: false },
    webhook: { enabled: true, preset: 'slack', url: `http://127.0.0.1:${webhookPort}/webhook` } } }));
  const canary = await readFile(p('home/.claude/settings.json')); const config = await readFile(p('home/.claude/claude-notifications-go/config.json'));
  const actualVersion = await run(m, p('bundle/bin/claude-notifications'), ['version']); assert.equal(actualVersion.stdout.trim(), 'claude-notifications v'+version);
  await run(m,p('bundle/bin/claude-notifications'),['setup-products','prepare','--products','codex','--codex-executable',codex,'--skip-agent-notify','--intent-file',p('bundle/bin/source-proof.json'),'--plain']);
  const sourceProof=await json<{provenance:{SourceCommit:string;SHA256:string;Version:string}}>('bundle/bin/source-proof.json');
  assert(sourceProof.provenance.SourceCommit===candidate&&sourceProof.provenance.SHA256===sha256&&sourceProof.provenance.Version===version,'actual_binary_source_version_SHA_proof');
  await save('codex-version.json', await run(m, codex, ['--version']));
  for (let i = 0; i < 2; i++) {
    await save(`setup-${i}.json`, await run(m, p('bundle/bin/claude-notifications'), ['setup-codex', '--plugin-root', p('bundle'), '--skip-agent-notify']));
    const hooks = await readFile(p('codex/hooks.json'));
    if (!i) await writeFile(p('setup-hooks.json'), hooks); else assert.deepEqual(hooks, await readFile(p('setup-hooks.json')));
    assert.deepEqual(await readFile(p('home/.claude/settings.json')), canary);
    assert.deepEqual(await readFile(p('home/.claude/claude-notifications-go/config.json')), config);
  }
  await cp(fileURLToPath(import.meta.url), p('observer.mts'));
  await cp(fileURLToPath(new URL('./zip-contract.mts',import.meta.url)),p('zip-contract.mts'));
  m.harnessHashes={'observer.mts':hash(await readFile(p('observer.mts'))),'zip-contract.mts':hash(await readFile(p('zip-contract.mts')))};
  const hooks = JSON.parse(await readFile(p('codex/hooks.json'), 'utf8')) as Hooks;
  assert.equal(hooks.hooks.Stop.length, 1); assert.equal(hooks.hooks.Stop[0].hooks.length, 1);
  m.command = hooks.hooks.Stop[0].hooks[0].command;
  assert.equal(m.command, `sh ${shellQuote(p('codex/claude-notifications-go/bin/codex-hook-wrapper.sh'))} handle-hook Stop --product codex`);
  m.observer = `${shellQuote(m.node)} --experimental-strip-types ${shellQuote(p('observer.mts'))} tap ${shellQuote(root)} ${custodyArguments.map(shellQuote).join(' ')}`;
  hooks.hooks.Stop.push({ hooks: [{ type: 'command', command: m.observer, timeout: 10 }] });
  await writeFile(p('codex/hooks.json'), JSON.stringify(hooks, null, 2)); m.hooksHash = hash(await readFile(p('codex/hooks.json')));
  await writeFile(p('codex/config.toml'), `model = "gpt-5.1"\nmodel_provider = "TEST_fixture"\ncli_auth_credentials_store = "file"\napproval_policy = "never"\nsandbox_mode = "read-only"\nweb_search = "disabled"\n[analytics]\nenabled = false\n[features]\nhooks = true\n[model_providers.TEST_fixture]\nname = "TEST loopback"\nbase_url = "http://127.0.0.1:${modelPort}/v1"\nwire_api = "responses"\nrequires_openai_auth = false\nsupports_websockets = false\nrequest_max_retries = 0\nstream_max_retries = 0\n`);
  await save('meta.json', m); console.log(JSON.stringify({ root, candidate, nonce: m.nonce, command: m.command, observer: m.observer }));
} else {
  assert.equal(await realpath(root),root,'physical prepared TEST root required');
  const m = await json<Meta>('meta.json'); assert.equal(m.root, root);
  assert(m.operator===operator && m.approvedTESTThread===approvedTESTThread && m.candidate===candidate && m.version===version && m.signingRun===signingRun && m.sha256===draftArmBinarySHA256
    && m.draftAssetSHA256===draftArmAssetSHA256 && m.signingCustodySHA256===signingRunCustodySHA256 && m.portableZipSHA256===portableZipSHA256 && m.nativeZipSHA256===nativeZipSHA256 && m.nativeCustodySHA256===nativeCustodySHA256 && m.signingAttempt===signingAttempt,'same_verified_preparation_custody');
  assert.equal((await lstat(root)).mode&0o777,0o700,'private_TEST_root_required');
  assert.equal(hash(await readFile(m.codex)),m.codexSHA256,'same_actual_Codex_executable_required');
  assert.equal(hash(await readFile(fileURLToPath(import.meta.url))),m.harnessHashes['observer.mts'],'same_executed_observer_required');
  for(const [name,digest] of Object.entries(m.harnessHashes))assert.equal(hash(await readFile(p(name))),digest,'same_staged_observer_required');
  if (phase === 'tap') {
    const chunks: Buffer[] = []; for await (const chunk of process.stdin) chunks.push(Buffer.from(chunk));
    const raw = Buffer.concat(chunks); assert(raw.length < 1 << 20); const payload = JSON.parse(raw.toString()) as Json;
    assert.equal(payload.hook_event_name, 'Stop'); assert.equal(await realpath(String(payload.cwd)), await realpath(p('TEST-project')));
    await appendFile(p('native-stop.jsonl'), JSON.stringify(payload) + '\n', { mode: 0o600 });
  } else if (phase === 'server') {
    await writeFile(p('server-started.json'),JSON.stringify({startedAt:Date.now(),pid:process.pid}),{flag:'wx',mode:0o600});
    let failed = false; const servers: Server[] = []; const startedAt = Date.now();
    try { for (const kind of ['model', 'webhook'] as const) {
      const server = createServer(async (req: IncomingMessage, res: ServerResponse) => {
        try {
          assert.equal(req.method, 'POST'); assert.equal(req.url, kind === 'model' ? '/v1/responses' : '/webhook');
          assert(!req.headers.authorization, 'fixture must not receive credentials');
          const parsed = JSON.parse((await body(req)).toString()) as Json;
          await appendFile(p(`${kind}.jsonl`), JSON.stringify({ at: Date.now(), body: parsed }) + '\n', { mode: 0o600 });
          if (kind === 'webhook') { res.writeHead(200); res.end('{}'); return; }
          const previous = (await readFile(p('model.jsonl'), 'utf8')).trim().split('\n'); assert.equal(previous.length, 1, 'one model request only');
          const events = [{ type: 'response.created', response: { id: m.nonce } },
            { type: 'response.output_item.done', item: { type: 'message', role: 'assistant', id: `msg${m.nonce}`, content: [{ type: 'output_text', text: `Completed ${m.nonce}` }] } },
            { type: 'response.completed', response: { id: m.nonce, usage: { input_tokens: 0, output_tokens: 0, total_tokens: 0 } } }];
          res.writeHead(200, { 'content-type': 'text/event-stream' });
          res.end(events.map(e => `event: ${e.type}\ndata: ${JSON.stringify(e)}\n\n`).join(''));
        } catch (error) { failed = true; await appendFile(p('server-errors.jsonl'), JSON.stringify({ error: String(error) }) + '\n'); res.writeHead(500); res.end(); }
      });
      await new Promise<void>((r, j) => { server.once('error', j); server.listen(kind === 'model' ? m.modelPort : m.webhookPort, '127.0.0.1', r); }); servers.push(server);
    } } catch (error) { for (const server of servers) server.close(); throw error; }
    await save('server-ready.json', { pid: process.pid, startedAt, root });
    const shutdown = async () => { for (const server of servers) await new Promise<void>(r => server.close(() => r()));
      await save('server-closed.json', { pid: process.pid, failed, closedAt: Date.now() }); process.exit(failed ? 1 : 0); };
    process.once('SIGTERM', () => { void shutdown(); }); process.once('SIGINT', () => { void shutdown(); });
    console.log('Fixture ready. Use tui, review /hooks, trust only TEST handlers, exit; then turn.');
  } else if (phase === 'tui') {
    assert(process.stdin.isTTY&&process.stdout.isTTY,'genuine_controlling_TTY_required_for_hooks_trust');
    const child = spawn(m.codex, ['-C', p('TEST-project')], { cwd: p('TEST-project'), env: env(m), stdio: 'inherit' });
    let spawnError:Error|undefined; child.once('error',error=>{spawnError=error;});
    const code=await new Promise<number|null>(r=>child.once('close',r));
    await appendFile(p('closed-children.jsonl'),JSON.stringify({phase,kind:'trust-tui',pid:child.pid,executable:m.codex,closeObserved:true,code,signal:child.signalCode})+'\n',{mode:0o600});
    assert(!spawnError && code===0,'real_trust_TUI_successful_close_required');
  } else if (phase === 'turn') {
    assert.equal(hash(await readFile(p('codex/hooks.json'))), m.hooksHash, 'hook definitions changed since preparation');
    const hooks = await hookList(m);
    for (const command of [m.command, m.observer]) { const matches = hooks.filter(h => h.eventName === 'stop' && h.command === command);
      assert.equal(matches.length, 1); assert(matches[0].enabled && !matches[0].isManaged && matches[0].trustStatus === 'trusted'); assert.match(matches[0].currentHash, /^sha256:[a-f0-9]{64}$/); }
    await writeFile(p('turn-started.json'), JSON.stringify({ startedAt: Date.now(), candidate, nonce: m.nonce }), { flag: 'wx', mode: 0o600 });
    const result = await run(m, m.codex, ['exec', '--json', '--skip-git-repo-check', `Reply exactly: Completed ${m.nonce}. Do not call tools.`]);
    await writeFile(p('turn.jsonl'), result.stdout); await writeFile(p('turn.stderr'), result.stderr); await save('turn-result.json', result);
    await wait(3000); await save('quiet-tail.json', { finishedAt: Date.now(), webhookHash: hash(await readFile(p('webhook.jsonl'))), stopHash: hash(await readFile(p('native-stop.jsonl'))) });
  } else if (phase === 'verify') {
    const lines = async <T,>(name: string) => (await readFile(p(name), 'utf8')).trim().split('\n').filter(Boolean).map(x => JSON.parse(x) as T);
    const result = await json<{ code: number; argv: string[] }>('turn-result.json'); assert.equal(result.code, 0); assert(!result.argv.some(x => x.includes('bypass')));
    const listed = await json<{ data: { hooks: Hook[] }[] }>('hooks-list.json');
    for (const command of [m.command, m.observer]) {
      const found = listed.data.flatMap(x => x.hooks).filter(h => h.eventName === 'stop' && h.command === command);
      assert.equal(found.length, 1); assert(found[0].enabled && !found[0].isManaged && found[0].trustStatus === 'trusted');
      assert.equal(found[0].sourcePath, p('codex/hooks.json')); assert.match(found[0].currentHash, /^sha256:[a-f0-9]{64}$/);
    }
    const events = await lines<Json>('turn.jsonl'); assert.equal(events.filter(x => x.type === 'turn.completed').length, 1);
    assert.equal(events.filter(x => x.type === 'thread.started').length, 1); assert(!events.some(x => x.type === 'turn.failed'));
    const observationalKinds = ['agent_message', 'reasoning', 'error', 'todo_list'];
    assert(!events.some(x => { const item = x.item as Json | undefined; return item && !observationalKinds.includes(String(item.type)); }), 'actual tool execution or unknown native item');
    const message = events.filter(x => x.type === 'item.completed').map(x => x.item as Json).filter(x => x?.type === 'agent_message');
    assert.equal(message.length, 1); assert.equal(message[0].text, `Completed ${m.nonce}`);
    const stop = await lines<Json>('native-stop.jsonl'); assert.equal(stop.length, 1); assert.equal(stop[0].hook_event_name, 'Stop');
    assert.equal(stop[0].last_assistant_message, `Completed ${m.nonce}`); assert.equal(await realpath(String(stop[0].cwd)), await realpath(p('TEST-project')));
    const thread = events.find(x => x.type === 'thread.started'); assert.equal(stop[0].session_id, thread?.thread_id); assert(stop[0].turn_id);
    const webhook = await lines<{ body: unknown }>('webhook.jsonl'); assert.equal(webhook.length, 1); assert(JSON.stringify(webhook[0].body).includes(m.nonce));
    assert.equal((await lines<Json>('model.jsonl')).length, 1);
    const quiet = await json<{ webhookHash: string; stopHash: string }>('quiet-tail.json');
    assert.equal(hash(await readFile(p('webhook.jsonl'))), quiet.webhookHash); assert.equal(hash(await readFile(p('native-stop.jsonl'))), quiet.stopHash);
    const children=await lines<{phase:string;kind:string;pid:number;processGroupManaged?:boolean;closeObserved:boolean;code:number|null;signal:string|null;timedOut?:boolean;spawnError?:string}>('closed-children.jsonl');
    assert(children.every(child=>child.closeObserved&&!child.timedOut&&!child.spawnError),'every_observed_child_closed_without_timeout');
    for(const [expectedPhase,kind] of [['tui','trust-tui'],['turn','hooks-list']] as const)
      assert.equal(children.filter(child=>child.phase===expectedPhase&&child.kind===kind).length,1,'one_actual_trust_TUI_and_app_server_closure');
    const turns=children.filter(child=>child.phase==='turn'&&child.kind==='command');assert.equal(turns.length,1);assert.equal(turns[0].code,0);
    for(const child of children){assert(Number.isInteger(child.pid)&&child.pid>0,'actual_started_child_PID_required');
      for(const pid of child.processGroupManaged?[child.pid,-child.pid]:[child.pid]){try{process.kill(pid,0);throw new Error('owned child or group is still live');}catch(error){assert.equal((error as NodeJS.ErrnoException).code,'ESRCH');}}}
    const closed = await json<{ failed: boolean; pid: number }>('server-closed.json'); assert.equal(closed.failed, false);
    try { process.kill(closed.pid, 0); throw new Error('owned server is still live'); } catch (e) { assert.equal((e as NodeJS.ErrnoException).code, 'ESRCH'); }
    assert.equal(hash(await readFile(p('codex/hooks.json'))), m.hooksHash); assert.equal(hash(await readFile(p('bundle/bin/claude-notifications'))), m.sha256);
    assert.equal(hash(await readFile(p('codex/claude-notifications-go/bin/claude-notifications'))), m.sha256);
    await save('smoke-report.json', { status: 'qualified', candidate, version, operator, approvedTESTThread, signingRun, draftAssetSHA256:m.draftAssetSHA256,
      harnessHashes:m.harnessHashes,codexSHA256:m.codexSHA256,signingCustodySHA256:m.signingCustodySHA256, portableZipSHA256:m.portableZipSHA256,nativeZipSHA256:m.nativeZipSHA256,nativeCustodySHA256:m.nativeCustodySHA256,signingAttempt:m.signingAttempt, fixtureModel:true,actualCLIHookExecution:true, artifactSha256: m.sha256, nonce: m.nonce,
      closedChildren:children.length, nativeTurn: 1, nativeStop: 1, correlatedWebhook: 1, toolCalls: 0, quietTailMs: 3000, scope: 'offline Codex Stop/webhook only',
      desktop: false, liveProvider: false, exclusiveFilesystemAudit: false, egressFirewallProof: false });
    console.log('Qualified offline native Stop/webhook smoke. Desktop and live-provider proof remain separate.');
  } else throw new Error('phase must be prepare, server, tui, turn, tap or verify');
}
