// Exactly one disposable SDK cold click. A receiver receipt never proves process collection.
import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { closeSync, existsSync, fstatSync, fsyncSync, lstatSync, mkdtempSync, openSync, readSync,
  readdirSync, realpathSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
type Json = Record<string, unknown>;
type Actor = { mode: string; pid?: number; code: number | null; signal: string | null; collected: boolean;
  timedOut: boolean; overflow: boolean; stdout: string; stderr: string; error?: string; record?: Json };
if (process.platform !== 'win32' || process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications' || process.env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || process.env.GITHUB_RUN_ATTEMPT !== '1' || !process.env.RUNNER_TEMP) throw new Error('explicit own disposable CI only');
const scenario = process.env.SDK_COLDCLICK_SCENARIO ?? 'shell_foreground';
if (!['shell_foreground', 'owned_test_foreground', 'disposable_global_shortcut'].includes(scenario)) throw new Error('fixed foreground scenario required');
if (scenario === 'disposable_global_shortcut' && (process.env.GITHUB_ACTIONS !== 'true' || process.env.RUNNER_OS !== 'Windows' || process.env.SDK_COLDCLICK_DISPOSABLE_CLIENT !== 'windows-11-vs2026-arm')) throw new Error('fixed disposable Windows client dispatch authority required');
const historyEnabled = process.env.SDK_COLDCLICK_HISTORY === 'true';
if (process.env.SDK_COLDCLICK_HISTORY && !['true', 'false'].includes(process.env.SDK_COLDCLICK_HISTORY)) throw new Error('explicit fixed history diagnostic choice');
const start = performance.now(), nonce = randomUUID();
const output = mkdtempSync(join(process.env.RUNNER_TEMP ?? '', 'TEST-sdk-coldclick-evidence-'));
const evidence: Json = { nonce, scenario, historyEnabled, sourceSHA: process.env.SOURCE_SHA, outcome: 'unknown', actors: [], receipts: {},
  sdkColdActivationQualified: false, targetVisibleQualified: false, registrationCleanupQualified: false,
  globalBrokerQuiescenceQualified: false, automaticRetry: false };
const actors = evidence.actors as Actor[];
function check(value: unknown, why: string): asserts value { if (!value) throw new Error(why); }
function object(value: unknown): Json { check(value !== null && typeof value === 'object' && !Array.isArray(value), 'object required'); return value as Json; }
function exact(value: Json, expected: string[]): void { check(Object.keys(value).sort().join() === expected.sort().join(), 'exact schema'); }
function physical(path: string, directory = false): void {
  const s = lstatSync(path); check(!s.isSymbolicLink() && (directory ? s.isDirectory() : s.isFile() && s.nlink === 1) &&
    realpathSync(path).toLowerCase() === path.toLowerCase(), 'physical own file required');
}
function bytes(path: string, cap: number): Buffer {
  physical(path); const fd = openSync(path, 'r');
  try {
    const before = fstatSync(fd); check(before.size <= cap, 'file cap'); const data = Buffer.alloc(cap + 1); let n = 0, got: number;
    while (n < data.length && (got = readSync(fd, data, n, data.length - n, n)) > 0) n += got;
    const after = fstatSync(fd); check(n <= cap && n === before.size && after.size === n && before.ino === after.ino && before.dev === after.dev &&
      before.mtimeMs === after.mtimeMs && before.ctimeMs === after.ctimeMs, 'held file changed/bounded read'); return data.subarray(0, n);
  } finally { closeSync(fd); }
}
function hash(path: string): string {
  physical(path); const fd = openSync(path, 'r');
  try {
    const before = fstatSync(fd); check(before.size <= 64 << 20, 'pin file cap'); const h = createHash('sha256'), b = Buffer.alloc(65536); let n = 0, got: number;
    while ((got = readSync(fd, b, 0, b.length, n)) > 0) { n += got; check(n <= 64 << 20, 'pin grew'); h.update(b.subarray(0, got)); }
    const after = fstatSync(fd); check(n === before.size && after.size === n && after.ino === before.ino && after.dev === before.dev &&
      after.mtimeMs === before.mtimeMs && after.ctimeMs === before.ctimeMs, 'held pin changed'); return h.digest('hex');
  } finally { closeSync(fd); }
}
function json(path: string, cap = 8192): Json { return object(JSON.parse(bytes(path, cap).toString('utf8'))); }
function durable(path: string, value: unknown): void {
  const body = JSON.stringify(value); check(Buffer.byteLength(body) <= 262144, 'evidence cap'); const fd = openSync(path, 'wx');
  try { writeFileSync(fd, body); fsyncSync(fd); } finally { closeSync(fd); }
}
function token(value: unknown, admin = false): Json {
  const t = object(value); exact(t, ['sidSHA256', 'authLUIDSHA256', 'session', 'elevated', 'elevationType', 'integrityRID', ...(admin ? ['enabledAdmins'] : [])]);
  check(['sidSHA256', 'authLUIDSHA256'].every(k => typeof t[k] === 'string' && /^[a-f0-9]{64}$/.test(t[k] as string)) &&
    typeof t.elevated === 'boolean' && [1, 2, 3].includes(t.elevationType as number) &&
    ['session', 'integrityRID'].every(k => Number.isInteger(t[k]) && (t[k] as number) >= 0 && (t[k] as number) <= 0xffffffff) &&
    (!admin || typeof t.enabledAdmins === 'boolean') && !(t.elevationType === 2 && t.elevated === false) && !(t.elevationType === 3 && t.elevated === true), 'actual token schema'); return t;
}
function sameFacts(a: Json, b: Json): void { check(Object.keys(a).length === Object.keys(b).length && Object.keys(a).every(k => a[k] === b[k]), 'all actual token facts unchanged'); }
function sameIdentity(a: Json, b: Json): void { for (const k of ['sidSHA256', 'authLUIDSHA256', 'session']) check(a[k] === b[k], 'same current-user identity/session'); }
function medium(t: Json): void { check(t.elevated === false && t.integrityRID === 0x2000 && t.enabledAdmins === false, 'actual Medium/nonadmin required'); }
const phaseFields: Record<string, string[]> = {
  sender: ['token', 'bootstrapModule', 'bootstrapCallBootMs', 'bootstrapHRESULT', 'selectedFramework', 'isSupported', 'runtimeModule', 'handlerBeforeRegister', 'registered', 'nativeID', 'showReturned', 'unregisterReturned', 'bootstrapShutdown', 'outcome', 'error', 'query', 'showCallBootMs', 'showReturnedBootMs', 'unregisterCallBootMs', 'unregisterReturnedBootMs', 'registryIdentity', 'payloadSHA256'],
  history: ['token', 'physicalImage', 'exeSHA256', 'bootstrapModule', 'bootstrapCallBootMs', 'bootstrapHRESULT', 'selectedFramework', 'isSupported', 'runtimeModule', 'senderID', 'expectedPayloadSHA256', 'registryBefore', 'registryAfter', 'registryReadbackError', 'queryCallBootMs', 'queryReturnedBootMs', 'asyncStatus', 'count', 'matchingIDCount', 'exactPayloadMatch', 'observedPayloadSHA256', 'bootstrapShutdown', 'cancelRequested', 'rpcCompletionQualified', 'outcome', 'query', 'error'],
  receiver_ready: ['token', 'bindingSHA256', 'leaseDeadlineBootMs'],
  receiver_terminal: ['token', 'bootstrapModule', 'bootstrapCallBootMs', 'bootstrapHRESULT', 'selectedFramework', 'isSupported', 'runtimeModule', 'handlerBeforeRegister', 'activationKind', 'bootstrapShutdown', 'unregisterReturned', 'outcome', 'registrationStillOwned', 'error', 'query'],
  collector: ['readyWaitDeadlineBootMs', 'readyObserved', 'readyObservedBootMs', 'ackPublishedBootMs', 'stage', 'ready', 'heldToken', 'heldPhysicalImage', 'ackPublished', 'waitResult', 'collected', 'exitCode', 'outcome', 'query', 'error'],
  shell_invoke: ['scenario', 'centerAttempted', 'inputEffectUnknown', 'stage', 'censusAttempts', 'completedCensusAttempts', 'maxRoots', 'maxNodes', 'maxAdmittedProviderRoots', 'maxOwnedTitleMatches', 'firstCompletedCensusBootMs', 'lastCompletedCensusBootMs', 'invokeCallBootMs', 'invokeReturnedBootMs', 'providerPID', 'providerBirth', 'providerImage', 'exactOwnedTitle', 'invokeHRESULT', 'invokeReturned', 'query', 'error', 'outcome'],
  center_input: ['scenario', 'deadlineBootMs', 'endBootMs', 'failureBootMs', 'firstCompletedCensusBootMs', 'completedCensusAttempts', 'inputEffectUnknown', 'sender', 'foregroundPID', 'foregroundBirth', 'globalSourceSHA', 'windowsClient', 'initialKeysReleased', 'disposableAuthority', 'intentBootMs', 'inputCallBootMs', 'inputReturnedBootMs', 'outcome', 'query', 'error', 'acceptedEvents', 'cleanupReleaseAttempts', 'releaseUnknown', 'ownedKeysReleased', 'sendError', 'centerOpenedProved'],
  owned_foreground: ['scenario', 'creatorTID', 'className', 'physicalImage', 'exeSHA256', 'token', 'deadlineBootMs', 'createIntentBootMs', 'windowCreated', 'createdBootMs', 'startupFlags', 'startupShowWindow', 'visible', 'foregroundAttempts', 'foregroundCallBootMs', 'foregroundReturnedBootMs', 'setForegroundReturned', 'actualForeground', 'destroyCallBootMs', 'destroyed', 'windowCustodyKnown', 'ownerLifetimeRetained', 'endBootMs'],
  callback: ['eventKind', 'token', 'argument', 'entryBootMs', 'deadlineBootMs', 'sender', 'uri', 'familyName', 'uriSupport', 'launchReturned', 'accepted', 'outcome', 'error', 'query', 'launchEntered', 'targetVisibleQualified', 'timely', 'queryAdmissionBootMs', 'queryDeadlineBootMs', 'queryCallBootMs', 'queryReturnedBootMs', 'queryEntered', 'queryReturned', 'launchAdmissionBootMs', 'launchDeadlineBootMs', 'launchCallBootMs', 'launchReturnedBootMs'],
};
phaseFields.query_intent = [...phaseFields.callback!, 'queryBoundaryArmed'];
phaseFields.launch_intent = [...phaseFields.callback!, 'launchBoundaryArmed'];
function correlation(r: Json, phase: string): void {
  const allowed = ['nonce', 'phase', 'pid', 'birth', 'bootMs', ...(['sender', 'history', 'collector', 'shell_invoke'].includes(phase) ? ['deadlineBootMs', 'endBootMs', 'failureBootMs'] : []), ...phaseFields[phase]!];
  check(Object.keys(r).every(k => allowed.includes(k)), 'closed native phase schema');
  check(r.nonce === nonce && r.phase === phase && Number.isInteger(r.pid) && (r.pid as number) > 0 &&
    typeof r.birth === 'string' && /^[1-9][0-9]{1,19}$/.test(r.birth) && typeof r.bootMs === 'number' && Number.isSafeInteger(r.bootMs), 'actor incarnation schema');
}
function diagnostic(r: Json, phase: 'sender' | 'history' | 'collector' | 'shell_invoke'): void {
  correlation(r, phase);
  const clock = (key: string): number => { const value = r[key]; check(Number.isSafeInteger(value) && (value as number) >= (r.bootMs as number), `finite diagnostic ${key}`); return value as number; };
  const end = clock('endBootMs'), deadline = clock('deadlineBootMs'); check(end >= (r.bootMs as number) && deadline > (r.bootMs as number), 'original actor diagnostic clocks');
  if (r.outcome === 'unknown') check('failureBootMs' in r, 'actual negative failure clock required');
  if ('failureBootMs' in r) check(clock('failureBootMs') <= end, 'failure observed before terminal publication');
  const ordered = (a: string, b: string): void => { check(clock(a) <= clock(b) && clock(b) <= end, 'observed phase return clocks'); };
  if (phase === 'sender' && r.showReturned === true) { ordered('showCallBootMs', 'showReturnedBootMs'); if (r.unregisterReturned === true) { ordered('unregisterCallBootMs', 'unregisterReturnedBootMs'); check(clock('showReturnedBootMs') <= clock('unregisterCallBootMs'), 'Show before Unregister'); } }
  if (phase === 'collector') {
    check(['ready_wait', 'ready_validate', 'receiver_collect'].includes(r.stage as string) && typeof r.readyObserved === 'boolean' && typeof r.ackPublished === 'boolean' && clock('readyWaitDeadlineBootMs') === (historyEnabled ? object((evidence.receipts as Json)['TEST-history-request.json']).deadlineBootMs : deadline - 70000), 'fixed collector ready admission');
    if (r.readyObserved === true) check(clock('readyObservedBootMs') <= end, 'ready observation retained'); else check(r.ackPublished === false && !('readyObservedBootMs' in r), 'no ack without actual ready');
    if (r.query === 'AbsoluteDeadline' && r.stage === 'ready_wait') check(r.readyObserved === false && clock('failureBootMs') >= clock('readyWaitDeadlineBootMs'), 'actual missing-ready admission deadline');
    if (r.ackPublished === true) { ordered('readyObservedBootMs', 'ackPublishedBootMs'); check(r.stage === 'receiver_collect', 'ack after ready validation'); }
  }
  if (phase === 'shell_invoke') {
    check(['interactive', 'census', 'center_input', 'invoke_admission', 'invoke'].includes(r.stage as string), 'closed finder phase');
    for (const key of ['censusAttempts', 'completedCensusAttempts', 'maxRoots', 'maxNodes', 'maxAdmittedProviderRoots', 'maxOwnedTitleMatches']) check(Number.isInteger(r[key]) && (r[key] as number) >= 0 && (r[key] as number) <= 0xffffffff, 'bounded aggregate census count');
    check((r.completedCensusAttempts as number) <= (r.censusAttempts as number) && (r.maxRoots as number) <= 64 && (r.maxNodes as number) <= 512 && (r.maxAdmittedProviderRoots as number) <= (r.maxRoots as number) && (r.maxOwnedTitleMatches as number) <= 2, 'observed finder bounds');
    if ((r.completedCensusAttempts as number) > 0) ordered('firstCompletedCensusBootMs', 'lastCompletedCensusBootMs');
    if (r.query === 'OwnedToastUnavailable') check((r.completedCensusAttempts as number) > 0 && r.maxOwnedTitleMatches === 0 && clock('failureBootMs') >= deadline && !('invokeCallBootMs' in r), 'owned finder unavailable after measured census deadline without Invoke');
    if (r.invokeReturned === true) ordered('invokeCallBootMs', 'invokeReturnedBootMs');
  }
}
const minimalEnv = (root: string): NodeJS.ProcessEnv => ({ SystemRoot: process.env.SystemRoot, TEMP: root, TMP: root });
async function run(mode: string, exe: string, args: string[], cwd: string, limit: number, nativeCustody = false): Promise<Actor> {
  check(performance.now() - start + limit + 5000 < 170000, 'global deadline insufficient; actor refused');
  const result: Actor = { mode, code: null, signal: null, collected: false, timedOut: false, overflow: false, stdout: '', stderr: '' }; actors.push(result);
  const child = spawn(exe, args, { cwd, windowsHide: mode !== 'owned-test-foreground-invoke', env: nativeCustody ? { ...minimalEnv(cwd), RUNNER_TEMP: process.env.RUNNER_TEMP } : { ...minimalEnv(cwd), ...(['owned-shell-invoke', 'owned-test-foreground-invoke', 'disposable-global-shortcut-invoke'].includes(mode) ? { SDK_COLDCLICK_DISPOSABLE_TEST: nonce, ...(mode === 'disposable-global-shortcut-invoke' ? { SDK_COLDCLICK_GLOBAL_AUTHORITY: `dispatch-attempt1:${process.env.SOURCE_SHA}:${nonce}`, SDK_COLDCLICK_DISPOSABLE_CLIENT: process.env.SDK_COLDCLICK_DISPOSABLE_CLIENT } : {}) } : {}) }, stdio: ['ignore', 'pipe', 'pipe'] });
  result.pid = child.pid; let total = 0;
  const capture = (b: Buffer, err: boolean): void => { const saved = b.subarray(0, Math.max(0, 16384 - total)).toString('utf8'); total += b.length;
    if (err) result.stderr += saved; else result.stdout += saved; if (total > 16384) { result.overflow = true; child.kill(); } };
  child.stdout.on('data', (b: Buffer) => capture(b, false)); child.stderr.on('data', (b: Buffer) => capture(b, true));
  await new Promise<void>(done => {
    const kill = setTimeout(() => { result.timedOut = true; child.kill(); }, limit);
    const end = setTimeout(() => { result.timedOut = true; child.kill(); child.stdout.destroy(); child.stderr.destroy(); child.unref(); clearTimeout(kill); done(); }, limit + 3000);
    child.once('error', e => { result.error = e.message.slice(0, 512); });
    child.once('close', (code, signal) => { Object.assign(result, { collected: true, code, signal }); clearTimeout(kill); clearTimeout(end); done(); });
  });
  if (result.stdout.trim().split(/\r?\n/).length === 1 && result.stdout.trim()) { try { result.record = object(JSON.parse(result.stdout)); } catch { /* Raw failure retained. */ } }
  return result;
}
function completed(r: Actor): void { check(r.collected && !r.timedOut && !r.overflow && !r.error && r.code === 0 && r.signal === null && r.stderr === '', `actor unknown: ${r.mode}`); }
const receipts = ['TEST-history-request.json', 'TEST-history-intent.json', 'TEST-history.json', 'TEST-register-intent.json', 'TEST-show-intent.json', 'TEST-sender.json', 'TEST-sender-collected.json',
  'TEST-owned-foreground-intent.json', 'TEST-owned-foreground.json', 'TEST-center-input-intent.json', 'TEST-center-input.json', 'TEST-ui-invoke-intent.json', 'TEST-ui-invoke.json', 'TEST-receiver-ready.json', 'TEST-receiver-ack.json', 'TEST-receiver-lease-intent.json',
  'TEST-second-receiver.json', 'TEST-second-callback.json', 'TEST-query-intent.json', 'TEST-launch-intent.json', 'TEST-callback.json', 'TEST-receiver-terminal.json', 'TEST-collector.json'];
let generation = '';
function retain(): void {
  const found = evidence.receipts as Json;
  if (!generation) return;
  for (const leaf of receipts) if (existsSync(join(generation, leaf))) {
    const raw = bytes(join(generation, leaf), 8192).toString('utf8');
    try { found[leaf] = object(JSON.parse(raw)); } catch { found[leaf] = { raw, parseError: true }; }
  }
}
async function main(): Promise<void> {
  try {
    check(process.platform === 'win32' && process.arch === 'arm64' && process.env.GITHUB_REPOSITORY === '777genius/agent-notifications' &&
      process.env.GITHUB_RUN_ATTEMPT === '1' && /^[a-f0-9]{40}$/.test(process.env.SOURCE_SHA ?? ''), 'one trusted disposable CI dispatch');
    const prerequisite = json(join(process.env.SDK_PREREQUISITE_EVIDENCE!, 'sdk-runtime-evidence.json'), 262144); evidence.prerequisite = prerequisite;
    check(prerequisite.sourceSHA === process.env.SOURCE_SHA && prerequisite.outcome === 'runtime_prerequisite_measured', 'same-head accepted runtime prerequisite');
    const previous = prerequisite.actors as Json[]; check(Array.isArray(previous) && previous.length === 2, 'two actual prerequisite actors');
    check(previous[0]!.outcome === 'exact_current_user_readback' && previous[1]!.outcome === 'sdk_static_supported', 'actual prerequisite results');
    const baseline = token(object(previous[1]!.launcher).baseline);
    const provenance = object(prerequisite.provenance); evidence.provenance = provenance;
    const vendor = json(process.env.SDK_VENDOR_EVIDENCE!); evidence.vendor = vendor;
    check(vendor.outcome === 'exact_current_user_installed' && vendor.fullName === 'OpenAI.Codex_26.930.7945.0_arm64__2p2nqsd0c76g0' &&
      vendor.archiveSHA256 === 'a208d373c7c84aa3e0452cd3dd8406a6794d8139ec1260c64a770c2a00fbeeb8' && vendor.signatureVerified === true, 'fixed TEST vendor preflight');
    const launcherSource = resolve(process.env.LUA_LAUNCHER_EXE!), source = resolve(process.env.SDK_COLD_EXE!);
    check(hash(launcherSource) === process.env.LUA_LAUNCHER_SHA256 && hash(source) === process.env.SDK_COLD_EXE_SHA256, 'compiled same-source binary pins');
    generation = join(resolve(process.env.RUNNER_TEMP!), `TEST-lua-preflight-${nonce}`); evidence.generation = generation;
    const create = await run('private-create', launcherSource, ['--TEST-create-private-sdk-root', nonce, generation, 'coldclick'], process.env.RUNNER_TEMP!, 10000, true);
    completed(create); check(create.record?.nonce === nonce && create.record.pid === create.pid && create.record.created === true && create.record.protectedDACL === true && create.record.files === 5, 'native private root proof');
    const launcher = join(generation, `TEST-launcher-${nonce}.exe`), exe = join(generation, `TEST-sdk-${nonce}.exe`);
    const pins = new Map<string, string>();
    for (const [input, output] of [[launcherSource, launcher], [source, exe], [resolve(process.env.BOOTSTRAP_DLL!), join(generation, 'Microsoft.WindowsAppRuntime.Bootstrap.dll')]]) {
      check(lstatSync(output!).size === 0, 'native-owned empty leaf'); writeFileSync(output!, bytes(input!, 64 << 20), { flag: 'r+' }); pins.set(output!, hash(output!));
    }
    check(pins.get(exe) === process.env.SDK_COLD_EXE_SHA256 && pins.get(join(generation, 'Microsoft.WindowsAppRuntime.Bootstrap.dll')) === provenance.bootstrapSHA256, 'copy pin/source provenance');
    writeFileSync(join(generation, 'TEST-module-pins.txt'), `${provenance.bootstrapSHA256}\n${provenance.runtimeModuleSHA256}\n`, { flag: 'r+' });
    const binding = { nonce, exeSHA256: pins.get(exe), bootstrapSHA256: provenance.bootstrapSHA256, runtimeSHA256: provenance.runtimeModuleSHA256,
      identity: baseline, leaseMs: 65000, familyName: 'OpenAI.Codex_2p2nqsd0c76g0', fullName: vendor.fullName, uri: `codex://threads/${nonce}` };
    writeFileSync(join(generation, 'TEST-cold-binding.json'), JSON.stringify(binding), { flag: 'r+' });
    for (const leaf of ['TEST-module-pins.txt', 'TEST-cold-binding.json']) pins.set(join(generation, leaf), hash(join(generation, leaf)));
    check(readdirSync(generation).sort().join() === [...pins.keys()].map(p => basename(p)).sort().join(), 'closed generation without redirect sidecars');
    const held = new Map([...pins.keys()].map(p => [p, lstatSync(p)]));
    const seal = await run('private-seal', launcher, ['--TEST-seal-sdk-root', nonce, generation, 'coldclick'], generation, 10000, true);
    completed(seal); check(seal.record?.nonce === nonce && seal.record.pid === seal.pid && seal.record.sealed === true && seal.record.protectedDACL === true && seal.record.files === 5, 'sealed private generation');
    for (const [p, pin] of pins) check(hash(p) === pin && lstatSync(p).ino === held.get(p)!.ino, 'sealed held pins'); evidence.pins = Object.fromEntries(pins);
    const sender = await run('medium-sender', launcher, [historyEnabled ? '--TEST-sdk-cold-sender-history' : '--TEST-sdk-cold-sender', nonce, generation, exe], generation, 35000); retain(); completed(sender);
    const l = object(sender.record); check(l.nonce === nonce && l.pid === sender.pid && l.variant === (historyEnabled ? 'sdk-cold-sender-history' : 'sdk-cold-sender') && l.collected === true && l.childExit === 0 &&
      l.childTerminated === false && l.timedOut === false && l.cleanupError === null && l.queriesComplete === true && l.sameIdentitySession === true && l.enabledAdmins === false, 'actual contained sender exit');
    const s = object(JSON.parse(l.childRecord as string)); diagnostic(s, 'sender'); check(s.pid === l.childPid && s.birth === l.childBirth && s.showReturned === true && s.unregisterReturned === true &&
      s.handlerBeforeRegister === true && s.bootstrapHRESULT === 0 && s.isSupported === true && Number.isInteger(s.nativeID) && (s.nativeID as number) > 0, 'one actual native Show before death');
    check(s.selectedFramework === 'Microsoft.WindowsAppRuntime.2_2.5.1.0_arm64__8wekyb3d8bbwe' && object(s.bootstrapModule).sha256 === provenance.bootstrapSHA256 && object(s.runtimeModule).sha256 === provenance.runtimeModuleSHA256, 'actual sender pinned SDK graph/modules');
    const st = token(s.token, true); medium(st); sameIdentity(st, baseline); for (const k of Object.keys(token(l.held))) check(st[k] === object(l.held)[k], 'held/own sender token');
    // Native boot-clock correlation comes from the collected launcher's actual child report, not Date.now.
    const collectedBoot = l.collectionBootMs as number; check(Number.isSafeInteger(collectedBoot) && collectedBoot >= (s.bootMs as number), 'actual post-wait boot clock');
    durable(join(generation, 'TEST-sender-collected.json'), { nonce, pid: s.pid, birth: s.birth, collected: true, exitCode: 0, collectedBootMs: collectedBoot });
    if (historyEnabled) {
      check(performance.now() - start + 35000 + 110000 < 170000, 'history and joint UI reserve fit original global budget');
      const deadline = collectedBoot + 30000;
      durable(join(generation, 'TEST-history-request.json'), { nonce, enabled: true, deadlineBootMs: deadline });
      const historyActor = await run('medium-history', launcher, ['--TEST-sdk-cold-history', nonce, generation, exe], generation, 35000); retain();
      const launch = object(historyActor.record);
      check(launch.variant === 'sdk-cold-history' && launch.nonce === nonce && launch.pid === historyActor.pid && launch.collected === true && launch.childExit === 0 && launch.childTerminated === false && launch.timedOut === false && launch.cleanupError === null && launch.queriesComplete === true && launch.sameIdentitySession === true && launch.enabledAdmins === false && launch.childStderr === '', 'collected contained history actor before UI');
      completed(historyActor);
      const observed = object((evidence.receipts as Json)['TEST-history.json']); diagnostic(observed, 'history');
      evidence.historyDiagnosticsRetained = true;
      const wire = object(JSON.parse(launch.childRecord as string)); check(JSON.stringify(wire) === JSON.stringify(observed) && observed.pid === launch.childPid && observed.birth === launch.childBirth && observed.physicalImage === exe && observed.exeSHA256 === pins.get(exe) && observed.deadlineBootMs === deadline, 'same pinned history executable/receipt/held incarnation');
      const ht = token(observed.token, true); medium(ht); sameFacts(ht, st); sameIdentity(ht, baseline);
      for (const k of Object.keys(token(launch.held))) check(ht[k] === object(launch.held)[k], 'held and own history token');
      check(observed.bootstrapHRESULT === 0 && observed.isSupported === true && observed.selectedFramework === s.selectedFramework && object(observed.bootstrapModule).sha256 === provenance.bootstrapSHA256 && object(observed.bootstrapModule).path === object(s.bootstrapModule).path && object(observed.runtimeModule).sha256 === provenance.runtimeModuleSHA256 && object(observed.runtimeModule).path === object(s.runtimeModule).path && observed.bootstrapShutdown === true && observed.asyncStatus === 1 && !('cancelRequested' in observed), 'completed supported SDK history with shutdown');
      const digest = (x: unknown): boolean => typeof x === 'string' && /^[a-f0-9]{64}$/.test(x);
      const registry = (value: unknown): Json => {
        const r = object(value); exact(r, ['pathKey', 'notificationGUID', 'lastWrite', 'valueBytes', 'values', 'subkeys', 'guidBytesSHA256']);
        check(r.pathKey === `Software\\Classes\\AppUserModelId\\${exe.replaceAll('\\', '.')}` && typeof r.notificationGUID === 'string' && /^[{][a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}[}]$/.test(r.notificationGUID) && typeof r.lastWrite === 'string' && /^[0-9]{1,20}$/.test(r.lastWrite) && [76, 78].includes(r.valueBytes as number) && r.values === 1 && r.subkeys === 0 && digest(r.guidBytesSHA256), 'bounded existing own SDK path/GUID snapshot'); return r;
      };
      const registered = registry(s.registryIdentity), before = registry(observed.registryBefore), after = registry(observed.registryAfter);
      for (const k of Object.keys(registered)) check(registered[k] === before[k] && before[k] === after[k], 'existing sender registry identity unchanged before/after Default/query');
      check(digest(s.payloadSHA256) && observed.expectedPayloadSHA256 === s.payloadSHA256 && observed.senderID === s.nativeID && Number.isInteger(observed.count) && (observed.count as number) >= 0 && (observed.count as number) <= 32 && Number.isInteger(observed.matchingIDCount) && (observed.matchingIDCount as number) >= 0 && (observed.matchingIDCount as number) <= (observed.count as number) && typeof observed.exactPayloadMatch === 'boolean', 'bounded actual history ID and exact UTF8 Payload digest');
      if ((observed.matchingIDCount as number) > 0) check(digest(observed.observedPayloadSHA256), 'bounded matching-ID payload digest');
      else check(!('observedPayloadSHA256' in observed), 'no invented payload digest');
      const intent = object((evidence.receipts as Json)['TEST-history-intent.json']); correlation(intent, 'history');
      for (const k of ['nonce', 'pid', 'birth', 'bootMs', 'deadlineBootMs', 'physicalImage', 'exeSHA256', 'senderID', 'expectedPayloadSHA256']) check(intent[k] === observed[k], 'immutable pre-SDK history intent');
      sameFacts(token(intent.token, true), ht); for (const k of Object.keys(before)) check(registry(intent.registryBefore)[k] === before[k], 'intent preconstructor registry identity');
      check([observed.bootMs, observed.bootstrapCallBootMs, observed.queryCallBootMs, observed.queryReturnedBootMs, observed.endBootMs, launch.collectionBootMs].every(x => Number.isSafeInteger(x)) && collectedBoot <= (observed.bootMs as number) && (observed.bootMs as number) <= (observed.bootstrapCallBootMs as number) && (observed.bootstrapCallBootMs as number) <= (observed.queryCallBootMs as number) && (observed.queryCallBootMs as number) <= (observed.queryReturnedBootMs as number) && (observed.queryReturnedBootMs as number) <= (observed.endBootMs as number) && (observed.endBootMs as number) < deadline && (observed.endBootMs as number) <= (launch.collectionBootMs as number) && (launch.collectionBootMs as number) < deadline, 'shared absolute budget and actual collection before UI');
      const exactMatch = observed.count !== 0 && observed.matchingIDCount === 1 && observed.observedPayloadSHA256 === s.payloadSHA256;
      check(observed.exactPayloadMatch === exactMatch && ['completed_empty', 'completed_mismatch', 'completed_exact'].includes(observed.outcome as string) && (observed.outcome === 'completed_empty') === (observed.count === 0) && (observed.outcome === 'completed_exact') === exactMatch, 'completed non-null history classification');
      if (!exactMatch) { for (const [p, pin] of pins) check(hash(p) === pin && lstatSync(p).ino === held.get(p)!.ino, 'negative history post-run generation custody'); evidence.outcome = observed.outcome; evidence.historyDiagnosticsValidated = true; return; }
      evidence.historyDiagnosticsValidated = true;
    }
    // Start collector first but do not await it: synchronous Shell Invoke can wait for receiver registration.
    check(performance.now() - start + 110000 < 170000, 'joint collector/Invoke admission reserve');
    const collectorPromise = run('cold-collector', exe, ['--TEST-sdk-cold-collect', nonce], generation, 105000);
    const invokeMode = scenario === 'disposable_global_shortcut' ? ['disposable-global-shortcut-invoke', '--TEST-sdk-cold-invoke-global-shortcut'] : scenario === 'shell_foreground' ? ['owned-shell-invoke', '--TEST-sdk-cold-invoke'] : ['owned-test-foreground-invoke', '--TEST-sdk-cold-invoke-own-foreground'];
    const invokePromise = run(invokeMode[0]!, exe, [invokeMode[1]!, nonce], generation, 35000);
    const settled = await Promise.allSettled([collectorPromise, invokePromise]); retain();
    check(settled.every(r => r.status === 'fulfilled'), 'all started own actors collected or explicitly unknown');
    const collector = (settled[0] as PromiseFulfilledResult<Actor>).value, invoked = (settled[1] as PromiseFulfilledResult<Actor>).value;
    for (const [leaf, phase, actor] of [['TEST-collector.json', 'collector', collector], ['TEST-ui-invoke.json', 'shell_invoke', invoked]] as const) {
      const observed = object((evidence.receipts as Json)[leaf]); diagnostic(observed, phase); check(observed.pid === actor.pid, 'diagnostic actor incarnation');
    }
    const ui = object((evidence.receipts as Json)['TEST-ui-invoke.json']);
    check(ui.scenario === scenario, 'fixed CLI foreground scenario');
    if (historyEnabled) check(ui.deadlineBootMs === collectedBoot + 30000 && (ui.bootMs as number) >= (object((evidence.receipts as Json)['TEST-history.json']).endBootMs as number), 'UI consumes original history deadline without reset');
    const windowRecord = (evidence.receipts as Json)['TEST-owned-foreground.json'];
    if (windowRecord) {
      const w = object(windowRecord), intent = object((evidence.receipts as Json)['TEST-owned-foreground-intent.json']); correlation(w, 'owned_foreground'); correlation(intent, 'owned_foreground');
      check(scenario === 'owned_test_foreground' && w.scenario === scenario && w.pid === ui.pid && w.birth === ui.birth && w.physicalImage === exe && w.exeSHA256 === pins.get(exe) && w.deadlineBootMs === ui.deadlineBootMs && w.className === `NavigationColdForegroundTEST-${nonce}`, 'owned generation/window lifecycle');
      sameIdentity(token(w.token, true), baseline); sameFacts(token(intent.token, true), token(w.token, true));
      check((ui.completedCensusAttempts as number) >= 1 && Number.isSafeInteger(ui.firstCompletedCensusBootMs) && (w.createIntentBootMs as number) >= (ui.firstCompletedCensusBootMs as number), 'own window only after first complete census');
      for (const k of ['nonce', 'pid', 'birth', 'bootMs', 'scenario', 'creatorTID', 'className', 'physicalImage', 'exeSHA256', 'deadlineBootMs', 'createIntentBootMs']) check(intent[k] === w[k], 'immutable lifecycle create/show intent');
      check(Number.isInteger(w.creatorTID) && (w.creatorTID as number) > 0 && (w.creatorTID as number) <= 0xffffffff && intent.windowCreated === false && intent.foregroundAttempts === 0 && [0, 1].includes(w.foregroundAttempts as number) && typeof w.windowCreated === 'boolean' && typeof w.destroyed === 'boolean' && typeof w.windowCustodyKnown === 'boolean' && typeof w.ownerLifetimeRetained === 'boolean' && w.ownerLifetimeRetained === !w.windowCustodyKnown, 'one creator/foreground attempt');
      if (w.windowCreated === true) check(Number.isInteger(w.startupFlags) && (w.startupFlags as number) >= 0 && (w.startupFlags as number) <= 0xffffffff && Number.isInteger(w.startupShowWindow) && (w.startupShowWindow as number) >= 0 && (w.startupShowWindow as number) <= 0xffff && typeof w.visible === 'boolean', 'actual startup/visibility observations');
      const clocks = ['createIntentBootMs', ...(w.windowCreated ? ['createdBootMs'] : []), ...(w.foregroundAttempts === 1 ? ['foregroundCallBootMs', 'foregroundReturnedBootMs'] : []), ...('destroyCallBootMs' in w ? ['destroyCallBootMs'] : []), 'endBootMs'].map(k => w[k]);
      check(clocks.every(n => Number.isSafeInteger(n) && (n as number) >= (w.bootMs as number)) && clocks.every((n, i) => i === 0 || (n as number) >= (clocks[i - 1] as number)) && (w.createIntentBootMs as number) >= collectedBoot, 'finite owned lifecycle order');
      if (ui.inputEffectUnknown === false && ui.centerAttempted === true) check(w.windowCreated === true && w.visible === true && w.setForegroundReturned === true && w.actualForeground === true && w.foregroundAttempts === 1 && w.destroyed === true && w.windowCustodyKnown === true, 'actual owned foreground and destruction custody');
      if (ui.invokeReturned === true) check(w.destroyed === true && w.windowCustodyKnown === true && (w.destroyCallBootMs as number) >= (ui.invokeReturnedBootMs as number), 'window held through Invoke return');
    } else {
      check(!(evidence.receipts as Json)['TEST-owned-foreground-intent.json'], 'GUI terminal required after lifecycle intent');
      if (scenario === 'owned_test_foreground') {
        check(ui.centerAttempted === false && ui.inputEffectUnknown === false && !(evidence.receipts as Json)['TEST-center-input-intent.json'] && !(evidence.receipts as Json)['TEST-center-input.json'], 'direct banner lane has no window/input effects');
        if (ui.invokeReturned === true) check((ui.completedCensusAttempts as number) >= 2 && ui.maxOwnedTitleMatches === 1, 'fresh complete double census before direct banner Invoke');
      }
    }
    check(typeof ui.centerAttempted === 'boolean' && typeof ui.inputEffectUnknown === 'boolean', 'actual center discriminator flags');
    if (ui.centerAttempted === true) {
      const input = object((evidence.receipts as Json)['TEST-center-input.json']); correlation(input, 'center_input');
      check(input.scenario === scenario && input.pid === ui.pid && input.birth === ui.birth && input.deadlineBootMs === ui.deadlineBootMs && input.centerOpenedProved === false && input.completedCensusAttempts === 1 && input.firstCompletedCensusBootMs === ui.firstCompletedCensusBootMs && typeof input.inputEffectUnknown === 'boolean', 'single correlated center discriminator');
      for (const k of ['endBootMs', 'acceptedEvents', 'cleanupReleaseAttempts', 'sendError']) check(Number.isSafeInteger(input[k]) && (input[k] as number) >= 0, 'bounded actual input custody');
      check((input.sendError as number) <= 0xffffffff && (input.acceptedEvents as number) <= 6 && (input.cleanupReleaseAttempts as number) <= 2 && typeof input.releaseUnknown === 'boolean' && typeof input.ownedKeysReleased === 'boolean' && ui.inputEffectUnknown === input.inputEffectUnknown, 'own down/release observations');
      if (input.inputEffectUnknown === false) {
        const intent = object((evidence.receipts as Json)['TEST-center-input-intent.json']); correlation(intent, 'center_input');
        for (const k of ['nonce', 'scenario', 'pid', 'birth', 'bootMs', 'deadlineBootMs', 'firstCompletedCensusBootMs', 'completedCensusAttempts', 'foregroundPID', 'foregroundBirth', 'globalSourceSHA', 'windowsClient', 'intentBootMs', 'disposableAuthority', 'initialKeysReleased']) check(intent[k] === input[k], 'immutable input intent');
        const clocks = ['firstCompletedCensusBootMs', 'intentBootMs', 'inputCallBootMs', 'inputReturnedBootMs', 'endBootMs'].map(k => input[k]);
        check(clocks.every(n => Number.isSafeInteger(n) && (n as number) >= 0) && clocks.every((n, i) => i === 0 || (n as number) >= (clocks[i - 1] as number)) && (input.inputCallBootMs as number) < (ui.deadlineBootMs as number), 'original input admission/order clocks');
        check(input.initialKeysReleased === true && input.disposableAuthority === true && input.outcome === 'input_accepted' && input.acceptedEvents === 4 && input.cleanupReleaseAttempts === 0 && input.releaseUnknown === false && input.ownedKeysReleased === true && input.sendError === 0, 'four accepted own events require released custody');
        if (scenario === 'disposable_global_shortcut') check(input.globalSourceSHA === process.env.SOURCE_SHA && input.windowsClient === true && !('foregroundPID' in input) && !('foregroundBirth' in input) && !(evidence.receipts as Json)['TEST-owned-foreground.json'] && !(evidence.receipts as Json)['TEST-owned-foreground-intent.json'], 'explicit disposable global shortcut without foreground effects');
        else check(Number.isInteger(input.foregroundPID) && (input.foregroundPID as number) > 0 && (input.foregroundPID as number) <= 0xffffffff && /^[1-9][0-9]{1,19}$/.test(input.foregroundBirth as string) && !('globalSourceSHA' in input) && !('windowsClient' in input), 'historical foreground authority unchanged');
        if (scenario === 'owned_test_foreground') check(input.foregroundPID === ui.pid && input.foregroundBirth === ui.birth, 'own foreground incarnation before chord');
        if (ui.invokeReturned === true) check((ui.completedCensusAttempts as number) >= 3 && (ui.lastCompletedCensusBootMs as number) >= (input.inputReturnedBootMs as number) && (ui.invokeCallBootMs as number) >= (input.inputReturnedBootMs as number), 'fresh complete post-input censuses before Invoke');
        const inputSender = object(input.sender); check(inputSender.nonce === nonce && inputSender.pid === s.pid && inputSender.birth === s.birth && inputSender.collected === true && inputSender.exitCode === 0 && inputSender.collectedBootMs === collectedBoot && (input.intentBootMs as number) >= collectedBoot, 'collected sender before one input');
      } else check(!('invokeCallBootMs' in ui) && ui.outcome === 'unknown' && input.outcome === 'unknown' && Number.isSafeInteger(input.failureBootMs) && (input.failureBootMs as number) <= (input.endBootMs as number), 'uncertain input forbids Invoke');
    } else check(ui.inputEffectUnknown === false && !(evidence.receipts as Json)['TEST-center-input-intent.json'] && !(evidence.receipts as Json)['TEST-center-input.json'], 'zero input attempt');
    evidence.actorDiagnosticsValidated = true; completed(collector);
    const r = evidence.receipts as Json, c = object(r['TEST-collector.json']), ready = object(r['TEST-receiver-ready.json']), terminal = object(r['TEST-receiver-terminal.json']);
    correlation(c, 'collector'); correlation(ready, 'receiver_ready'); correlation(terminal, 'receiver_terminal');
    check(c.pid === collector.pid && c.collected === true && c.exitCode === 0 && c.ackPublished === true && c.heldPhysicalImage === exe &&
      ready.pid !== s.pid && ready.pid !== collector.pid && ready.pid !== invoked.pid && terminal.pid === ready.pid && terminal.birth === ready.birth &&
      object(c.ready).pid === ready.pid && object(c.ready).birth === ready.birth, 'held cold receiver collection');
    const ct = token(ready.token, true); sameIdentity(ct, baseline);
    check(!r['TEST-second-receiver.json'] && !r['TEST-second-callback.json'], 'second activation/incarnation rejected');
    if (terminal.outcome === 'sdk_unsupported_token') {
      check(!(ct.elevated === false && ct.integrityRID === 0x2000 && ct.enabledAdmins === false) &&
        !r['TEST-query-intent.json'] && !r['TEST-launch-intent.json'] && !r['TEST-callback.json'], 'unsupported token must have zero SDK/handoff effects');
      evidence.outcome = 'sdk_unsupported_cold_token'; return;
    }
    completed(invoked);
    medium(ct); check(terminal.bootstrapHRESULT === 0 && terminal.isSupported === true && terminal.handlerBeforeRegister === true && terminal.selectedFramework === s.selectedFramework && object(terminal.bootstrapModule).sha256 === provenance.bootstrapSHA256 && object(terminal.runtimeModule).sha256 === provenance.runtimeModuleSHA256, 'actual cold pinned SDK graph/modules');
    const click = object(r['TEST-ui-invoke.json']); correlation(click, 'shell_invoke'); check(click.pid === invoked.pid && click.exactOwnedTitle === true && click.invokeReturned === true && click.invokeHRESULT === 0, 'genuine owned Shell Invoke');
    const callback = object(r['TEST-callback.json']); correlation(callback, 'callback');
    sameFacts(token(c.heldToken, true), ct);
    check(callback.pid === ready.pid && callback.birth === ready.birth && ['AppInstance_AppNotification', 'NotificationInvoked'].includes(callback.eventKind as string) &&
      callback.argument === `TEST-cold-${nonce}` && callback.outcome === 'handoff_accepted' && callback.launchEntered === true && callback.launchReturned === true && callback.accepted === true &&
      callback.timely === true && object(r['TEST-ui-invoke-intent.json']).pid === invoked.pid &&
      (ready.bootMs as number) >= (object(r['TEST-ui-invoke-intent.json']).bootMs as number) && (callback.entryBootMs as number) >= (ready.bootMs as number) && callback.uriSupport === 0 && callback.uri === binding.uri && callback.familyName === binding.familyName &&
      (callback.entryBootMs as number) > collectedBoot && (callback.deadlineBootMs as number) <= (ready.leaseDeadlineBootMs as number) - 5000 &&
      terminal.outcome === 'callback_observed' && terminal.unregisterReturned === true && terminal.bootstrapShutdown === true, 'actual bounded cold callback/targeted TEST handoff');
    const queryIntent = object(r['TEST-query-intent.json']), launchIntent = object(r['TEST-launch-intent.json']);
    correlation(queryIntent, 'query_intent'); correlation(launchIntent, 'launch_intent');
    const intentBase = ['nonce', 'phase', 'pid', 'birth', 'bootMs', 'eventKind', 'token', 'argument', 'entryBootMs', 'deadlineBootMs', 'sender', 'uri', 'familyName', 'queryAdmissionBootMs', 'queryDeadlineBootMs', 'queryEntered'];
    exact(queryIntent, [...intentBase, 'queryBoundaryArmed']);
    exact(launchIntent, [...intentBase, 'queryCallBootMs', 'queryReturnedBootMs', 'queryReturned', 'uriSupport', 'launchAdmissionBootMs', 'launchDeadlineBootMs', 'launchEntered', 'launchBoundaryArmed']);
    const actionDeadline = Math.min((callback.entryBootMs as number) + 30000, (ready.leaseDeadlineBootMs as number) - 5000);
    for (const intent of [queryIntent, launchIntent]) {
      check(intent.pid === callback.pid && intent.birth === callback.birth && intent.eventKind === callback.eventKind && intent.argument === callback.argument &&
        intent.uri === binding.uri && intent.familyName === binding.familyName && intent.entryBootMs === callback.entryBootMs &&
        intent.deadlineBootMs === actionDeadline && intent.bootMs === callback.bootMs, 'durable intent activation/route/action deadline');
      sameFacts(token(intent.token, true), ct);
      const senderProof = object(intent.sender); exact(senderProof, ['nonce', 'pid', 'birth', 'collected', 'exitCode', 'collectedBootMs']);
      check(senderProof.nonce === nonce && senderProof.pid === s.pid && senderProof.birth === s.birth && senderProof.collected === true && senderProof.exitCode === 0 && senderProof.collectedBootMs === collectedBoot, 'durable intent collected sender incarnation');
    }
    const clockFields = ['entryBootMs', 'deadlineBootMs', 'queryAdmissionBootMs', 'queryDeadlineBootMs', 'queryCallBootMs', 'queryReturnedBootMs', 'launchAdmissionBootMs', 'launchDeadlineBootMs', 'launchCallBootMs', 'launchReturnedBootMs'];
    check(clockFields.every(k => Number.isSafeInteger(callback[k]) && (callback[k] as number) >= 0) && callback.deadlineBootMs === actionDeadline, 'finite exact observed action/phase clocks');
    for (const k of ['queryAdmissionBootMs', 'queryDeadlineBootMs']) check(queryIntent[k] === callback[k] && launchIntent[k] === callback[k], 'fixed durable query phase cap');
    for (const k of ['queryCallBootMs', 'queryReturnedBootMs']) check(launchIntent[k] === callback[k], 'query completion retained before launch intent');
    for (const k of ['launchAdmissionBootMs', 'launchDeadlineBootMs']) check(launchIntent[k] === callback[k], 'fixed durable launch phase cap');
    check(queryIntent.queryBoundaryArmed === true && queryIntent.queryEntered === false && callback.queryEntered === true && callback.queryReturned === true &&
      launchIntent.queryEntered === true && launchIntent.queryReturned === true && launchIntent.uriSupport === 0 && launchIntent.launchBoundaryArmed === true && launchIntent.launchEntered === false &&
      callback.queryDeadlineBootMs === Math.min(actionDeadline, (callback.queryAdmissionBootMs as number) + 10000) &&
      callback.launchDeadlineBootMs === Math.min(actionDeadline, (callback.launchAdmissionBootMs as number) + 15000) &&
      (callback.entryBootMs as number) <= (callback.queryAdmissionBootMs as number) && (callback.queryAdmissionBootMs as number) <= (callback.queryCallBootMs as number) &&
      (callback.queryCallBootMs as number) <= (callback.queryReturnedBootMs as number) && (callback.queryReturnedBootMs as number) < (callback.queryDeadlineBootMs as number) &&
      (callback.queryReturnedBootMs as number) <= (callback.launchAdmissionBootMs as number) && (callback.launchAdmissionBootMs as number) <= (callback.launchCallBootMs as number) &&
      (callback.launchCallBootMs as number) <= (callback.launchReturnedBootMs as number) && (callback.launchReturnedBootMs as number) < (callback.launchDeadlineBootMs as number), 'durable pre-entry caps and observed Query-before-Launch order');
    const callbackToken = token(callback.token, true); medium(callbackToken); sameIdentity(callbackToken, ct); sameFacts(callbackToken, ct);
    for (const [p, pin] of pins) check(hash(p) === pin, 'post-run generation custody');
    evidence.sdkColdActivationQualified = true; evidence.outcome = 'genuine_cold_handoff_accepted';
  } catch (e) { evidence.sdkColdActivationQualified = false; evidence.outcome = 'unknown'; evidence.error = String(e).slice(0, 2048); try { retain(); } catch (readError) { evidence.receiptReadError = String(readError).slice(0, 512); } }
  finally {
    durable(join(output, 'sdk-coldclick-evidence.json'), evidence);
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${output}\n`, { flag: 'a', flush: true });
    process.exitCode = evidence.sdkColdActivationQualified === true ? 0 : 1;
  }
}
await main();
