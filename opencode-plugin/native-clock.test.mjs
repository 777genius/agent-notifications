import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, renameSync, rmSync } from 'node:fs';
import { resolve } from 'node:path';
import { address, closeAll, createNativeClock, darwinABI, darwinBoot, filetimeNS,
  interruptABI, interruptNS, machNS, uint64, windowsBoot, windowsKernelABI, windowsNtABI } from './native-clock-contract.mjs';
import { parseUptime } from './linux-clock.mjs';
import { selectClockCell } from './clock-cells.mjs';

const MAX = 9223372036854775807n, U64 = 18446744073709551615n;
const boot = '00112233-4455-6677-8899-aabbccddeeff';
const changedBoot = '11223344-4455-6677-8899-aabbccddeeff';
const denied = fn => assert.throws(fn, { name: 'TypeError', message: 'clock_unavailable' });

test('actual LP64/Win64 ABI contracts use output pointers, unsigned counters and VOID status', () => {
  assert.deepEqual(darwinABI, {
    mach_continuous_time: { returns: 'u64', args: [] },
    mach_timebase_info: { returns: 'i32', args: ['ptr'] },
    sysctlbyname: { returns: 'i32', args: ['ptr', 'ptr', 'ptr', 'ptr', 'u64'] },
  });
  assert.deepEqual(windowsKernelABI, {
    GetSystemDirectoryW: { returns: 'u32', args: ['ptr', 'u32'] },
    LoadLibraryExW: { returns: 'u64', args: ['ptr', 'u64', 'u32'] },
    GetProcAddress: { returns: 'ptr', args: ['u64', 'ptr'] },
    FreeLibrary: { returns: 'i32', args: ['u64'] },
    GetSystemTimePreciseAsFileTime: { returns: 'void', args: ['ptr'] },
  });
  assert.deepEqual(windowsNtABI, { NtQuerySystemInformation: { returns: 'i32', args: ['u32', 'ptr', 'u32', 'ptr'] } });
  assert.deepEqual(interruptABI, { returns: 'void', args: ['ptr'] });
  denied(() => address(0)); denied(() => address(4097, 8)); denied(() => address(2 ** 53));
  assert.equal(address(4096, 8), 4096);
  assert.throws(() => { interruptABI.returns = 'u64'; });
  assert.throws(() => { windowsKernelABI.LoadLibraryExW.args[1] = 'u32'; });
});

test('unsigned FFI conversion rejects lossy numbers and native range errors', () => {
  assert.equal(uint64(9007199254740991), 9007199254740991n);
  assert.equal(uint64(U64), U64);
  for (const v of [9007199254740992, -1, -1n, U64 + 1n, 1.5, NaN, Infinity, '1', null]) denied(() => uint64(v));
  assert.equal(interruptNS(12345678901234567n), 1234567890123456700n);
  assert.equal(interruptNS(92233720368547758n), 9223372036854775800n);
  for (const v of [0n, 92233720368547759n, U64]) denied(() => interruptNS(v));
});

test('Mach conversion matches integer Libc multiply/divide and refuses wrap before division', () => {
  assert.equal(machNS(24000001n, 125, 3), 1000000041n);
  assert.equal(machNS(9007199254740993n, 1, 1), 9007199254740993n);
  assert.equal(machNS(MAX, 1, 1), MAX);
  for (const args of [[0n, 1, 1], [1n, 0, 1], [1n, 1, 0], [1n, 1.5, 1],
    [1n, 2 ** 32, 1], [U64, 2, 2], [MAX + 1n, 1, 1], [1n, 1, 2]]) denied(() => machNS(...args));
});

test('FILETIME epoch, unsigned 100ns and int64 edges use independent Go vectors', () => {
  assert.equal(filetimeNS(116444736000000001n), 100n);
  assert.equal(filetimeNS(133444736001234567n), 1700000000123456700n);
  assert.equal(filetimeNS(208678456368547758n), 9223372036854775800n);
  for (const v of [0n, 116444735999999999n, 116444736000000000n, 208678456368547759n, U64]) denied(() => filetimeNS(v));
});

