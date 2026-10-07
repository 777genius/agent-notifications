// Closed two-actor experiment; SDK support never qualifies notification/session/cold runtime behavior.
import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { closeSync, fstatSync, fsyncSync, lstatSync, mkdtempSync, openSync,
  readSync, readdirSync, realpathSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { catalog, outerSHA256 } from './runtime-catalog.ts';

const started = performance.now();
if (!process.env.RUNNER_TEMP) throw new Error('own runner temp required');
const root = mkdtempSync(join(process.env.RUNNER_TEMP, 'TEST-sdk-prerequisite-'));
const evidence: Record<string, unknown> = { sourceSHA: process.env.SOURCE_SHA, outcome: 'unknown',
  notificationEffects: 0, sdkNativeCallback: false, runtimeQualified: false, actors: [], automaticRetry: false };
function check(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }
function physical(path: string, directory = false): void {
  const st = lstatSync(path);
  check(!st.isSymbolicLink() && (directory ? st.isDirectory() : st.isFile() && st.nlink === 1) &&
    realpathSync(path).toLowerCase() === path.toLowerCase(), 'redirected/nonregular input');
}
function hash(path: string, cap = 64 << 20): string {
  physical(path); const fd = openSync(path, 'r');
  try {
    const before = fstatSync(fd); check(before.size <= cap, 'file bound');
    const digest = createHash('sha256'), buffer = Buffer.alloc(65536); let total = 0, count: number;
    while ((count = readSync(fd, buffer, 0, Math.min(buffer.length, cap - total + 1), total)) !== 0) {
      total += count; check(total <= cap, 'file grew beyond bound'); digest.update(buffer.subarray(0, count));
    }
    const after = fstatSync(fd);
    check(total === before.size && after.size === before.size && after.dev === before.dev && after.ino === before.ino &&
      after.mtimeMs === before.mtimeMs && after.ctimeMs === before.ctimeMs, 'held hashed file changed');
    return digest.digest('hex');
  } finally { closeSync(fd); }
}
function boundedBytes(path: string, cap: number): Buffer {
  physical(path); const fd = openSync(path, 'r');
  try {
    check(fstatSync(fd).size <= cap, 'text bound'); const bytes = Buffer.alloc(cap + 1); let length = 0, count: number;
    while (length < bytes.length && (count = readSync(fd, bytes, length, bytes.length - length, length)) !== 0) length += count;
    check(length <= cap, 'text grew beyond bound'); return bytes.subarray(0, length);
  } finally { closeSync(fd); }
}
const boundedText = (path: string, cap: number): string => boundedBytes(path, cap).toString('utf8');
const remaining = (): number => 145000 - (performance.now() - started);
function obj(v: unknown): Record<string, unknown> {
  check(v !== null && typeof v === 'object' && !Array.isArray(v), 'object required'); return v as Record<string, unknown>;
}
function keys(v: Record<string, unknown>, expected: string[]): void {
  check(Object.keys(v).sort().join() === expected.sort().join(), 'exact schema required');
}
function token(v: unknown): Record<string, unknown> {
  const f = obj(v); keys(f, ['sidSHA256', 'authLUIDSHA256', 'session', 'elevated', 'elevationType', 'integrityRID']);
  check(typeof f.sidSHA256 === 'string' && /^[a-f0-9]{64}$/.test(f.sidSHA256) && typeof f.authLUIDSHA256 === 'string' &&
    /^[a-f0-9]{64}$/.test(f.authLUIDSHA256) && typeof f.elevated === 'boolean' &&
    [f.session, f.integrityRID, f.elevationType].every(n => typeof n === 'number' && Number.isInteger(n) && n >= 0 && n <= 0xffffffff) &&
    (f.elevationType === 1 || f.elevationType === 2 || f.elevationType === 3), 'token facts incomplete');
  check(!(f.elevationType === 2 && !f.elevated) && !(f.elevationType === 3 && f.elevated), 'token elevation/type inconsistent');
  return f;
}
function same(a: Record<string, unknown>, b: Record<string, unknown>): void {
  for (const k of Object.keys(a)) check(a[k] === b[k], `observed facts differ: ${k}`);
}
function copy(source: string, destination: string): string {
  const pin = hash(source); physical(destination); check(lstatSync(destination).size === 0, 'native-owned empty leaf required');
  const bytes = boundedBytes(source, 64 << 20);
  writeFileSync(destination, bytes, { flag: 'r+' }); // Preserve explicit native owner/DACL; never clone source security.
  check(hash(destination) === pin, 'copied bytes changed'); return pin;
}
function durable(path: string, data: unknown): void {
  const body = JSON.stringify(data, null, 2); check(Buffer.byteLength(body) <= 262144, 'bounded evidence');
  const fd = openSync(path, 'wx'); try { writeFileSync(fd, body); fsyncSync(fd); } finally { closeSync(fd); }
}
async function custody(operation: 'create' | 'seal', stage: 'deploy' | 'bootstrap', launcher: string, nonce: string,
  dir: string, record: Record<string, unknown>, count: number): Promise<void> {
  check(remaining() >= 15000, 'global deadline insufficient for custody');
  let stdout = '', stderr = '', overflow = false, collected = false;
  const args = operation === 'create' ? ['--TEST-create-private-sdk-root', nonce, dir, stage] : ['--TEST-seal-sdk-root', nonce, dir];
  const child = spawn(launcher, args, { cwd: operation === 'create' ? process.env.RUNNER_TEMP : dir,
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  const capture = (data: Buffer, err: boolean): void => {
    if (Buffer.byteLength(stdout) + Buffer.byteLength(stderr) + data.length > 8192) { overflow = true; child.kill(); return; }
    if (err) stderr += data.toString('utf8'); else stdout += data.toString('utf8');
  };
  child.stdout.on('data', (data: Buffer) => capture(data, false)); child.stderr.on('data', (data: Buffer) => capture(data, true));
  let spawnError: string | undefined;
  const result = await new Promise<number | null>(done => {
    const kill = setTimeout(() => child.kill(), 10000);
    const deadline = setTimeout(() => { child.kill(); child.stdout.destroy(); child.stderr.destroy(); child.unref(); clearTimeout(kill); done(null); }, 15000);
    child.once('error', e => { spawnError = e.message.slice(0, 512); });
    child.once('close', code => { collected = true; clearTimeout(kill); clearTimeout(deadline); done(code); });
  });
  record[operation] = { pid: child.pid, stdout, stderr, result, collected, overflow, spawnError };
  check(collected && !overflow && !spawnError && result === 0 && stderr === '' && stdout.trim().split(/\r?\n/).length === 1, 'root seal collection unknown');
  const proof = obj(JSON.parse(stdout)); keys(proof, ['nonce', 'pid', operation === 'create' ? 'created' : 'sealed', 'protectedDACL', 'files']);
  check(proof.nonce === nonce && proof.pid === child.pid && proof[operation === 'create' ? 'created' : 'sealed'] === true && proof.protectedDACL === true && proof.files === count,
    'private protected root/file ACL readback absent');
}
async function actor(stage: 'deploy' | 'bootstrap', inputs: string, bootstrap: string,
  bootstrapPin: string, payloadPin: string): Promise<Record<string, unknown>> {
  const nonce = randomUUID(), dir = join(resolve(process.env.RUNNER_TEMP!), `TEST-lua-preflight-${nonce}`);
  const record: Record<string, unknown> = { stage, nonce, root: dir, outcome: 'unknown' };
  (evidence.actors as unknown[]).push(record);
  const launcher = join(dir, `TEST-launcher-${nonce}.exe`), exe = join(dir, `TEST-sdk-${nonce}.exe`);
  const pins = new Map<string, string>();
  check(hash(process.env.LUA_LAUNCHER_EXE!) === process.env.LUA_LAUNCHER_SHA256 &&
    hash(process.env.SDK_EXE!) === process.env.SDK_EXE_SHA256, 'frozen source executable pins');
  physical(resolve(process.env.RUNNER_TEMP!), true);
  await custody('create', stage, process.env.LUA_LAUNCHER_EXE!, nonce, dir, record, stage === 'deploy' ? 12 : 4);
  pins.set(launcher, copy(process.env.LUA_LAUNCHER_EXE!, launcher));
  pins.set(exe, copy(process.env.SDK_EXE!, exe));
  check(pins.get(launcher) === process.env.LUA_LAUNCHER_SHA256 && pins.get(exe) === process.env.SDK_EXE_SHA256, 'copied compiled pins');
  pins.set(join(dir, 'Microsoft.WindowsAppRuntime.Bootstrap.dll'), copy(bootstrap, join(dir, 'Microsoft.WindowsAppRuntime.Bootstrap.dll')));
  check(pins.get(join(dir, 'Microsoft.WindowsAppRuntime.Bootstrap.dll')) === bootstrapPin, 'bootstrap custody lost');
  const modulePins = join(dir, 'TEST-module-pins.txt'); physical(modulePins); check(lstatSync(modulePins).size === 0, 'empty owned pins leaf required');
  writeFileSync(modulePins, `${bootstrapPin}\n${payloadPin}\n`, { flag: 'r+' });
  pins.set(join(dir, 'TEST-module-pins.txt'), hash(join(dir, 'TEST-module-pins.txt')));
  if (stage === 'deploy') for (const p of catalog) for (const leaf of [p.file, `${p.file}.manifest.xml`]) {
    const destination = join(dir, leaf); pins.set(destination, copy(join(inputs, leaf), destination));
  }
  physical(dir, true);
  check(readdirSync(dir).sort().join() === [...pins.keys()].map(path => basename(path)).sort().join(), 'closed actor root required (no sidecars/.local)');
  record.pins = Object.fromEntries(pins);
  const identities = new Map([...pins.keys()].map(path => { const st = lstatSync(path); return [path, { dev: st.dev, ino: st.ino, size: st.size }] as const; }));
  await custody('seal', stage, launcher, nonce, dir, record, pins.size);
  for (const [path, pin] of pins) {
    const before = identities.get(path)!, after = lstatSync(path);
    check(after.dev === before.dev && after.ino === before.ino && after.size === before.size && hash(path) === pin, 'custody changed during seal');
  }
  check(remaining() >= (stage === 'deploy' ? 100000 : 40000), 'insufficient global budget; next SDK effect stage refused');
  let stdout = '', stderr = '', outputBytes = 0, overflow = false, timedOut = false, collected = false;
  let spawnError: string | undefined;
  // Exactly one launch per fixed stage; no retry and no arbitrary command/executable input.
  const child = spawn(launcher, [stage === 'deploy' ? '--TEST-sdk-deployment' : '--TEST-sdk-bootstrap', nonce, dir, exe],
    { cwd: dir, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  record.pid = child.pid;
  const capture = (data: Buffer, err: boolean): void => {
    const text = data.subarray(0, Math.max(0, 16384 - outputBytes)).toString('utf8');
    if (err) stderr += text; else stdout += text; outputBytes += data.length;
    if (outputBytes > 16384 && !overflow) { overflow = true; child.kill(); }
  };
  child.stdout.on('data', (data: Buffer) => capture(data, false)); child.stderr.on('data', (data: Buffer) => capture(data, true));
  const result = await new Promise<{ code: number | null; signal: string | null }>(done => {
    const kill = setTimeout(() => { timedOut = true; child.kill(); }, stage === 'deploy' ? 95000 : 35000);
    const deadline = setTimeout(() => { timedOut = true; child.kill(); child.stdout.destroy(); child.stderr.destroy(); child.unref();
      clearTimeout(kill); done({ code: null, signal: 'collection_deadline' }); }, stage === 'deploy' ? 100000 : 40000);
    child.once('error', e => { spawnError = e.message.slice(0, 512); });
    child.once('close', (code, signal) => { collected = true; clearTimeout(kill); clearTimeout(deadline); done({ code, signal }); });
  });
  Object.assign(record, { stdout, stderr, result, collected, timedOut, overflow, spawnError });
  // Preserve every own durable phase, including intent-without-completion after actor/broker uncertainty.
  const phases: Record<string, unknown>[] = [];
  record.durablePhases = phases;
  for (const leaf of readdirSync(dir).filter(n => /^TEST-deploy-[0-3]-(intent|complete)\.json$/.test(n)).sort()) {
    const path = join(dir, leaf); physical(path); check(lstatSync(path).size <= 8192, 'phase bound');
    const raw = boundedText(path, 8192);
    try { phases.push({ file: leaf, data: JSON.parse(raw) as unknown }); }
    catch { phases.push({ file: leaf, raw, parseError: true }); }
  }
  for (const [path, pin] of pins) check(hash(path) === pin, 'post-run custody failed');
  check(hash(process.env.LUA_LAUNCHER_EXE!) === process.env.LUA_LAUNCHER_SHA256 &&
    hash(process.env.SDK_EXE!) === process.env.SDK_EXE_SHA256 && hash(bootstrap) === bootstrapPin, 'post-run source pins');
  check(collected && !timedOut && !overflow && !spawnError && stderr === '' && stdout.trim().split(/\r?\n/).length === 1,
    'actor collection unknown; stop without retry');
  const r = obj(JSON.parse(stdout)); record.launcher = r;
  keys(r, ['nonce', 'pid', 'variant', 'restrictedBefore', 'restrictedAfter', 'parentAfter', 'loweringAttempted', 'integrityLowered',
    'baseline', 'held', 'childPid', 'childBirth', 'enabledAdmins', 'sameIdentitySession', 'collected', 'timedOut', 'childExit',
    'childTerminated', 'cleanupError', 'childRecord', 'childStderr', 'queriesComplete', 'query', 'error', 'sdkNativeCallback', 'notificationEffects']);
  check(r.pid === child.pid && r.nonce === nonce && r.variant === `sdk-${stage}` && r.queriesComplete === true &&
    r.collected === true && r.timedOut === false && r.childTerminated === false && r.cleanupError === null && r.childExit === 0 &&
    result.code === 0 && result.signal === null && typeof r.childBirth === 'string' && /^[1-9][0-9]{1,19}$/.test(r.childBirth) &&
    typeof r.childPid === 'number' && Number.isInteger(r.childPid) && r.childPid > 0 && r.childPid !== r.pid && typeof r.childRecord === 'string' &&
    r.childRecord.trim().split(/\r?\n/).length === 1 && r.childStderr === '' && r.sdkNativeCallback === false && r.notificationEffects === 0,
    'launcher/query incomplete; stop without retry');
  check(r.sameIdentitySession === true && typeof r.enabledAdmins === 'boolean' && r.query === null && r.error === null,
    'launcher observation incomplete');
  const c = obj(JSON.parse(r.childRecord)); record.child = c;
  keys(c, ['nonce', 'pid', 'stage', 'before', 'after', 'bootstrapModule', 'runtimeModule', 'selectedFramework', 'phases',
    'bootstrapCalled', 'bootstrapHRESULT', 'bootstrapShutdown', 'isSupported', 'queriesComplete', 'query', 'error', 'sdkNativeCallback', 'notificationEffects']);
  check(c.nonce === nonce && c.pid === r.childPid && c.stage === stage && c.queriesComplete === true && c.query === null &&
    c.error === null && c.sdkNativeCallback === false && c.notificationEffects === 0, 'actor record incomplete');
  const baseline = token(r.baseline), held = token(r.held), before = token(c.before), after = token(c.after);
  same(held, before); same(before, after);
  for (const k of ['sidSHA256', 'authLUIDSHA256', 'session']) check(baseline[k] === before[k], 'actor identity/session drift');
  const bootstrapModule = obj(c.bootstrapModule);
  check(bootstrapModule.path === join(dir, 'Microsoft.WindowsAppRuntime.Bootstrap.dll') && bootstrapModule.sha256 === bootstrapPin &&
    typeof bootstrapModule.fileVersion === 'string' && /^\d+\.\d+\.\d+\.\d+$/.test(bootstrapModule.fileVersion), 'bootstrap module provenance');
  if (stage === 'deploy') {
    check(r.restrictedBefore === null && r.restrictedAfter === null && r.parentAfter === null &&
      r.loweringAttempted === false && r.integrityLowered === false, 'deployment must preserve own primary without restriction/lowering');
    same(baseline, before);
    check(c.bootstrapCalled === false && c.bootstrapShutdown === false && c.runtimeModule === null && c.isSupported === null &&
      Array.isArray(c.phases) && c.phases.length === 4 && phases.length === 8, 'deployment phase evidence absent');
    for (let i = 0; i < catalog.length; i++) {
      const p = catalog[i]!, full = `${p.name}_${p.version}_arm64__8wekyb3d8bbwe`, phase = obj(c.phases[i]);
      check(phase.package === full && phase.readbackExact === true && typeof phase.skippedExact === 'boolean', 'package readback mismatch');
      for (const suffix of ['intent', 'complete']) {
        const item = phases.find(p => p.file === `TEST-deploy-${i}-${suffix}.json`); check(item, 'durable phase missing');
        const data = obj(item.data); check(data.nonce === nonce && data.pid === c.pid && data.package === full &&
          data.sha256 === p.sha256, 'durable phase identity mismatch'); same(token(data.token), before);
        check(data.operation === (phase.skippedExact ? 'skip_exact' : 'AddPackageAsync_None'), 'durable operation mismatch');
        if (suffix === 'complete') {
          check(data.readbackExact === true && data.completionObserved === true &&
            (phase.skippedExact ? data.asyncStatus === null && data.extendedHRESULT === null : data.asyncStatus === 1 && data.extendedHRESULT === 0),
            'actual Completed/S_OK deployment result absent'); same(token(data.tokenAfter), before);
        }
      }
    }
  } else {
    same(token(r.parentAfter), baseline); same(token(r.restrictedAfter), held);
    check(before.elevated === false && before.integrityRID === 0x2000 && r.enabledAdmins === false &&
      c.bootstrapCalled === true && c.bootstrapHRESULT === 0 && c.bootstrapShutdown === true && typeof c.isSupported === 'boolean' &&
      c.selectedFramework === `${catalog[0].name}_2.5.1.0_arm64__8wekyb3d8bbwe`, 'bootstrap prerequisite incomplete');
    const initial = token(r.restrictedBefore);
    check((initial.integrityRID as number) >= 0x2000 && r.loweringAttempted === (initial.integrityRID !== 0x2000) &&
      r.integrityLowered === r.loweringAttempted, 'actual restricted lowering invariant');
    const loaded = obj(c.runtimeModule);
    check(loaded.sha256 === payloadPin && typeof loaded.path === 'string' &&
      loaded.path.endsWith('\\Microsoft.WindowsAppRuntime.dll') && typeof loaded.fileVersion === 'string', 'runtime module payload provenance');
  }
  record.outcome = stage === 'deploy' ? 'exact_current_user_readback' : c.isSupported ? 'sdk_static_supported' : 'sdk_static_unsupported';
  return baseline;
}
async function main(): Promise<void> {
  try {
    check(process.platform === 'win32' && process.arch === 'arm64' && process.env.GITHUB_REPOSITORY === '777genius/agent-notifications' &&
      process.env.GITHUB_RUN_ATTEMPT === '1' && /^[a-f0-9]{40}$/.test(process.env.SOURCE_SHA ?? ''), 'trusted own CI only');
    const inputs = resolve(process.env.SDK_INPUTS ?? ''), bootstrap = resolve(process.env.BOOTSTRAP_DLL ?? ''), payload = join(inputs, 'Microsoft.WindowsAppRuntime.dll');
    physical(inputs, true);
    const provenance = obj(JSON.parse(boundedText(join(inputs, 'provenance.json'), 8192)));
    check(provenance.runtimeVerified === true && provenance.outerSHA256 === outerSHA256 && provenance.foundationVersion === '2.3.12' &&
      provenance.foundationVerified === true && provenance.foundationContentHash === 'UflrNbXoZmHPt8lLGDk4gQ+yoGfQlCHd3TVTy7YwCMZ/aVGWAiLDqGCEjtWmNsI8ySy6imx3ZlenKfd2/VMPEg==',
      'same verified archive/source required');
    check(provenance.runtimeModuleSourceMSIXSHA256 === catalog[0].sha256 && provenance.runtimeModuleSourceEntry === 'Microsoft.WindowsAppRuntime.dll' &&
      provenance.bootstrapSourceEntry === 'runtimes/win-arm64/native/Microsoft.WindowsAppRuntime.Bootstrap.dll' &&
      typeof provenance.foundationArchiveSHA256 === 'string' && /^[a-f0-9]{64}$/.test(provenance.foundationArchiveSHA256), 'exact payload source pins required');
    evidence.provenance = provenance;
    const bootstrapPin = hash(bootstrap), payloadPin = hash(payload);
    check(bootstrapPin === provenance.bootstrapSHA256 && payloadPin === provenance.runtimeModuleSHA256, 'payload pin provenance mismatch');
    for (const p of catalog) check(hash(join(inputs, p.file)) === p.sha256 && hash(join(inputs, `${p.file}.manifest.xml`)) === p.manifestSHA256,
      'raw MSIX/manifest frozen identity mismatch');
    check(process.env.SDK_EXE && process.env.SDK_EXE_SHA256 && hash(process.env.SDK_EXE) === process.env.SDK_EXE_SHA256 &&
      process.env.LUA_LAUNCHER_EXE && process.env.LUA_LAUNCHER_SHA256 && hash(process.env.LUA_LAUNCHER_EXE) === process.env.LUA_LAUNCHER_SHA256, 'compiled binary pins required');
    const deploymentBaseline = await actor('deploy', inputs, bootstrap, bootstrapPin, payloadPin);
    const bootstrapBaseline = await actor('bootstrap', inputs, bootstrap, bootstrapPin, payloadPin);
    same(deploymentBaseline, bootstrapBaseline); check(performance.now() - started < 145000, 'overall experiment deadline');
    evidence.outcome = 'runtime_prerequisite_measured';
  } catch (e) { evidence.failure = String(e).slice(0, 1024); process.exitCode = 1; }
  finally {
    evidence.durationMs = performance.now() - started; durable(join(root, 'sdk-runtime-evidence.json'), evidence);
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${root}\n`, { flag: 'a' });
  }
}
void main().catch(e => { console.error(String(e).slice(0, 1024)); process.exitCode = 1; });
