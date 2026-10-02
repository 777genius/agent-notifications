import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, renameSync, rmSync } from 'node:fs';
import { resolve } from 'node:path';
import { address, closeAll, createNativeClock, darwinABI, darwinBoot, filetimeNS,
  interruptABI, interruptNS, machNS, uint64, withWallOffset, windowsBoot, windowsKernelABI, windowsNtABI } from './native-clock-contract.mjs';
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
  const selected = [selectClockCell('v1'), selectClockCell('v2')];
  const { createDarwinClock } = await import('./darwin-clock.mjs');
  const { createWindowsClock } = await import('./windows-clock.mjs');
  const { pinNativeImage } = await import('./native-clock-image.mjs');
  const { createPlatformClock } = await import('./platform-clock.mjs');
  assert.equal(typeof createPlatformClock, 'function');
  denied(pinNativeImage);
  await assert.rejects(createDarwinClock(), { message: 'clock_unavailable' });
  await assert.rejects(createWindowsClock(), { message: 'clock_unavailable' });
  assert.deepEqual([selectClockCell('v1'), selectClockCell('v2')], selected);
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

// Baseline denies native receipts/frames and async preparation never becomes
// ready. These inert boundaries exercise production lifecycle arithmetic only;
// their literal policies/images are test data and grant no native qualification.
import { createPreparedDelivery } from './prepared-delivery.mjs';
import { describeClockPolicy, describeClockSource } from './clock-cells.mjs';
import { clockReceipt, domainOK, parseJSON } from './protocol.mjs';

const deferred = () => {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return { promise, resolve };
};
function deliveryFixture(rawKind = 'darwin-monotonic-raw', generation = 'v2', factory) {
  const domain = rawKind === 'linux-boottime' ? 'linux-time:4:7' :
    rawKind === 'darwin-monotonic-raw' ? 'darwin-kernel' : 'windows-kernel';
  const quantum = rawKind === 'linux-boottime' ? 10000000n : rawKind === 'darwin-monotonic-raw' ? 1n : 100n;
  const initial = rawKind === 'linux-boottime' ? 1000000000000n :
    rawKind === 'darwin-monotonic-raw' ? 1000000000007n : 1000000000100n;
  const wallBase = 1700000000000000000n;
  let tick = initial, closes = 0, cancels = 0, events = 0, frame, mutate, receiptMutation, helperGate, queued, spawnGap = 0n;
  const calls = [], helpers = [];
  const source = { imageSHA256: 'a'.repeat(64), sample() {
    calls.push('sample');
    const wallNS = wallBase + tick - initial;
    const value = { boot, domain, rawKind, loNS: tick, hiNS: tick + quantum, wallNS };
    if (rawKind === 'linux-boottime') Object.assign(value,
      { offsetLoNS: wallNS - value.hiNS - 2000000n, offsetHiNS: wallNS - tick + 2000000n });
    return mutate ? mutate(value) : value;
  }, dispose() { closes++; calls.push('dispose'); } };
  const registry = {
    async clock(options) {
      calls.push('helper'); helpers.push(options);
      const receipt = { protocol: 1, boot, clockDomain: domain, clockKind: rawKind,
        monoLoNs: String(tick + 1000000n), monoHiNs: String(tick + 2000000n),
        wallUnixNs: String(wallBase + tick - initial), uncertaintyNs: '4000000' };
      const output = Buffer.from(JSON.stringify(receiptMutation ? receiptMutation(receipt) : receipt));
      tick += 10000000n;
      if (helperGate) await helperGate.promise;
      return { status: 'ok', output };
    },
    event(options) {
      events++; calls.push('admission');
      const finish = () => { const afterSpawn = options.prepare(); calls.push('spawn'); tick += spawnGap; frame = afterSpawn(); return Promise.resolve(); };
      if (queued) { queued.options = options; queued.finish = finish; return queued.promise; }
      return finish();
    },
    cancel() { cancels++; calls.push('cancel'); },
  };
  const policy = { generation, images: [{ imageSHA256: 'a'.repeat(64) }], profileID: 'TEST-inert-policy', calibrationID: 'TEST-inert-calibration', rawKind,
    sourceKind: rawKind === 'linux-boottime' ? 'linux-proc-boottime' : rawKind === 'darwin-monotonic-raw' ?
      'darwin-mach-continuous' : 'windows-interrupt-precise', sourceWallBoundNS: 2000000n,
    nativeReadBoundNS: 103000000n, comparisonBoundNS: 430000000n, translationBoundNS: 224000000n };
  const delivery = createPreparedDelivery({ registry, origin: 'a'.repeat(64), policy, isOwned: () => true,
    sourceFactory: factory ? options => factory(options, source) : () => source });
  return { delivery, source, calls, helpers, advance(n) { tick += n; }, tick: () => tick,
    closes: () => closes, cancels: () => cancels, events: () => events, frame: () => parseJSON(frame),
    mutate(fn) { mutate = fn; }, receipt(fn) { receiptMutation = fn; }, gap(n) { spawnGap = n; }, gateHelper(gate) { helperGate = gate; }, queue(gate) { queued = gate; },
    fact() { return { version: 1, kind: 'question_asked', sessionID: 's', turnID: 'original-turn', requestID: 'r', rootSession: true,
      provenance: { generation, observationID: 'original-observation', nativeTime: Number((wallBase + tick - initial) / 1000000n),
        timeBasis: generation === 'v1' ? 'assistant_created_lower_bound' : 'envelope_created',
        ...(generation === 'v2' ? { nativeEventID: 'original-native-event' } : {}) } }; },
    handoff() {
      delivery.beginIngress({ type: 'question.asked' });
      const ms = delivery.clock.now(), controller = new AbortController();
      return { clockID: delivery.clock.id, ingressMonotonicMs: ms, metadataDeadline: ms + 2000,
        signal: controller.signal, isCurrent: () => !controller.signal.aborted, controller };
    },
  };
}

