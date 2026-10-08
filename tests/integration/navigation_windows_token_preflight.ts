// TEST-only: one own-process measurement, never SDK/bootstrap/notification qualification.
import { spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { closeSync, constants, copyFileSync, fsyncSync, mkdtempSync, openSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const started = performance.now();
const nonce = randomUUID();
if (!process.env.RUNNER_TEMP) throw new Error('RUNNER_TEMP required for own TEST evidence');
const root = mkdtempSync(join(process.env.RUNNER_TEMP, 'TEST-token-preflight-'));
const evidence: Record<string, unknown> = { nonce, sourceSHA: process.env.SOURCE_SHA,
  executedTokenProbe: false, sdkNativeCallback: false, nativeEffects: 0, outcome: 'unknown' };
const hash = (file: string): string => createHash('sha256').update(readFileSync(file)).digest('hex');
const hex = (value: unknown): boolean => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
const integer = (value: unknown, max = 0xffffffff): value is number =>
  typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= max;
function check(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }

async function run(): Promise<void> {
  check(process.platform === 'win32' && process.arch === 'arm64', 'Windows ARM64 required');
  check(process.env.RUNNER_TEMP && /^[a-f0-9]{40}$/.test(process.env.SOURCE_SHA ?? ''), 'trusted source SHA required');
  check(process.env.TOKEN_PROBE_EXE && hex(process.env.TOKEN_PROBE_SHA256), 'compiled binary pin required');
  const original = resolve(process.env.TOKEN_PROBE_EXE);
  const binary = join(root, `TEST-token-${nonce}.exe`);
  check(hash(original) === process.env.TOKEN_PROBE_SHA256, 'original binary pin mismatch');
  copyFileSync(original, binary, constants.COPYFILE_EXCL);
  evidence.binarySHA256 = hash(binary);
  check(evidence.binarySHA256 === process.env.TOKEN_PROBE_SHA256, 'copied binary pin mismatch');
  let stdout = '';
  let stderr = '';
  let outputBytes = 0;
  let timedOut = false;
  let overflow = false;
  let collected = false;
  let spawnError: string | undefined;
  const child = spawn(binary, ['--read-only-TEST-token-preflight', nonce],
    { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  evidence.executedTokenProbe = child.pid !== undefined;
  evidence.pid = child.pid;
  const capture = (chunk: Buffer, stream: 'stdout' | 'stderr'): void => {
    const available = Math.max(0, 8192 - outputBytes);
    const text = chunk.subarray(0, available).toString('utf8');
    if (stream === 'stdout') stdout += text; else stderr += text;
    outputBytes += chunk.length;
    if (outputBytes > 8192 && !overflow) { overflow = true; child.kill(); }
  };
  child.stdout.on('data', (chunk: Buffer) => capture(chunk, 'stdout'));
  child.stderr.on('data', (chunk: Buffer) => capture(chunk, 'stderr'));
  const result = await new Promise<{ code: number | null; signal: string | null }>((done) => {
    const timeout = setTimeout(() => { timedOut = true; child.kill(); }, 12000);
    const deadline = setTimeout(() => {
      timedOut = true; child.kill(); child.stdout.destroy(); child.stderr.destroy(); child.unref();
      clearTimeout(timeout); done({ code: null, signal: 'collection_deadline' });
    }, 15000);
    child.once('error', (error) => { spawnError = error.message.slice(0, 512); });
    child.once('close', (code, signal) => {
      collected = true; clearTimeout(timeout); clearTimeout(deadline); done({ code, signal });
    });
  });
  Object.assign(evidence, { stdout, stderr, exitCode: result.code, signal: result.signal,
    collected, timedOut, overflow, spawnError, durationMs: performance.now() - started });
  check(collected && !timedOut && !overflow && !spawnError, 'probe collection failed; no retry');
  check(hash(binary) === evidence.binarySHA256 && hash(original) === evidence.binarySHA256, 'post-run binary pin mismatch');
  check(performance.now() - started < 30000, 'overall deadline exceeded');
  check(stderr === '', 'unexpected stderr');
  const lines = stdout.trim().split(/\r?\n/);
  check(lines.length === 1, 'one JSON record required');
  const value: unknown = JSON.parse(lines[0]!);
  check(value !== null && typeof value === 'object' && !Array.isArray(value), 'record required');
  const record = value as Record<string, unknown>;
  evidence.record = record;
  check(record.nonce === nonce && record.pid === child.pid && integer(record.pid, 0xffffffff) && record.pid > 0,
    'nonce or actual child PID mismatch');
  check(record.nativeEffects === 0 && typeof record.queriesComplete === 'boolean', 'invalid query/effect schema');
  if (!record.queriesComplete) {
    check(record.tokenPrecondition === 'unknown' && result.code !== 0 && typeof record.query === 'string' &&
      integer(record.error), 'invalid failure schema');
    throw new Error('token query incomplete; prerequisite unknown');
  }
  const keys = ['nonce', 'pid', 'session', 'elevated', 'elevationType', 'integrityRID', 'sidSHA256',
    'authLUIDSHA256', 'wtsState', 'queriesComplete', 'tokenPrecondition', 'nativeEffects'];
  check(Object.keys(record).sort().join() === keys.sort().join(), 'unexpected success schema');
  check(result.code === 0 && result.signal === null && integer(record.session) && typeof record.elevated === 'boolean' &&
    integer(record.elevationType, 3) && record.elevationType >= 1 && integer(record.integrityRID) &&
    hex(record.sidSHA256) && hex(record.authLUIDSHA256) && integer(record.wtsState, 9), 'invalid complete query values');
  const precondition = record.elevated ? 'unsupported_elevated' :
    (record.integrityRID >= 0x3000 ? 'unsupported_high_integrity' : 'supported_non_elevated');
  check(record.tokenPrecondition === precondition, 'false token readiness');
  check(!(record.elevationType === 2 && !record.elevated) && !(record.elevationType === 3 && record.elevated) &&
    (!record.elevated || record.integrityRID >= 0x3000), 'inconsistent elevation/integrity');
  evidence.outcome = record.tokenPrecondition;
}

async function main(): Promise<void> {
  try { await run(); } catch (error) {
    evidence.failure = (error instanceof Error ? error.message : String(error)).slice(0, 1024);
    process.exitCode = 1;
  } finally {
    evidence.durationMs = performance.now() - started;
    const body = JSON.stringify(evidence, null, 2);
    check(Buffer.byteLength(body) < 131072, 'evidence bound exceeded');
    const fd = openSync(join(root, 'token-evidence.json'), 'wx');
    try { writeFileSync(fd, body); fsyncSync(fd); } finally { closeSync(fd); }
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${root}\n`, { flag: 'a' });
  }
}
void main().catch((error: unknown) => { console.error(String(error).slice(0, 1024)); process.exitCode = 1; });
