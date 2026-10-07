// A false/Medium/Admin-disabled same-identity token is ONLY a token prerequisite.
import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { closeSync, constants, copyFileSync, fsyncSync, mkdtempSync, openSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const started = performance.now(), nonce = randomUUID();
if (!process.env.RUNNER_TEMP) throw new Error('own runner TEST root required');
const root = mkdtempSync(join(process.env.RUNNER_TEMP, 'TEST-lua-preflight-'));
const evidence: Record<string, unknown> = { nonce, sourceSHA: process.env.SOURCE_SHA,
  outcome: 'unknown', executedLauncher: false, sdkNativeCallback: false, notificationEffects: 0 };
const hash = (path: string): string => createHash('sha256').update(readFileSync(path)).digest('hex');
const hex = (v: unknown): boolean => typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);
const integer = (v: unknown, max = 0xffffffff): v is number => typeof v === 'number' && Number.isInteger(v) && v >= 0 && v <= max;
function check(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }
function object(v: unknown): Record<string, unknown> {
  check(v !== null && typeof v === 'object' && !Array.isArray(v), 'object required');
  return v as Record<string, unknown>;
}
function schema(v: Record<string, unknown>, keys: string[]): void {
  check(Object.keys(v).sort().join() === keys.sort().join(), 'exact schema required');
}
function facts(v: unknown): Record<string, unknown> {
  const f = object(v);
  schema(f, ['sidSHA256', 'authLUIDSHA256', 'session', 'elevated', 'elevationType', 'integrityRID']);
  check(hex(f.sidSHA256) && hex(f.authLUIDSHA256) && integer(f.session) && typeof f.elevated === 'boolean' &&
    integer(f.elevationType, 3) && f.elevationType >= 1 && integer(f.integrityRID), 'invalid token facts');
  check(!(f.elevationType === 2 && !f.elevated) && !(f.elevationType === 3 && f.elevated), 'inconsistent token type');
  return f;
}
async function run(): Promise<void> {
  check(process.platform === 'win32' && process.arch === 'arm64' && /^[a-f0-9]{40}$/.test(process.env.SOURCE_SHA ?? ''), 'trusted Windows ARM64 source required');
  const pins = [
    { original: process.env.LUA_LAUNCHER_EXE, pin: process.env.LUA_LAUNCHER_SHA256, copy: join(root, `TEST-lua-${nonce}.exe`) },
    { original: process.env.TOKEN_PROBE_EXE, pin: process.env.TOKEN_PROBE_SHA256, copy: join(root, `TEST-token-${nonce}.exe`) },
  ];
  for (const p of pins) {
    check(p.original && hex(p.pin), 'compiled executable pins required');
    p.original = resolve(p.original);
    check(hash(p.original) === p.pin, 'original executable pin mismatch');
    copyFileSync(p.original, p.copy, constants.COPYFILE_EXCL);
    check(hash(p.copy) === p.pin, 'copied executable pin mismatch');
  }
  evidence.binaryPins = pins.map((p) => ({ sha256: p.pin, basename: p.copy.slice(root.length + 1) }));
  let stdout = '', stderr = '', bytes = 0, overflow = false, timedOut = false, collected = false;
  let spawnError: string | undefined;
  const child = spawn(pins[0]!.copy, ['--TEST-lua-token-preflight', nonce, root, pins[1]!.copy],
    { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  evidence.executedLauncher = child.pid !== undefined;
  evidence.launcherPid = child.pid;
  const capture = (data: Buffer, stream: 'stdout' | 'stderr'): void => {
    const part = data.subarray(0, Math.max(0, 16384 - bytes)).toString('utf8');
    if (stream === 'stdout') stdout += part; else stderr += part;
    bytes += data.length;
    if (bytes > 16384 && !overflow) { overflow = true; child.kill(); }
  };
  child.stdout.on('data', (data: Buffer) => capture(data, 'stdout'));
  child.stderr.on('data', (data: Buffer) => capture(data, 'stderr'));
  const exit = await new Promise<{ code: number | null; signal: string | null }>((done) => {
    const timeout = setTimeout(() => { timedOut = true; child.kill(); }, 20000);
    const deadline = setTimeout(() => {
      timedOut = true; child.kill(); child.stdout.destroy(); child.stderr.destroy(); child.unref();
      clearTimeout(timeout); done({ code: null, signal: 'collection_deadline' });
    }, 25000);
    child.once('error', (error) => { spawnError = error.message.slice(0, 512); });
    child.once('close', (code, signal) => { collected = true; clearTimeout(timeout); clearTimeout(deadline); done({ code, signal }); });
  });
  Object.assign(evidence, { stdout, stderr, exit, overflow, timedOut, collected, spawnError });
  for (const p of pins) check(p.original && hash(p.original) === p.pin && hash(p.copy) === p.pin, 'post-run executable pin mismatch');
  check(collected && !overflow && !timedOut && !spawnError && stderr === '' && performance.now() - started < 30000, 'launcher collection failed; no retry');
  check(stdout.trim().split(/\r?\n/).length === 1, 'one launcher JSON record required');
  const r = object(JSON.parse(stdout)); evidence.record = r;
  schema(r, ['nonce', 'pid', 'baseline', 'held', 'childPid', 'childBirth', 'enabledAdmins', 'sameIdentitySession',
    'collected', 'timedOut', 'childExit', 'childTerminated', 'cleanupError', 'childRecord', 'childStderr', 'queriesComplete', 'query', 'error', 'sdkNativeCallback', 'notificationEffects']);
  check(r.nonce === nonce && r.pid === child.pid && integer(r.pid) && r.pid > 0 &&
    typeof r.queriesComplete === 'boolean' && r.sdkNativeCallback === false && r.notificationEffects === 0, 'invalid launcher identity/schema');
  if (!r.queriesComplete) {
    check(exit.code !== 0 && typeof r.query === 'string' && integer(r.error), 'invalid launcher failure');
    throw new Error('restricted-child prerequisite unknown');
  }
  check(exit.code === 0 && exit.signal === null && r.query === null && r.error === null && r.collected === true &&
    r.timedOut === false && r.childTerminated === false && r.cleanupError === null && r.childExit === 0 && integer(r.childPid) && r.childPid > 0 && r.childPid !== r.pid &&
    typeof r.childBirth === 'string' && /^[1-9][0-9]{1,19}$/.test(r.childBirth) && typeof r.enabledAdmins === 'boolean' &&
    r.sameIdentitySession === true && typeof r.childRecord === 'string' && Buffer.byteLength(r.childRecord) <= 8192 &&
    r.childStderr === '', 'incomplete held-child collection');
  const baseline = facts(r.baseline), held = facts(r.held);
  for (const key of ['sidSHA256', 'authLUIDSHA256', 'session']) check(baseline[key] === held[key], 'identity/session drift');
  check(r.childRecord.trim().split(/\r?\n/).length === 1, 'one child JSON record required');
  const c = object(JSON.parse(r.childRecord)); evidence.child = c;
  schema(c, ['nonce', 'pid', 'session', 'elevated', 'elevationType', 'integrityRID', 'sidSHA256', 'authLUIDSHA256',
    'wtsState', 'queriesComplete', 'tokenPrecondition', 'nativeEffects']);
  check(c.nonce === nonce && c.pid === r.childPid && c.queriesComplete === true && c.nativeEffects === 0 && integer(c.wtsState, 9), 'child correlation/query failure');
  for (const key of ['sidSHA256', 'authLUIDSHA256', 'session', 'elevated', 'elevationType', 'integrityRID'])
    check(c[key] === held[key], 'held token and child observations differ');
  check(c.tokenPrecondition === (c.elevated ? 'unsupported_elevated' :
    ((c.integrityRID as number) >= 0x3000 ? 'unsupported_high_integrity' : 'supported_non_elevated')), 'false probe readiness');
  evidence.outcome = c.elevated === false && c.integrityRID === 0x2000 && r.enabledAdmins === false
    ? 'supported_token_prerequisite' : 'unsupported_token_prerequisite';
}
async function main(): Promise<void> {
  try { await run(); } catch (error) { evidence.failure = String(error).slice(0, 1024); process.exitCode = 1; }
  finally {
    evidence.durationMs = performance.now() - started;
    const body = JSON.stringify(evidence, null, 2);
    check(Buffer.byteLength(body) <= 262144, 'bounded evidence required');
    const fd = openSync(join(root, 'lua-token-evidence.json'), 'wx');
    try { writeFileSync(fd, body); fsyncSync(fd); } finally { closeSync(fd); }
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${root}\n`, { flag: 'a' });
  }
}
void main().catch((error: unknown) => { console.error(String(error).slice(0, 1024)); process.exitCode = 1; });
