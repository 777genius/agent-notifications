// API fixture only: actual public autoload, no product TimePolicy or model calls.
import { appendFileSync, closeSync, constants, existsSync, fstatSync, lstatSync,
  openSync, readFileSync, readlinkSync, readSync, realpathSync, statfsSync, statSync, truncateSync, writeFileSync } from 'node:fs';
import { isAbsolute, relative, resolve, sep } from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
const requireCheck = (value, reason) => { if (!value) throw new Error(reason); };
const rootInput = process.env.CLOCK_TEST_ROOT;
requireCheck(typeof rootInput === 'string' && isAbsolute(rootInput), 'absolute_test_root_required');
const root = resolve(rootInput);
requireCheck(root.split(sep).some(x => x.startsWith('TEST-')), 'test_component_required');
for (let at = root; ; at = resolve(at, '..')) {
  requireCheck(!lstatSync(at).isSymbolicLink(), 'test_symlink_ancestry_rejected');
  if (at === resolve(at, '..')) break;
}
requireCheck(realpathSync(root) === root && statSync(root).isDirectory(), 'private_root_required');
function localFile(name) {
  const path = resolve(root, name);
  requireCheck(relative(root, path) === name, 'owned_handshake_required');
  if (existsSync(path)) requireCheck(!lstatSync(path).isSymbolicLink(), 'handshake_symlink_rejected');
  return path;
}
const metadata = JSON.parse(readFileSync(localFile('metadata.json'), 'utf8'));
const MAX = 9223372036854775807n, U64 = 18446744073709551615n;
const QUANTUM = 10000000n, R = 103000000n, Q = 2000000n;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const checks = {};
const aggregate = { maxPairWidthNs: 0n, maxOuterWidthNs: 0n, maxGoWidthNs: 0n,
  maxDatePreciseDistanceNs: 0n, preciseComparisons: 0 };
