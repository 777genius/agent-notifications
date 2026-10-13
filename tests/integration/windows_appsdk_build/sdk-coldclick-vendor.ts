// Reuse the existing fixed TEST vendor actors, without their legacy notification registration.
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { closeSync, existsSync, fstatSync, fsyncSync, lstatSync, openSync, readSync, realpathSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { hash } from '../navigation_windows_vendor_acquisition.ts';
type Json = Record<string, unknown>;
const expected = 'OpenAI.Codex_26.930.7945.0_arm64__2p2nqsd0c76g0';
const rawPin = 'a208d373c7c84aa3e0452cd3dd8406a6794d8139ec1260c64a770c2a00fbeeb8';
const started = performance.now(), root = resolve(process.env.SDK_VENDOR_METADATA_ROOT!);
const receipt: Json = { sourceSHA: process.env.SOURCE_SHA, outcome: 'unknown', actors: [], archiveSHA256: rawPin,
  fullName: expected, signatureVerified: false, removeAttempted: false, launchAttempted: false, registrationAttempted: false, retryAllowed: false };
function check(ok: unknown, why: string): asserts ok { if (!ok) throw new Error(why); }
function read(leaf: string): string {
  const path = join(root, leaf), s = lstatSync(path); check(s.isFile() && !s.isSymbolicLink() && s.nlink === 1 && realpathSync(path) === path, 'owned public input');
  const fd = openSync(path, 'r'); try { check(fstatSync(fd).size <= 65536, 'input cap'); const b = Buffer.alloc(65537); let n = 0, got: number;
    while (n < b.length && (got = readSync(fd, b, n, b.length - n, n)) > 0) n += got;
    check(n <= 65536 && n === s.size && fstatSync(fd).size === n, 'input changed/cap'); return b.subarray(0, n).toString('utf8');
  } finally { closeSync(fd); }
}
function json(leaf: string): Json { return JSON.parse(read(leaf)) as Json; }
async function actor(mode: 'vendor-state-before' | 'vendor-install' | 'vendor-state-after', nonce: string, binary: string, limit: number): Promise<number> {
  check(performance.now() - started + limit < 210000, 'finite vendor stage admission');
  const observation: Json = { mode, collected: false, stdout: '', stderr: '' }; (receipt.actors as Json[]).push(observation);
  const child = spawn(binary, [mode, root, nonce], { cwd: root, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
    env: { SystemRoot: process.env.SystemRoot, TEMP: root, TMP: root, CI: 'true', GITHUB_ACTIONS: 'true', NAVIGATION_WINDOWS_VENDOR_NATIVE_TEST: '1' } });
  observation.pid = child.pid; let count = 0;
  const capture = (b: Buffer, key: 'stdout' | 'stderr'): void => {
    const saved = b.subarray(0, Math.max(0, 16384 - count)).toString('utf8'); count += b.length; observation[key] = String(observation[key]) + saved;
    if (count > 16384) { observation.overflow = true; child.kill(); }
  };
  child.stdout.on('data', (b: Buffer) => capture(b, 'stdout')); child.stderr.on('data', (b: Buffer) => capture(b, 'stderr'));
  await new Promise<void>(done => {
    const timeout = setTimeout(() => { observation.timedOut = true; child.kill(); }, limit);
    const deadline = setTimeout(() => { child.kill(); child.stdout.destroy(); child.stderr.destroy(); child.unref(); clearTimeout(timeout); done(); }, limit + 3000);
    child.once('error', e => { observation.error = e.message.slice(0, 512); });
    child.once('close', (code, signal) => { Object.assign(observation, { collected: true, code, signal }); clearTimeout(timeout); clearTimeout(deadline); done(); });
  });
  check(observation.collected === true && observation.code === 0 && observation.signal === null && !observation.timedOut && !observation.overflow && !observation.error, 'vendor outcome unknown; no retry');
  return child.pid!;
}
try {
  check(process.platform === 'win32' && process.arch === 'arm64' && process.env.GITHUB_REPOSITORY === '777genius/agent-notifications' && process.env.GITHUB_EVENT_NAME === 'workflow_dispatch' &&
    process.env.GITHUB_RUN_ATTEMPT === '1', 'one own CI experiment');
  const evidence = json('vendor-evidence.json'), metadata = evidence.metadata as Json, nonce = evidence.nonce as string;
  check(evidence.sourceSHA === process.env.SOURCE_SHA && evidence.status === 'metadata_inspected' && evidence.signatureVerified === true &&
    evidence.packageSHA256 === rawPin && metadata.fullName === expected && metadata.familyName === 'OpenAI.Codex_2p2nqsd0c76g0' && metadata.architecture === 12 &&
    /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(nonce), 'same-source fixed signed vendor metadata');
  receipt.signatureVerified = true; receipt.metadata = metadata;
  const binary = join(root, 'navigation-native-probe.exe'), pin = evidence.binarySHA256;
  check(await hash(binary) === pin && await hash(join(root, 'client.msix')) === rawPin, 'retained signature custody');
  const custody = `TEST vendor custody ${nonce}\nsigntool-pa-all-success\n${rawPin}\n${pin}\n`;
  writeFileSync(join(root, 'vendor-custody.proof'), custody, { flag: 'wx', flush: true });
  const beforePID = await actor('vendor-state-before', nonce, binary, 15000), before = json('vendor-before.json'); receipt.before = before;
  check(before.pid === beforePID && before.nonce === nonce && before.installedCount === 0 && before.readOnly === true && before.exactFullName === false, 'absent own TEST vendor required');
  const proof = read('vendor-absent.proof').split('\n'); check(proof.length === 3 && proof[0] === nonce && /^(?:[a-f0-9]{2}){8,68}$/.test(proof[1]!), 'existing actor absence custody');
  const intent = custody + `absent-before-install\n${proof[1]}\n`;
  const addPID = await actor('vendor-install', nonce, binary, 165000), added = json('vendor-install-result.json'); receipt.add = added;
  check(added.pid === addPID && added.nonce === nonce && added.operationCompleted === true && added.extendedError === 0 &&
    read('vendor-install-intent.proof') === intent && read('vendor-install-completed.proof') === intent, 'actual exact S_OK vendor Add completion');
  const afterPID = await actor('vendor-state-after', nonce, binary, 15000), after = json('vendor-after.json'); receipt.after = after;
  check(after.pid === afterPID && after.nonce === nonce && after.installedCount === 1 && after.exactFullName === true && after.fullName === expected &&
    await hash(binary) === pin && await hash(join(root, 'client.msix')) === rawPin, 'exact current user vendor readback/custody');
  receipt.outcome = 'exact_current_user_installed';
} catch (e) { receipt.error = String(e).slice(0, 2048); process.exitCode = 1; }
receipt.durablePhases = ['vendor-install-intent.proof', 'vendor-install-completed.proof'].filter(leaf => existsSync(join(root, leaf))).map(leaf => ({ file: leaf, sha256: createHash('sha256').update(read(leaf)).digest('hex') }));
const path = join(root, 'sdk-coldclick-vendor.json'), body = JSON.stringify(receipt); check(Buffer.byteLength(body) <= 65536, 'bounded vendor evidence');
const fd = openSync(path, 'wx'); try { writeFileSync(fd, body); fsyncSync(fd); } finally { closeSync(fd); }
if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_file=${path}\n`, { flag: 'a', flush: true });