test('Darwin sysctl exact status/size/NUL ASCII UUID, never kern.boottime wall identity', () => {
  const bytes = Buffer.from('00112233-4455-6677-8899-AABBCCDDEEFF\0');
  assert.equal(darwinBoot(bytes, 37n, 0), boot);
  for (const [length, status] of [[36n, 0], [38n, 0], [37, 0], [37n, -1]]) denied(() => darwinBoot(bytes, length, status));
  for (const data of [Buffer.from('00000000-0000-0000-0000-000000000000\0'),
    Buffer.from(boot + '\n'), Buffer.from(boot.slice(0, -1) + '\xff\0'), Buffer.alloc(36)]) denied(() => darwinBoot(data, 37n, 0));
});

test('boot-environment GUID SDK byte order and complete 32-byte NT result', () => {
  // Literal GUID structure: DWORD/WORD/WORD little endian; eight tail bytes.
  const bytes = Buffer.from('33221100554477668899aabbccddeeff02000000000000000100000000000000', 'hex');
  assert.equal(windowsBoot(bytes, 32, 0), boot);
  for (const [len, status] of [[16, 0], [31, 0], [33, 0], [32, 1], [32, -1073741820]]) denied(() => windowsBoot(bytes, len, status));
  denied(() => windowsBoot(bytes.subarray(0, 16), 32, 0));
  denied(() => windowsBoot(Buffer.alloc(32), 32, 0));
});

// Literal read scripts model the independent library port. No fake FFI or
// replacement implementation computes the expected intervals/conversions.
function script({ ticks = [1000n, 1001n], walls = [1700000000000000000n], boots = [boot, boot, boot], verifies } = {}) {
  const calls = [];
  let ti = 0, wi = 0, bi = 0, vi = 0, closes = 0;
  const port = {
    readCounter() { calls.push('counter'); return ticks[ti++]; },
    readWall() { calls.push('wall'); return walls[wi++]; },
    readBoot() { calls.push('boot'); return boots[bi++]; },
    verify() { calls.push('verify'); if (verifies?.[vi++] === false) throw Error('revoked'); },
    close() { calls.push('close'); closes++; },
  };
  return { port, calls, closes: () => closes };
}

test('wall is bracketed by two native reads with fixed containing quantum and no Q/T grant', () => {
  for (const [domain, kind, hi] of [['darwin-kernel', 'darwin-monotonic-raw', 1002n],
    ['windows-kernel', 'windows-interrupt-precise', 1101n]]) {
    const s = script(), c = createNativeClock(s.port, domain);
    assert.deepEqual(c.sample(), { boot, domain, rawKind: kind, loNS: 1000n, hiNS: hi, wallNS: 1700000000000000000n });
    assert.deepEqual(s.calls, ['verify', 'boot', 'verify', 'boot', 'counter', 'wall', 'counter', 'boot', 'verify']);
    c.dispose(); c.dispose();
    denied(c.sample); assert.equal(s.closes(), 1);
  }
});

test('boot mismatch before/after read, unknown domain, proof loss and invalid numeric ranges revoke forever', () => {
  for (const opts of [{ boots: [boot, changedBoot] }, { boots: [boot, boot, changedBoot] },
    { ticks: [1001n, 1000n] }, { ticks: [0n, 1000n] }, { ticks: [1000, 1001n] },
    { ticks: [1000n, 100001000n] }, { ticks: [MAX - 100n, MAX] },
    { walls: [0n] }, { walls: [-1n] }, { walls: [MAX + 1n] },
    { verifies: [true, false] }, { verifies: [true, true, false] }]) {
    const s = script(opts), c = createNativeClock(s.port, 'windows-kernel');
    denied(c.sample); const calls = s.calls.length;
    denied(c.sample); c.dispose();
    assert.equal(s.closes(), 1); assert.equal(s.calls.length, calls);
  }
  for (const [opts, domain] of [[{ boots: ['bad-boot'] }, 'darwin-kernel'],
    [{ verifies: [false] }, 'darwin-kernel'], [{}, 'process-origin']]) {
    const s = script(opts); denied(() => createNativeClock(s.port, domain)); assert.equal(s.closes(), 1);
  }
});