test('async preparation finishes before helper and synchronous admission; exact native ingress/birth/anchor survive', async () => {
  for (const rawKind of ['linux-boottime', 'darwin-monotonic-raw', 'windows-interrupt-precise']) {
    for (const generation of ['v1', 'v2']) {
      const gate = deferred();
      const f = deliveryFixture(rawKind, generation, async (options, source) => {
        f.calls.push('preparing'); assert.equal(options.signal.aborted, false);
        await gate.promise; f.calls.push('prepared'); return source;
      });
      try {
        const activation = f.delivery.activate();
        assert.equal(f.delivery.ready(), false); assert.equal(f.helpers.length, 0);
        gate.resolve(); assert.equal(await activation, true);
        assert.ok(f.calls.indexOf('prepared') < f.calls.indexOf('sample'));
        assert.ok(f.calls.indexOf('sample') < f.calls.indexOf('helper'));
        f.advance(60000000n); const ingress = f.tick(), event = f.fact(), handoff = f.handoff();
        assert.equal(await f.delivery.beforeEmit(event, handoff), true);
        const n = f.calls.length, emitted = f.delivery.emit(event, handoff);
        // The entire seam completed on this stack before awaiting its result.
        assert.deepEqual(f.calls.slice(n), ['admission', 'sample', 'spawn', 'sample']);
        const frame = f.frame();
        assert.deepEqual(frame.event, event);
        assert.equal(frame.provenance.ingressTickNS, String(ingress));
        assert.equal(frame.provenance.anchor.monoLoNS, rawKind === 'linux-boottime' ? '1000001000000' :
          rawKind === 'darwin-monotonic-raw' ? '1000001000007' : '1000001000100');
        assert.equal(frame.provenance.calibration.nativeLoNS, frame.provenance.anchor.monoLoNS);
        assert.equal(frame.provenance.sourceEpoch, frame.provenance.calibration.sourceEpoch);
        await emitted;
      } finally { f.delivery.dispose(); }
      assert.equal(f.closes(), 1);
    }
  }
});

test('cancelled async preparation disposes late source and cannot retire or revive replacement epoch', async () => {
  const gate = deferred(); let first = true, signal, lateCloses = 0;
  const f = deliveryFixture('darwin-monotonic-raw', 'v2', (options, source) => {
    if (!first) return source;
    first = false; signal = options.signal;
    return gate.promise;
  });
  try {
    const old = f.delivery.activate(); f.delivery.invalidate('reader');
    assert.equal(signal.aborted, true); assert.equal(await old, false); assert.equal(f.cancels(), 1);
    assert.equal(await f.delivery.activate(), true);
    gate.resolve({ sample() { assert.fail('late source sampled'); }, dispose() { lateCloses++; } });
    await Promise.resolve(); await Promise.resolve();
    assert.equal(lateCloses, 1); assert.equal(f.delivery.ready(), true); assert.equal(f.helpers.length, 1);
  } finally { f.delivery.dispose(); }
  assert.equal(f.closes(), 1);
});