let started = false, recordCount = 0;
function record(value, operationStart) {
  const line = JSON.stringify({ schema: 1, pid: process.pid, ...value }) + '\n';
  requireCheck(recordCount < 2 && Buffer.byteLength(line) <= 16384, 'trace_limit');
  const path = localFile('loader-private.jsonl'), previousSize = existsSync(path) ? statSync(path).size : 0;
  if (operationStart !== undefined) requireCheck(performance.now() - operationStart <= 2000, 'qualification_operation_deadline');
  appendFileSync(path, line, { mode: 0o600 }); recordCount++;
  if (operationStart !== undefined && performance.now() - operationStart > 2000) {
    truncateSync(path, previousSize); recordCount--;
    throw new Error('qualification_operation_deadline');
  }
}
function decimal(value) {
  requireCheck(typeof value === 'string' && /^(0|[1-9][0-9]*)$/.test(value) && value.length <= 19, 'canonical_decimal_required');
  const number = BigInt(value); requireCheck(number <= MAX, 'int64_overflow'); return number;
}
function uint(value) {
  requireCheck(typeof value === 'bigint' || Number.isSafeInteger(value), 'ffi_integer_fidelity');
  const v = BigInt(value); requireCheck(v >= 0n && v <= U64, 'ffi_uint64_range'); return v;
}
function bootUUID(value) {
  requireCheck(uuid.test(value) && value !== '00000000-0000-0000-0000-000000000000', 'native_boot_uuid_required');
  return value;
}
function fsType(path, expected) {
  const v = statfsSync(path, { bigint: true });
  requireCheck(typeof v.type === 'bigint' && v.type === expected, 'native_filesystem_magic_mismatch');
}
function fdStat(fd) {
  const v = fstatSync(fd, { bigint: true });
  requireCheck(typeof v.dev === 'bigint' && typeof v.ino === 'bigint' && v.dev > 0n && v.ino > 0n, 'fstat_bigint_identity_required');
  return v;
}
function fdMagic(fd) { return `/proc/thread-self/fd/${fd}`; }
function readBounded(fd, limit) {
  const b = Buffer.alloc(limit + 1), size = readSync(fd, b, 0, b.length, 0);
  requireCheck(size > 0 && size <= limit && b.subarray(0, size).every(x => x < 128), 'native_read_bound');
  return b.subarray(0, size).toString('ascii');
}
function hash(path) { return createHash('sha256').update(readFileSync(path)).digest('hex'); }
function maximum(key, value) { if (value > aggregate[key]) aggregate[key] = value; }
function linuxSource() {
  requireCheck(process.platform === 'linux' && ['x64', 'arm64'].includes(process.arch), 'linux_native_cell_required');
  requireCheck(Number.isInteger(constants.O_RDONLY) && Number.isInteger(constants.O_NOFOLLOW), 'native_open_flags_required');
  fsType('/proc/thread-self', 0x9fa0n);
  const callingThread = readlinkSync('/proc/thread-self');
  requireCheck(new RegExp(`^${process.pid}/task/[1-9][0-9]*$`).test(callingThread), 'current_calling_thread_required');
  const held = [];
  function open(path, noFollow) {
    // Public fs constants has no O_CLOEXEC; do not invent a native binding.
    const fd = openSync(path, constants.O_RDONLY | (noFollow ? constants.O_NOFOLLOW : 0));
    held.push(fd); return fd;
  }
  try {
    const uptime = open('/proc/uptime', true);
    const bootFile = open('/proc/sys/kernel/random/boot_id', true);
    const ns = open('/proc/thread-self/ns/time', false); // fixed kernel namespace magic link
    fsType(fdMagic(uptime), 0x9fa0n); fsType(fdMagic(bootFile), 0x9fa0n); checks.procfs = true;
    fsType(fdMagic(ns), 0x6e736673n); checks.nsfs = true;
    const procIdentity = fdStat(uptime), bootIdentity = fdStat(bootFile), nsIdentity = fdStat(ns);
    checks.bigintFs = true;
    const domain = `linux-time:${nsIdentity.dev}:${nsIdentity.ino}`;
    function boot() {
      const value = readBounded(bootFile, 64);
      requireCheck(value.length === 37 && value.endsWith('\n') && uuid.test(value.slice(0, -1)) && value.slice(0, -1) !== '00000000-0000-0000-0000-000000000000', 'native_boot_uuid_required');
      return value.slice(0, -1);
    }
    const initialBoot = boot(); checks.bootGrammar = true;
    function current() {
      requireCheck(readlinkSync('/proc/thread-self') === callingThread, 'current_calling_thread_changed');
      checks.currentCallingThread = true;
      // Current thread must still inhabit the held namespace, not merely retain its fd.
      const value = statSync('/proc/thread-self/ns/time', { bigint: true });
      requireCheck(typeof value.dev === 'bigint' && typeof value.ino === 'bigint' && value.dev === nsIdentity.dev && value.ino === nsIdentity.ino, 'current_thread_namespace_changed');
      const present = statSync('/proc/uptime', { bigint: true });
      const presentBoot = statSync('/proc/sys/kernel/random/boot_id', { bigint: true });
      requireCheck(present.dev === procIdentity.dev && present.ino === procIdentity.ino &&
        presentBoot.dev === bootIdentity.dev && presentBoot.ino === bootIdentity.ino && boot() === initialBoot, 'proc_identity_or_boot_changed');
    }
    let previous = -1n;
    function uptimeNs() {
      const raw = readBounded(uptime, 256);
      const match = /^(0|[1-9][0-9]*)\.([0-9]{2}) (0|[1-9][0-9]*)\.[0-9]{2}\n$/.exec(raw);
      requireCheck(match !== null, 'native_uptime_grammar');
      const value = BigInt(match[1]) * 1000000000n + BigInt(match[2]) * QUANTUM;
      requireCheck(value <= MAX - QUANTUM && value >= previous, 'uptime_regression_or_overflow');
      previous = value;
      const milliseconds = Number(value / 1000000n);
      requireCheck(Number.isSafeInteger(milliseconds) && BigInt(milliseconds) * 1000000n === value, 'sdk_number_roundtrip_failed');
      checks.integerRoundtrip = true;
      return value;
    }
    let previousWall = -1n;
    function pair() {
      current();
      const lo = uptimeNs(), wallMs = Date.now(), upperFloor = uptimeNs();
      current(); checks.currentThreadDomain = true;
      requireCheck(Number.isSafeInteger(wallMs) && wallMs > 0 && BigInt(wallMs) * 1000000n <= MAX, 'native_date_int64_required');
      requireCheck(upperFloor >= lo && upperFloor - lo <= 100000000n, 'proc_pair_width');
      const hi = upperFloor + QUANTUM, wall = BigInt(wallMs) * 1000000n;
      requireCheck(wall >= previousWall, 'source_date_regression'); previousWall = wall;
      const width = hi - lo;
      if (width > aggregate.maxPairWidthNs) aggregate.maxPairWidthNs = width;
      checks.pairWidth = true;
      return { lo, hi, wall, domain, boot: initialBoot, offsetLo: wall - Q - hi, offsetHi: wall + Q - lo };
    }
    return { pair() { return { ...pair(), kind: 'linux-boottime' }; }, close() { for (const fd of held.splice(0)) closeSync(fd); } };
  } catch (error) {
    for (const fd of held) closeSync(fd);
    throw error;
  }
}
async function ffiSource() {
  const { dlopen, ptr, CFunction } = await import('bun:ffi');
  requireCheck(typeof dlopen === 'function' && typeof ptr === 'function', 'public_ffi_unavailable');
  const libraries = [], wrappers = [];
  let apiHandle, kernel;
  function library(path, symbols) { const lib = dlopen(path, symbols); libraries.push(lib); return lib.symbols; }
  function aligned(array) {
    const address = ptr(array);
    requireCheck(Number.isSafeInteger(address) && address > 0 && address % array.BYTES_PER_ELEMENT === 0, 'ffi_alignment');
    return address;
  }
  function close() {
    for (const fn of wrappers.splice(0)) fn.close();
    if (apiHandle !== undefined) { requireCheck(kernel.FreeLibrary(apiHandle) !== 0, 'api_set_release_failed'); apiHandle = undefined; }
    for (const lib of libraries.splice(0).reverse()) lib.close();
  }
  try {
    let tick, boot, preciseWall, kind, domain;
    if (process.platform === 'darwin') {
      const lib = library('/usr/lib/libSystem.B.dylib', {
        mach_continuous_time: { returns: 'u64', args: [] },
        mach_timebase_info: { returns: 'i32', args: ['ptr'] },
        sysctlbyname: { returns: 'i32', args: ['ptr', 'ptr', 'ptr', 'ptr', 'u64'] },
      });
      const base = new Uint32Array(2);
      requireCheck(lib.mach_timebase_info(aligned(base)) === 0 && base[0] > 0 && base[1] > 0, 'mach_timebase_failed');
      const numer = BigInt(base[0]), denom = BigInt(base[1]);
      const name = Buffer.from('kern.bootsessionuuid\0'), buffer = new Uint32Array(16), size = new BigUint64Array(1);
      boot = () => {
        buffer.fill(0); size[0] = BigInt(buffer.byteLength);
        requireCheck(lib.sysctlbyname(ptr(name), aligned(buffer), aligned(size), null, 0n) === 0 && size[0] === 37n, 'boot_sysctl_size_status');
        const bytes = Buffer.from(buffer.buffer);
        requireCheck(bytes[36] === 0 && bytes.subarray(0, 36).every(b => b > 0 && b < 128), 'boot_sysctl_encoding');
        // Apple sysctl may emit uppercase; Go's native port also canonicalizes.
        return bootUUID(bytes.subarray(0, 36).toString('ascii').toLowerCase());
      };
      tick = () => {
        const ticks = uint(lib.mach_continuous_time());
        requireCheck(ticks > 0n && ticks <= U64 / numer, 'mach_multiply_overflow');
        const ns = ticks * numer / denom;
        requireCheck(ns <= MAX, 'mach_int64_overflow'); return ns;
      };
      kind = 'darwin-monotonic-raw'; domain = 'darwin-kernel';
      checks.machTimebaseAndSysctl = true;
    } else {
      requireCheck(process.platform === 'win32' && process.arch === 'x64', 'windows_native_cell_required');
      const system = metadata.windows;
      requireCheck(system && isAbsolute(system.kernel32) && isAbsolute(system.ntdll) &&
        hash(system.kernel32) === system.kernel32Sha256 && hash(system.ntdll) === system.ntdllSha256, 'authenticated_os_bootstrap_required');
      kernel = library(system.kernel32, {
        GetSystemDirectoryW: { returns: 'u32', args: ['ptr', 'u32'] },
        LoadLibraryExW: { returns: 'u64', args: ['ptr', 'u64', 'u32'] },
        GetProcAddress: { returns: 'ptr', args: ['u64', 'ptr'] },
        FreeLibrary: { returns: 'i32', args: ['u64'] },
        GetSystemTimePreciseAsFileTime: { returns: 'void', args: ['ptr'] },
      });
      const directory = new Uint16Array(32768);
      const length = kernel.GetSystemDirectoryW(aligned(directory), directory.length);
      requireCheck(length > 0 && length < directory.length && directory[length] === 0 &&
        String.fromCharCode(...directory.subarray(0, length)).toLowerCase() === system.directory.toLowerCase(), 'ffi_system_directory_mismatch');
      const contract = Buffer.from('api-ms-win-core-realtime-l1-1-1.dll\0', 'utf16le');
      apiHandle = uint(kernel.LoadLibraryExW(ptr(contract), 0n, 0x800));
      requireCheck(apiHandle > 0n && typeof CFunction === 'function', 'api_set_load_or_public_cfunction_unavailable');
      const address = kernel.GetProcAddress(apiHandle, ptr(Buffer.from('QueryInterruptTimePrecise\0')));
      requireCheck(Number.isSafeInteger(address) && address > 0, 'precise_interrupt_symbol_unavailable');
      const precise = new CFunction({ ptr: address, returns: 'void', args: ['ptr'] }); wrappers.push(precise);
      const counter = new BigUint64Array(1), filetime = new BigUint64Array(1);
      tick = () => {
        counter[0] = 0n; precise(aligned(counter)); // VOID; ignore return and last-error.
        requireCheck(counter[0] > 0n && counter[0] <= MAX / 100n, 'interrupt_output_or_overflow');
        return counter[0] * 100n;
      };
      preciseWall = () => {
        filetime[0] = 0n; kernel.GetSystemTimePreciseAsFileTime(aligned(filetime));
        requireCheck(filetime[0] > 116444736000000000n && filetime[0] - 116444736000000000n <= MAX / 100n, 'filetime_output_or_overflow');
        return (filetime[0] - 116444736000000000n) * 100n;
      };
      const nt = library(system.ntdll, { NtQuerySystemInformation: { returns: 'i32', args: ['u32', 'ptr', 'u32', 'ptr'] } });
      const info = new BigUint64Array(4), returned = new Uint32Array(1);
      boot = () => {
        info.fill(0n); returned[0] = 0;
        requireCheck(nt.NtQuerySystemInformation(90, aligned(info), 32, aligned(returned)) === 0 && returned[0] === 32, 'nt_boot_status_or_length');
        const b = Buffer.from(info.buffer), hex = (n, w) => n.toString(16).padStart(w, '0');
        return bootUUID(`${hex(b.readUInt32LE(0), 8)}-${hex(b.readUInt16LE(4), 4)}-${hex(b.readUInt16LE(6), 4)}-${b.subarray(8, 10).toString('hex')}-${b.subarray(10, 16).toString('hex')}`);
      };
      kind = 'windows-interrupt-precise'; domain = 'windows-kernel';
      checks.system32ApiSet = true; checks.ntBootExact32 = true; checks.voidUnsigned100ns = true;
    }
    const initialBoot = boot(); let previous = -1n, lastPrecise = -1n, previousDate = -1n;
    function readTick() {
      const value = tick(); requireCheck(value >= previous, 'native_counter_regression'); previous = value;
      const ms = Number(value / 1000000n);
      requireCheck(Number.isSafeInteger(ms) && BigInt(ms) * 1000000n <= value && value - BigInt(ms) * 1000000n < 1000000n, 'sdk_integer_floor_fidelity');
      checks.integerRoundtrip = true; return value;
    }
    return { close, pair() {
      requireCheck(boot() === initialBoot, 'native_boot_changed');
      const lo = readTick(), preciseLo = preciseWall?.(), wallMs = Date.now(), preciseHi = preciseWall?.(), hi = readTick();
      requireCheck(boot() === initialBoot && hi >= lo && hi - lo <= 100000000n, 'native_pair_identity_or_width');
      requireCheck(Number.isSafeInteger(wallMs) && wallMs > 0 && BigInt(wallMs) <= MAX / 1000000n, 'native_date_int64_required');
      const wall = BigInt(wallMs) * 1000000n;
      requireCheck(wall >= previousDate, 'source_date_regression'); previousDate = wall;
      if (preciseWall) {
        requireCheck(preciseHi >= preciseLo && preciseLo >= lastPrecise, 'precise_wall_regression'); lastPrecise = preciseHi;
        maximum('maxDatePreciseDistanceNs', wall < preciseLo ? preciseLo - wall : wall > preciseHi ? wall - preciseHi : 0n);
        aggregate.preciseComparisons++;
        checks.dateInsidePreciseInterval = (checks.dateInsidePreciseInterval ?? true) && wall >= preciseLo && wall <= preciseHi;
        checks.dateWithinTwoMsOfPrecise = (checks.dateWithinTwoMsOfPrecise ?? true) && wall + Q >= preciseLo && wall - Q <= preciseHi;
      }
      checks.pairWidth = true; maximum('maxPairWidthNs', hi - lo);
      return { lo, hi, wall, boot: initialBoot, domain, kind, preciseLo, preciseHi,
        offsetLo: wall - Q - hi, offsetHi: wall + Q - lo };
    } };
  } catch (error) { close(); throw error; }
}
function helper(raw, current) {
  requireCheck(Buffer.byteLength(raw) <= 1024, 'go_renderer_response_limit');
  const v = JSON.parse(raw), keys = ['protocol', 'boot', 'clockDomain', 'clockKind', 'monoLoNs', 'monoHiNs', 'wallUnixNs', 'uncertaintyNs'];
  requireCheck(JSON.stringify(Object.keys(v)) === JSON.stringify(keys) && JSON.stringify(v) + '\n' === raw && v.protocol === 1, 'canonical_closed_helper_required');
  bootUUID(v.boot);
  requireCheck(v.boot === current.boot && v.clockDomain === current.domain && v.clockKind === current.kind, 'helper_native_coordinate_mismatch');
  const lo = decimal(v.monoLoNs), hi = decimal(v.monoHiNs), wall = decimal(v.wallUnixNs), error = decimal(v.uncertaintyNs);
  requireCheck(hi >= lo && hi - lo <= 100000000n && error === hi - lo + 3000000n && error <= R, 'native_helper_bracket_invalid');
  checks.canonicalHelper = true; checks.goDomainBootKind = true; checks.nativeBracket = true;
  maximum('maxGoWidthNs', hi - lo);
  return { lo, hi, wall, offsetLo: wall - error - hi, offsetHi: wall + error - lo };
}
async function qualify() {
  let clock, rounds = 0;
  try {
    // Import/ABI preparation and genuine host initialization do not consume the 2s operation.
    clock = process.platform === 'linux' ? linuxSource() : await ffiSource();
    writeFileSync(localFile('clock-ready'), 'ready\n', { mode: 0o600, flag: 'wx' });
    const waitStart = performance.now();
    while (!existsSync(localFile('clock-start'))) {
      requireCheck(performance.now() - waitStart <= 45000, 'diagnostic_arm_deadline');
      await new Promise(r => setTimeout(r, 5));
    }
    requireCheck(readFileSync(localFile('clock-start'), 'ascii') === 'start\n', 'fresh_arm_required');
    const operationStart = performance.now(); let firstLo, previousNative, previousPair;
    for (let i = 0; i < 3; i++) {
      const before = clock.pair(); firstLo ??= before.lo;
      if (previousPair) requireCheck(before.lo >= previousPair.lo && before.wall >= previousPair.wall, 'source_wall_or_counter_regression');
      const response = localFile(`clock-response-${i}.json`);
      requireCheck(!existsSync(response), 'fresh_response_required');
      writeFileSync(localFile(`clock-request-${i}`), `${i}\n`, { mode: 0o600, flag: 'wx' });
      while (!existsSync(response)) {
        requireCheck(performance.now() - operationStart <= 2000 && clock.pair().hi - firstLo <= 2000000000n, 'qualification_operation_deadline');
        await new Promise(r => setTimeout(r, 5));
      }
      requireCheck(statSync(response).size <= 4096, 'helper_transport_limit');
      const native = helper(readFileSync(response, 'utf8'), before), after = clock.pair();
      requireCheck(performance.now() - operationStart <= 2000 && after.hi - firstLo <= 2000000000n, 'qualification_operation_deadline');
      requireCheck(native.lo >= before.lo && native.hi <= after.hi && after.wall >= before.wall, 'native_causal_overlap_or_date_regression');
      if (previousNative) requireCheck(native.lo >= previousNative.hi && native.wall >= previousNative.wall, 'helper_cross_round_regression');
      if (process.platform === 'win32') {
        requireCheck(native.wall >= before.preciseLo && native.wall <= after.preciseHi, 'go_precise_wall_containment');
        // Windows Date predicates are observations, never a Q/R/T grant.
        checks.goPreciseWallContained = true;
      } else {
        const intervals = [before, native, after];
        requireCheck(intervals.reduce((v, x) => v > x.offsetLo ? v : x.offsetLo, intervals[0].offsetLo) <=
          intervals.reduce((v, x) => v < x.offsetHi ? v : x.offsetHi, intervals[0].offsetHi) &&
          native.wall >= before.wall - Q && native.wall <= after.wall + Q, 'native_wall_continuity_failed');
      }
      checks.wallContinuity = true; checks.causalOverlap = true; checks.monotonicNonregression = true;
      maximum('maxOuterWidthNs', after.hi - before.lo);
      previousNative = native; previousPair = after; rounds++;
    }
    requireCheck(clock.pair().hi - firstLo <= 2000000000n, 'qualification_operation_deadline');
    clock.close(); clock = undefined;
    requireCheck(performance.now() - operationStart <= 2000, 'qualification_operation_deadline');
    record({ kind: 'clock_result', status: 'api_prequalification_passed', roundCount: rounds, checks,
      aggregate: Object.fromEntries(Object.entries(aggregate).map(([k, v]) => [k, typeof v === 'bigint' ? v.toString() : v])),
      datePrecisePredicatesObserved: aggregate.preciseComparisons > 0, sourceWallBoundQualified: false,
      candidateRQualified: false, candidateTQualified: false, productionClockModuleBound: false,
      timePolicyQualified: false, suspendExperimentPerformed: false, providerCalls: 0 }, operationStart);
  } catch (error) {
    const reason = /^[a-z0-9_]{1,80}$/.test(error?.message ?? '') ? error.message : 'native_api_exception';
    record({ kind: 'clock_result', status: 'api_prequalification_gap', failureReason: reason, roundCount: rounds,
      productionClockModuleBound: false, timePolicyQualified: false, sourceWallBoundQualified: false });
  } finally { clock?.close(); }
}
function start(branch, ctx) {
  requireCheck(!started, 'duplicate_native_loader'); started = true;
  requireCheck(process.platform === metadata.platform && process.arch === metadata.arch &&
    process.ppid === metadata.parentPID && process.execPath === metadata.executable &&
    hash(process.execPath) === metadata.imageSha256 && hash(fileURLToPath(import.meta.url)) === metadata.moduleSha256 &&
    globalThis.Bun?.version === metadata.bun, 'native_image_module_identity_mismatch');
  requireCheck(branch === metadata.branch && typeof ctx === 'object' && ctx !== null &&
    (branch === 'v1' ? typeof ctx.client?.session?.get === 'function' : typeof ctx.event?.subscribe === 'function'), 'genuine_public_context_required');
  record({ kind: 'loader', branch, execPath: process.execPath, ppid: process.ppid, platform: process.platform,
    arch: process.arch, bunVersion: Bun.version, moduleSha256: metadata.moduleSha256, imageSha256: metadata.imageSha256,
    sourceManifestSha256: metadata.sourceManifestSha256, publicContextVerified: true });
  void qualify();
}
export default { id: 'native-clock-api-qualification',
  async server(ctx) { start('v1', ctx); return {}; },
  async setup(ctx) { start('v2', ctx); },
};