test('cross-sample counter and wall nonregression; exact width ceiling includes quantum', () => {
  for (const opts of [{ ticks: [1000n, 2000n, 1999n, 2001n] },
    { ticks: [1000n, 2000n, 2000n, 2001n], walls: [1700000000000000000n, 1699999999999999999n] }]) {
    const s = script({ boots: [boot, boot, boot, boot, boot], ...opts });
    const c = createNativeClock(s.port, 'darwin-kernel'); c.sample(); denied(c.sample); assert.equal(s.closes(), 1);
  }
  const s = script({ ticks: [1000n, 100000900n] });
  const c = createNativeClock(s.port, 'windows-kernel');
  const a = c.sample(); assert.equal(a.hiNS - a.loNS, 100000000n); assert.ok(Object.isFrozen(a)); c.dispose();
});

test('reverse disposal attempts every owned handle even if one fails, without double release', () => {
  const closed = [], releases = [() => closed.push('image'), () => { closed.push('dll'); throw Error(); },
    () => closed.push('api-set'), () => closed.push('wrapper')];
  denied(() => closeAll(releases));
  assert.deepEqual(closed, ['wrapper', 'api-set', 'dll', 'image']);
  closeAll(releases); assert.equal(closed.length, 4);
  const s = script(); s.port.close = () => { throw Error(); };
  const c = createNativeClock(s.port, 'darwin-kernel'); assert.throws(c.dispose); denied(c.sample); c.dispose();
});

test('Linux arm64 shares official proc floor grammar; nearest int64 overflow is refused purely', () => {
  assert.equal(parseUptime('1234567890.12 456.00\n'), 1234567890120000000n);
  assert.equal(parseUptime('9223372036.85 0.00\n'), 9223372036850000000n);
  for (const text of ['1.0 0.00\n', '01.00 0.00\n', '1.00 0.00', '-1.00 0.00\n',
    '9223372036.86 0.00\n', '1.00 0.00\nextra']) assert.throws(() => parseUptime(text));
});

test('portable Node imports stay inert; absent native platform/image proof denies, cells remain closed', async () => {
  const { createDarwinClock } = await import('./darwin-clock.mjs');
  const { createWindowsClock } = await import('./windows-clock.mjs');
  const { pinNativeImage } = await import('./native-clock-image.mjs');
  const { createPlatformClock } = await import('./platform-clock.mjs');
  assert.equal(typeof createPlatformClock, 'function');
  denied(pinNativeImage);
  await assert.rejects(createDarwinClock(), { message: 'clock_unavailable' });
  await assert.rejects(createWindowsClock(), { message: 'clock_unavailable' });
  assert.equal(selectClockCell('v1'), undefined); assert.equal(selectClockCell('v2'), undefined);
});

test('held source image proof binds exact file bytes and refuses replacement or use after release', async () => {
  // All writes stay in the runner's private TEST fixture; no executing image is touched.
  assert.ok(process.cwd().split(/[\\/]/).some(part => part.startsWith('TEST-')));
  const dir = mkdtempSync(resolve('TEST-held-image-'));
  const { holdFile } = await import('./native-clock-image.mjs');
  const path = resolve(dir, 'image'), replacement = resolve(dir, 'replacement');
  let held;
  try {
    writeFileSync(path, 'abc', { flag: 'wx', mode: 0o600 });
    held = holdFile(path);
    assert.equal(held.digest, 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
    held.verify();
    writeFileSync(replacement, 'abc', { flag: 'wx', mode: 0o600 });
    renameSync(replacement, path);
    denied(held.verify);
    held.close(); held.close(); denied(held.verify);
  } finally { held?.close(); rmSync(dir, { recursive: true }); }
});