test('helper completion from retired epoch is cancelled and cannot publish a new epoch', async () => {
  const f = deliveryFixture(), gate = deferred(); f.gateHelper(gate);
  try {
    const old = f.delivery.activate(); await Promise.resolve(); await Promise.resolve();
    assert.equal(f.helpers.length, 1); const options = f.helpers[0];
    f.delivery.invalidate('reader'); assert.equal(options.signal.aborted, true); assert.equal(options.isCurrent(), false);
    f.gateHelper(undefined); assert.equal(await f.delivery.activate(), true);
    gate.resolve(); assert.equal(await old, false); assert.equal(f.delivery.ready(), true);
  } finally { f.delivery.dispose(); }
  // Each accepted preparation is disposed, including the retired activation.
  assert.equal(f.closes(), 2);
});

test('old prepared work cannot survive reader epoch replacement or handoff cancellation', async () => {
  for (const mode of ['epoch', 'signal']) {
    const f = deliveryFixture();
    try {
      assert.equal(await f.delivery.activate(), true); f.advance(60000000n);
      const event = f.fact(), handoff = f.handoff(); assert.equal(await f.delivery.beforeEmit(event, handoff), true);
      if (mode === 'epoch') { f.delivery.invalidate('reader'); assert.equal(await f.delivery.activate(), true); }
      else handoff.controller.abort();
      await f.delivery.emit(event, handoff); assert.equal(f.events(), 0);
    } finally { f.delivery.dispose(); }
  }
});

test('malformed source identities/intervals fail closed and dispose on activation', async () => {
  for (const change of [v => ({ ...v, boot: 'bad' }), v => ({ ...v, domain: 'windows-kernel' }),
    v => ({ ...v, rawKind: 'windows-interrupt-precise' }), v => ({ ...v, rawKind: 'unknown' }),
    v => ({ ...v, loNS: Number(v.loNS) }), v => ({ ...v, hiNS: v.loNS }),
    v => ({ ...v, hiNS: v.loNS + 100000001n }), v => ({ ...v, wallNS: 0n })]) {
    const f = deliveryFixture(); f.mutate(change);
    assert.equal(await f.delivery.activate(), false); assert.equal(f.delivery.ready(), false);
    assert.equal(f.helpers.length, 0); assert.equal(f.closes(), 1); assert.equal(f.cancels(), 1);
    f.delivery.dispose(); assert.equal(f.closes(), 1);
  }
});

test('closed platform policy descriptions cannot populate qualification; no implicit native source-wall premise', () => {
  for (const [goos, goarch] of [['linux', 'arm64'], ['darwin', 'arm64'], ['darwin', 'amd64'], ['windows', 'amd64']]) {
    const descriptor = describeClockSource(goos, goarch);
    for (const generation of ['v1', 'v2']) {
      const row = { protocol: 1, goos, goarch, generation, ...descriptor,
        images: [{ version: generation === 'v1' ? '1.18.33' : '2.0.21', imageSHA256: 'a'.repeat(64) }],
        algorithmSourceMerkleSHA256: 'd'.repeat(64), nativeReadBoundNS: '103000000',
        comparisonBoundNS: '430000000', translationBoundNS: '224000000',
        ...(goos === 'linux' ? {} : { sourceWallBoundNS: '2000000' }) };
      const selected = selectClockCell(generation);
      const policy = describeClockPolicy(row); assert.equal(policy.rawKind, descriptor.rawKind);
      assert.ok(Object.isFrozen(policy)); assert.equal(selectClockCell(generation), selected);
      for (const change of [r => { r.rawKind = 'unknown'; }, r => { r.sourceKind = 'wrong'; },
        r => { r.images[0].imageSHA256 = 'bad'; }, r => { r.images[0].version = '2.0.22'; },
        r => { r.goarch = '386'; }, r => { r.comparisonBoundNS = '2000000001'; }]) {
        const bad = structuredClone(row); change(bad); assert.throws(() => describeClockPolicy(bad));
      }
      if (goos !== 'linux') {
        const missing = { ...row }; delete missing.sourceWallBoundNS;
        assert.throws(() => describeClockPolicy(missing));
      }
    }
  }
  for (const pair of [['windows', 'arm64'], ['linux', 'x64'], ['freebsd', 'amd64']])
    assert.throws(() => describeClockSource(...pair));
  // Legacy amd64 image matching remains exact; a data description cannot grant it.
  const row = { protocol: 1, goos: 'linux', goarch: 'amd64', generation: 'v2',
    ...describeClockSource('linux', 'amd64'), images: [{ version: '2.0.21', imageSHA256: 'a'.repeat(64) }],
    algorithmSourceMerkleSHA256: 'd'.repeat(64), nativeReadBoundNS: '103000000',
    comparisonBoundNS: '430000000', translationBoundNS: '224000000' };
  assert.throws(() => describeClockPolicy(row));
});

