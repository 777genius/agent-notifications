// Windows client CI only. Native source is compiled from this exact checkout.
import { spawnSync } from 'node:child_process';
import { randomUUID, createHash } from 'node:crypto';
import { mkdirSync, writeFileSync, readFileSync, existsSync, realpathSync, copyFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';

type Json = Record<string, unknown>;
type Step = { mode: string; pid: number; status: number | null; signal: string | null;
  stdout: string; stderr: string; error?: string; exitedAt: number };
const evidence: Json = { status: 'failed', nativeCallbackQualified: false, navigationQualified: false,
  scope: 'Windows client native TEST cold toast COM callback only', clientRouteTested: false,
  showAttempts: 0, steps: [], sourceSHA: process.env.NAVIGATION_SOURCE_SHA ?? null,
  runnerLabel: process.env.NAVIGATION_WINDOWS_RUNNER ?? null,
  imageVersion: process.env.ImageVersion ?? null, nodeVersion: process.version };
const steps: Step[] = [];
let root: string | undefined;
let binary: string | undefined;
let nonce: string | undefined;
let installed = false;
let exitCode = 1;
function read(name: string): Json {
  if (!root) throw new Error('owned root absent');
  const path = join(root, name);
  if (statSync(path).size > 16_384) throw new Error(`oversized ${name}`);
  const value: unknown = JSON.parse(readFileSync(path, 'utf8'));
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`invalid ${name}`);
  return value as Json;
}
function run(mode: string, timeout: number): Step {
  if (!root || !binary || !nonce) throw new Error('owned fixture absent');
  const result = spawnSync(binary, [mode, root, nonce], { cwd: root, encoding: 'utf8',
    env: { ...process.env, AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E: '1' }, timeout, maxBuffer: 65_536,
    windowsHide: false });
  const step: Step = { mode, pid: result.pid, status: result.status, signal: result.signal,
    stdout: result.stdout ?? '', stderr: result.stderr ?? '', exitedAt: Date.now(),
    ...(result.error ? { error: result.error.message } : {}) };
  steps.push(step); evidence.steps = steps;
  return step;
}
function requireSuccess(step: Step): void {
  if (step.error || step.signal || step.status !== 0) throw new Error(`${step.mode} failed (${step.status}): ${step.error ?? step.stderr}`);
}
async function main(): Promise<void> {
  if (process.env.AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E !== '1' || process.env.CI !== 'true'
      || process.env.GITHUB_ACTIONS !== 'true' || process.platform !== 'win32') {
    throw new Error('requires explicit opt-in on disposable GitHub Windows CI');
  }
  if (process.env.NAVIGATION_WINDOWS_RUNNER !== 'windows-11-vs2026-arm') {
    throw new Error('qualification requires explicit supported Windows 11 client label');
  }
  if (Number(process.versions.node.split('.')[0]) !== 24) throw new Error('requires Node 24');
  nonce = randomUUID();
  root = join(realpathSync(process.env.RUNNER_TEMP ?? tmpdir()), `navigation-windows-test-${nonce}`);
  mkdirSync(root, { recursive: false });
  writeFileSync(join(root, '.owned-test-root'), `TEST navigation Windows ${nonce}\n`, { flag: 'wx' });
  const built = realpathSync(resolve(process.argv[2] ?? ''));
  if (!statSync(built).isFile()) throw new Error('compiled native probe absent');
  binary = join(root, 'navigation-native-probe.exe'); copyFileSync(built, binary);
  evidence.root = root; evidence.nonce = nonce;
  evidence.binarySHA256 = createHash('sha256').update(readFileSync(binary)).digest('hex');
  const preflight = run('preflight', 15_000);
  if (existsSync(join(root, 'preflight.json'))) evidence.preflight = read('preflight.json');
  if (preflight.status === 3) {
    evidence.status = 'unavailable'; throw new Error('Windows client/input desktop/Shell prerequisite unavailable');
  }
  requireSuccess(preflight);
  if ((evidence.preflight as Json).ready !== true) throw new Error('preflight not ready');
  // Mark before native send, including uncertain outcomes. Never retry Show.
  installed = true; evidence.showAttempts = 1;
  writeFileSync(join(root, 'submission-attempted'), nonce, { flag: 'wx' });
  const sender = run('send', 20_000);
  if (existsSync(join(root, 'sender.json'))) evidence.sender = read('sender.json');
  if (sender.status === 3) {
    evidence.status = 'unavailable'; evidence.showAttempts = 0;
    throw new Error('native notifier Setting is not Enabled; settings are not changed');
  }
  requireSuccess(sender);
  evidence.submitted = read('submitted.json');
  const senderReceipt = evidence.sender as Json;
  if (senderReceipt.pid !== sender.pid || senderReceipt.nonce !== nonce || senderReceipt.showAttempted !== true) {
    throw new Error('native sender correlation mismatch');
  }
  evidence.senderExitedBeforeInvoke = true; evidence.senderExitedAt = sender.exitedAt;
  // No controller call to callback mode/CoCreateInstance. OS is the only cold-server launcher.
  const invoke = run('invoke', 30_000);
  if (existsSync(join(root, 'ui-invoke.json'))) evidence.uiInvoke = read('ui-invoke.json');
  requireSuccess(invoke);
  if ((evidence.uiInvoke as Json).invokeHRESULT !== 0) throw new Error('UI provider did not accept native Invoke');
  for (let i = 0; i < 100 && !existsSync(join(root, 'callback.json')); i++) await delay(100);
  if (!existsSync(join(root, 'callback.json'))) throw new Error('no OS-launched COM callback receipt');
  const callback = read('callback.json'); evidence.callback = callback;
  const expectedAUMID = `AgentNotify.Navigation.TEST.${nonce}`;
  if (callback.matches !== true || callback.nonce !== nonce || callback.aumid !== expectedAUMID
      || typeof callback.pid !== 'number' || callback.pid <= 0 || callback.pid === sender.pid
      || callback.pid === invoke.pid || callback.pid === process.pid
      || typeof callback.startedAt !== 'number' || callback.startedAt < sender.exitedAt) {
    throw new Error('cold callback identity/correlation/post-sender-exit contract violated');
  }
  evidence.status = 'qualified'; evidence.nativeCallbackQualified = true;
  evidence.actualNativeUIInvoke = true; evidence.freshCallbackPID = true; exitCode = 0;
}
try {
  await main();
} catch (error: unknown) {
  evidence.error = error instanceof Error ? error.message : String(error);
} finally {
  if (root && installed) {
    // Only the UUID registration/shortcut/toast history. Native callback exits itself in <=30s.
    const cleanup = run('cleanup', 15_000);
    if (existsSync(join(root, 'cleanup.json'))) evidence.cleanup = read('cleanup.json');
    if (cleanup.error || cleanup.status !== 0 || cleanup.signal) {
      evidence.cleanupFailed = true; evidence.nativeCallbackQualified = false;
      evidence.status = 'failed'; exitCode = 1;
    }
  }
  if (root) {
    for (const name of ['preflight.json', 'sender.json', 'submitted.json', 'callback-started.json', 'callback.json', 'ui-candidate.json', 'ui-invoke.json', 'center-open.json']) {
      if (!existsSync(join(root, name))) continue;
      try { evidence[name] = read(name); } catch (error: unknown) { evidence[`${name}ReadError`] = String(error); }
    }
    writeFileSync(join(root, 'evidence.json'), `${JSON.stringify(evidence, null, 2)}\n`, { flag: 'wx' });
    // A single known path is the workflow artifact handoff, including failed/unavailable reports.
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${root}\n`, { flag: 'a' });
  }
  console.log(JSON.stringify(evidence, null, 2)); process.exitCode = exitCode;
}