test('native receipt domain/raw-kind pairing is closed and interval uncertainty remains exact', () => {
  for (const [kind, domain] of [['darwin-monotonic-raw', 'darwin-kernel'], ['windows-interrupt-precise', 'windows-kernel']]) {
    const value = { protocol: 1, boot, clockDomain: domain, clockKind: kind,
      monoLoNs: '1000', monoHiNs: '2000', wallUnixNs: '1700000000000000000', uncertaintyNs: '3001000' };
    assert.equal(clockReceipt(Buffer.from(JSON.stringify(value))).rawKind, kind);
    for (const change of [v => { v.clockDomain += ':1'; }, v => { v.clockKind = 'linux-boottime'; },
      v => { v.boot = changedBoot.toUpperCase(); }, v => { v.uncertaintyNs = '3000000'; },
      v => { v.monoHiNs = '100001001'; }, v => { v.monoLoNs = '01000'; }]) {
      const bad = { ...value }; change(bad); assert.throws(() => clockReceipt(Buffer.from(JSON.stringify(bad))));
    }
  }
  assert.equal(domainOK('linux-time:18446744073709551616:7'), false);
  assert.equal(domainOK('linux-time:04:7'), false);
});

// Baseline identity/interval checks must remain strict after admitting known
// kinds; a second helper cannot replace the genuine activation anchor.
test('validly shaped but wrong helper boot/domain/kind and disjoint calibration deny', async () => {
  for (const change of [r => ({ ...r, boot: changedBoot }),
    r => ({ ...r, clockDomain: 'windows-kernel', clockKind: 'windows-interrupt-precise' }),
    r => ({ ...r, monoLoNs: '1000010000008', monoHiNs: '1000011000008' })]) {
    const f = deliveryFixture(); f.receipt(change);
    assert.equal(await f.delivery.activate(), false); assert.equal(f.closes(), 1);
    f.delivery.dispose(); assert.equal(f.delivery.ready(), false);
  }
});

test('post-spawn and queued admission refuse pauses without enlarging 110ms or metadata budgets', async () => {
  for (const mode of ['spawn', 'queued']) {
    const f = deliveryFixture();
    try {
      assert.equal(await f.delivery.activate(), true); f.advance(60000000n);
      const event = f.fact(), h = f.handoff(); assert.equal(await f.delivery.beforeEmit(event, h), true);
      if (mode === 'spawn') {
        f.gap(110000000n); assert.throws(() => f.delivery.emit(event, h));
      } else {
        const queue = deferred(); f.queue(queue);
        const work = f.delivery.emit(event, h);
        f.advance(2000000000n); assert.throws(queue.finish); queue.resolve(); await work;
      }
      assert.equal(f.delivery.ready(), false); assert.equal(f.closes(), 1); assert.equal(f.cancels(), 1);
    } finally { f.delivery.dispose(); }
  }
});

// Independent Python canonical-JSON vectors freeze the prior Linux description
// formula. Production roots must be rebound to final changed source bytes.
test('Linux amd64 image tuples, bounds and legacy profile ID formula remain equivalent', () => {
  const vectors = [[{"protocol":1,"generation":"v1","goos":"linux","goarch":"amd64","images":[{"version":"1.18.33","imageSHA256":"0abbb7c32ab0294c0a7bfa2705f9ff0df5dce5ab721d1f00cccfe393f2a11427"},{"version":"1.18.34","imageSHA256":"9ca0b9953d49997601655e54f846a3efa464f237e47c6f1b04716d0f2e64c4c2"}],"algorithmSourceMerkleSHA256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","sourceKind":"linux-proc-boottime","rawKind":"linux-boottime","nativeReadBoundNS":"103000000","comparisonBoundNS":"430000000","translationBoundNS":"224000000"},"linux-amd64-proc-boottime-v1:cc07fb25402ff8be38f898fa18fcdabfdb5c72900ee9668a46503156eff53144"],[{"protocol":1,"generation":"v2","goos":"linux","goarch":"amd64","images":[{"version":"2.0.21","imageSHA256":"f916986543348d7953d8d43aa048516cdbc3f84f4d0dc9c0c5b9d1da3030cea7"}],"algorithmSourceMerkleSHA256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","sourceKind":"linux-proc-boottime","rawKind":"linux-boottime","nativeReadBoundNS":"103000000","comparisonBoundNS":"430000000","translationBoundNS":"224000000"},"linux-amd64-proc-boottime-v1:1e07a40d3ae80b2a66066e437dd0a577bbfc978e3e2ff7ad4ab8fcf1f70f7d15"]];
  for (const [row, expected] of vectors) {
    assert.equal(describeClockPolicy(row).profileID, expected);
    assert.equal(describeClockPolicy(row).calibrationID, expected + ':same-coordinate');
    const selected = selectClockCell(row.generation);
    assert.throws(() => describeClockPolicy({ ...row, originalNativeAge: 'unverified_original_date' }));
    assert.equal(selectClockCell(row.generation), selected);
  }
});


test('native offset arithmetic requires an explicit premise and respects integer interval edges', () => {
  const sample = { boot, domain: 'darwin-kernel', rawKind: 'darwin-monotonic-raw',
    loNS: 1000n, hiNS: 1001n, wallNS: 1700000000000000000n };
  const result = withWallOffset(sample, 2000000n);
  assert.equal(result.offsetLoNS, 1699999999997998999n);
  assert.equal(result.offsetHiNS, 1700000000001999000n);
  assert.equal(result.loNS, sample.loNS); assert.ok(Object.isFrozen(result));
  for (const bound of [undefined, 2000000, -1n, 2000000001n]) denied(() => withWallOffset(sample, bound));
  for (const change of [s => ({ ...s, loNS: 0n, hiNS: 1n }), s => ({ ...s, hiNS: MAX + 1n }),
    s => ({ ...s, wallNS: 1n, loNS: MAX - 1n, hiNS: MAX }),
    s => ({ ...s, domain: 'darwin-kernel:1' })]) denied(() => withWallOffset(change(sample), 2000000n));
});

test('source disposal while preparing denies every future activation and closes a late binding once', async () => {
  const gate = deferred(); let signal, closes = 0;
  const f = deliveryFixture('windows-interrupt-precise', 'v1', options => { signal = options.signal; return gate.promise; });
  const work = f.delivery.activate(); f.delivery.dispose();
  assert.equal(signal.aborted, true); assert.equal(await work, false);
  assert.equal(await f.delivery.activate(), false); assert.equal(f.helpers.length, 0);
  gate.resolve({ sample() { assert.fail('disposed binding sampled'); }, dispose() { closes++; } });
  await Promise.resolve(); await Promise.resolve(); f.delivery.dispose(); assert.equal(closes, 1);
});

test('pre-epoch native birth stays denied without rebirth or provenance replacement', async () => {
  for (const kind of ['darwin-monotonic-raw', 'windows-interrupt-precise']) {
    const f = deliveryFixture(kind);
    try {
      assert.equal(await f.delivery.activate(), true);
      const old = f.fact(), nativeTime = old.provenance.nativeTime;
      assert.equal(f.delivery.allowsBirth(nativeTime), false);
      f.advance(60000000n); const handoff = f.handoff();
      assert.equal(await f.delivery.beforeEmit(old, handoff), false);
      assert.equal(old.provenance.nativeTime, nativeTime);
      assert.equal(old.provenance.observationID, 'original-observation');
      assert.equal(f.delivery.ready(), false); assert.equal(f.events(), 0);
    } finally { f.delivery.dispose(); }
  }
});


// An authenticated but different native image must not inherit the descriptor.
// Both hashes are inert test identities; production pins are untouched.
test('well-formed wrong native image denies before counter/helper and disposes the binding', async () => {
  for (const kind of ['darwin-monotonic-raw', 'windows-interrupt-precise']) {
    const f = deliveryFixture(kind); f.source.imageSHA256 = 'b'.repeat(64);
    assert.equal(await f.delivery.activate(), false);
    assert.equal(f.calls.includes('sample'), false); assert.equal(f.helpers.length, 0);
    assert.equal(f.closes(), 1); assert.equal(f.cancels(), 1);
    f.delivery.dispose(); assert.equal(f.closes(), 1);
  }
});

// Cancellation alone does not prove an unresolved preparation is bounded.
// This uses the real existing 2s timer, with no native/FFI/process boundary.
test('unresolved native preparation expires inside the existing budget and disposes its late result', { timeout: 5000 }, async () => {
  const gate = deferred(); let signal, closes = 0;
  const f = deliveryFixture('darwin-monotonic-raw', 'v2', options => { signal = options.signal; return gate.promise; });
  try {
    assert.equal(await f.delivery.activate(), false);
    assert.equal(signal.aborted, true); assert.equal(f.helpers.length, 0);
    assert.equal(f.delivery.ready(), false); assert.equal(f.cancels(), 1);
    gate.resolve({ sample() { assert.fail('expired source sampled'); }, dispose() { closes++; } });
    await Promise.resolve(); await Promise.resolve(); assert.equal(closes, 1);
  } finally { f.delivery.dispose(); }
});


test('original native age exception is explicit, image-exact and cannot select a clock', () => {
  const row = { protocol: 1, generation: 'v1', goos: 'windows', goarch: 'amd64',
    ...describeClockSource('windows', 'amd64'),
    images: [{ version: '1.18.33', imageSHA256: '52f60248a576b34c9a6dcaa27e0a7f08089af35bcdc0dfb10c04d3e00a98314c' }],
    algorithmSourceMerkleSHA256: 'd'.repeat(64), nativeReadBoundNS: '103000000',
    comparisonBoundNS: '430000000', translationBoundNS: '224000000', sourceWallBoundNS: '2000000',
    originalNativeAge: 'unverified_original_date' };
  const selected = selectClockCell('v1'), policy = describeClockPolicy(row);
  assert.equal(policy.originalNativeAge, 'unverified_original_date');
  // Independent Python canonical-JSON vector binds mode with the SAME image.
  assert.equal(policy.profileID, 'windows-amd64-windows-interrupt-precise-v1:591cfa5af34b8c88fca19f94bec620d2a5fce5c2558707e0b92ed61c8cceaff2');
  assert.ok(Object.isFrozen(policy)); assert.equal(selectClockCell('v1'), selected);
  for (const change of [r => { delete r.originalNativeAge; }, r => { r.originalNativeAge = 'bounded'; },
    r => { r.originalNativeAge = ''; }, r => { r.originalNativeAge = 'unknown'; },
    r => { r.images[0].imageSHA256 = 'a'.repeat(64); }, r => { r.images[0].version = '1.18.34'; },
    r => { r.generation = 'v2'; r.images[0].version = '2.0.21'; },
    r => { r.goos = 'darwin'; Object.assign(r, describeClockSource('darwin', 'amd64')); },
    r => { r.goos = 'linux'; Object.assign(r, describeClockSource('linux', 'amd64')); delete r.sourceWallBoundNS; },
    r => { r.goarch = 'arm64'; }]) {
    const bad = structuredClone(row); change(bad); assert.throws(() => describeClockPolicy(bad));
  }
  const legacy = { ...row, images: [{ version: '1.18.33', imageSHA256: 'a'.repeat(64) }] };
  delete legacy.originalNativeAge;
  assert.equal(describeClockPolicy(legacy).originalNativeAge, 'bounded');
  assert.notEqual(policy.profileID, describeClockPolicy(legacy).profileID);
  assert.equal(policy.calibrationID, policy.profileID + ':same-coordinate');
});
