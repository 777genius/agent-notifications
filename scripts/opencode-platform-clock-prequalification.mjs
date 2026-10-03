// Parent CI fixture. Importing this file is inert; only explicit private execution
// invokes the ACTUAL source dispatch. No API substitutes, observer or policy rows.
import { createHash } from 'node:crypto';
import { openSync, readSync, fstatSync, statSync, lstatSync, realpathSync, closeSync,
  constants, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { resolve, relative, isAbsolute, sep } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { createInterface } from 'node:readline';

export const budgets = Object.freeze({ preparationMs: 2000, jsMs: 25000, goMs: 20000,
  helperMs: 224, sampleCap: 512, chunkSize: 32, rounds: 3, frameBytes: 16384 });
export const qualifications = Object.freeze({ TimePolicyQualified: false,
  runtimeEligibilityGranted: false, candidateRQualified: false, candidateTQualified: false,
  sourceWallBoundQualified: false, suspendQualified: false, finalSpanQualified: false,
  registryQualified: false });
export function need(value, reason) { if (!value) throw new Error(reason); }
const MAX = 9223372036854775807n, U64 = 18446744073709551615n;
export function decimal(value) {
  need(typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value), 'decimal_grammar');
  const n = BigInt(value); need(n <= MAX, 'int64_range'); return n;
}
export function tuple(boot, domain, kind, platform) {
  need(typeof boot === 'string' && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(boot) &&
    boot !== '00000000-0000-0000-0000-000000000000', 'boot_grammar');
  if (platform === 'linux') {
    need(kind === 'linux-boottime' && /^linux-time:[1-9][0-9]*:[1-9][0-9]*$/.test(domain) &&
      domain.split(':').slice(1).every(x => x.length <= 20 && BigInt(x) <= U64), 'linux_tuple');
  } else need((platform === 'darwin' && domain === 'darwin-kernel' && kind === 'darwin-monotonic-raw') ||
    (platform === 'win32' && domain === 'windows-kernel' && kind === 'windows-interrupt-precise'), 'native_tuple');
}
export function helperFrame(raw, platform) {
  need(Buffer.isBuffer(raw) && raw.length > 0 && raw.length <= 1024, 'helper_frame_limit');
  // Canonical reserialization refuses duplicate keys, whitespace and extra frames.
  const v = JSON.parse(raw.toString('utf8'));
  const keys = ['protocol', 'boot', 'clockDomain', 'clockKind', 'monoLoNs', 'monoHiNs', 'wallUnixNs', 'uncertaintyNs'];
  need(Object.keys(v).join('|') === keys.join('|') && v.protocol === 1 &&
    `${JSON.stringify(v)}\n` === raw.toString('utf8'), 'canonical_helper_frame');
  tuple(v.boot, v.clockDomain, v.clockKind, platform);
  const [lo, hi, wall, uncertainty] = keys.slice(4).map(k => decimal(v[k]));
  need(lo > 0n && wall > 0n && hi >= lo && hi - lo <= 100000000n &&
    uncertainty === hi - lo + 3000000n, 'original_go_bracket');
  return { boot: v.boot, domain: v.clockDomain, kind: v.clockKind, lo, hi, wall };
}
export function productSample(s, platform) {
  need(s && Object.isFrozen(s), 'frozen_actual_sample');
  const keys = platform === 'linux' ? ['boot', 'domain', 'rawKind', 'loNS', 'hiNS', 'wallNS', 'offsetLoNS', 'offsetHiNS'] :
    ['boot', 'domain', 'rawKind', 'loNS', 'hiNS', 'wallNS'];
  need(Object.keys(s).length === keys.length && keys.every(k => Object.hasOwn(s, k)), 'actual_sample_shape');
  const kind = s.rawKind;
  tuple(s.boot, s.domain, kind, platform);
  for (const k of ['loNS', 'hiNS', 'wallNS']) need(typeof s[k] === 'bigint' && s[k] > 0n && s[k] <= MAX, 'actual_wall_counter_types');
  need(s.hiNS >= s.loNS && s.hiNS - s.loNS <= (platform === 'linux' ? 110000000n : 100000000n), 'actual_pair_width');
  if (platform === 'linux') need(typeof s.offsetLoNS === 'bigint' && typeof s.offsetHiNS === 'bigint' &&
    s.offsetLoNS === s.wallNS - s.hiNS - 2000000n && s.offsetHiNS === s.wallNS - s.loNS + 2000000n, 'unchanged_linux_offsets');
  return { boot: s.boot, domain: s.domain, kind, lo: s.loNS, hi: s.hiNS, wall: s.wallNS };
}
export function compare(before, go, after, platform) {
  need([go, after].every(s => s.boot === before.boot && s.domain === before.domain && s.kind === before.kind), 'actual_tuple_parity');
  need(before.lo <= go.lo && go.hi <= after.hi && after.lo >= before.lo && after.wall >= before.wall,
    'causal_counter_containment');
  need(after.hi - before.lo <= 2000000000n, 'original_preparation_counter_span');
  // Same full native endpoint span as production anchorMatches.
  need(after.hi - before.lo <= BigInt(budgets.helperMs) * 1000000n, 'actual_translation_counter_span');
  // Fixed local source assumptions only. Neither predicate grants a wall policy.
  const wallAllowance = platform === 'win32' ? 0n : 2000000n;
  need(go.wall >= before.wall - wallAllowance && go.wall <= after.wall + wallAllowance, 'causal_wall_containment');
  return { outerWidthNs: String(after.hi - before.lo), goWidthNs: String(go.hi - go.lo) };
}
export function budgetCheck(now, start, count, chunk) {
  need(Number.isFinite(now) && Number.isFinite(start) && now >= start && now - start < budgets.preparationMs,
    'original_preparation_deadline');
  need(Number.isInteger(count) && count >= 0 && count <= budgets.sampleCap &&
    Number.isInteger(chunk) && chunk >= 0 && chunk <= budgets.chunkSize, 'sampler_bounds');
}
export function preciseDate(beforeWall, dateMs, afterWall) {
  need(typeof beforeWall === 'bigint' && typeof afterWall === 'bigint' && beforeWall > 0n &&
    afterWall >= beforeWall && afterWall <= MAX && Number.isSafeInteger(dateMs) && dateMs > 0,
    'native_date_types');
  const date = BigInt(dateMs) * 1000000n;
  need(date <= MAX, 'native_date_range');
  const distance = date < beforeWall ? beforeWall - date : date > afterWall ? date - afterWall : 0n;
  return { dateInsideNativeInterval: distance === 0n, datePreciseDistanceNs: String(distance) };
}
export function identity(actual, expected) {
  need(actual.platform === expected.platform && actual.arch === expected.arch && actual.bun === expected.bun &&
    actual.imageSha256 === expected.imageSha256 && actual.fixtureSha256 === expected.fixtureSha256 &&
    actual.sourceCommit === expected.sourceCommit && /^[a-f0-9]{40}$/.test(actual.sourceCommit), 'actual_image_module_identity');
}
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
function contained(root, input) {
  need(isAbsolute(input) && realpathSync(input) === input && !lstatSync(input).isSymbolicLink(), 'canonical_private_path');
  const rel = relative(root, input);
  need(rel && !rel.startsWith(`..${sep}`) && rel !== '..' && !isAbsolute(rel), 'private_path_containment');
  return input;
}
function heldImage(path) {
  const fd = openSync(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
  try {
    const initial = fstatSync(fd, { bigint: true });
    const keys = ['dev', 'ino', 'size', 'mtimeNs', 'ctimeNs'];
    need(initial.isFile() && initial.ino > 0n && initial.size > 0n && initial.size <= 536870912n &&
      keys.every(k => typeof initial[k] === 'bigint'), 'actual_image_file_types');
    const h = createHash('sha256'), b = Buffer.alloc(65536);
    for (let at = 0; at < Number(initial.size);) {
      const n = readSync(fd, b, 0, Math.min(b.length, Number(initial.size) - at), at);
      need(n > 0, 'actual_image_read'); h.update(b.subarray(0, n)); at += n;
    }
    let closed = false;
    return { digest: h.digest('hex'), fileIdentity: initial, verify() {
      need(!closed && realpathSync(process.execPath) === path && [fstatSync(fd, { bigint: true }),
        statSync(path, { bigint: true })].every(s => keys.every(k => s[k] === initial[k])), 'current_image_held_file');
    }, close() { if (!closed) { closed = true; closeSync(fd); } } };
  } catch (e) { closeSync(fd); throw e; }
}
function send(value) {
  const raw = JSON.stringify(value, (_, v) => typeof v === 'bigint' ? String(v) : v) + '\n';
  need(Buffer.byteLength(raw) <= budgets.frameBytes, 'fixture_frame_limit');
  process.stdout.write(raw);
}

async function execute(rootInput) {
  need(isAbsolute(rootInput) && realpathSync(rootInput) === rootInput &&
    rootInput.split(sep).some(p => p.startsWith('TEST-')), 'fresh_test_root');
  for (let p = rootInput;; p = resolve(p, '..')) {
    need(!lstatSync(p).isSymbolicLink(), 'test_ancestry'); if (p === resolve(p, '..')) break;
  }
  const root = rootInput, metadataPath = contained(root, resolve(root, 'metadata.json'));
  need(statSync(metadataPath).size <= 1048576, 'metadata_limit');
  const m = JSON.parse(readFileSync(metadataPath, 'utf8'));
  for (const k of ['HOME', 'USERPROFILE', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME',
    'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'TMPDIR', 'TEMP', 'TMP', 'OPENCODE_CONFIG_DIR'])
    contained(root, process.env[k]);
  need(process.env.BUN_BE_BUN === '1' && Object.keys(process.env).every(k => m.environmentKeys.includes(k)), 'minimal_actual_environment');
  const fixture = contained(root, fileURLToPath(import.meta.url));
  const imagePath = contained(root, realpathSync(process.execPath)), image = heldImage(imagePath);
  let clock, lines, disposed = false, previous, samples = 0;
  const globalStart = performance.now();
  const watchdog = setTimeout(() => { process.exitCode = 1; process.stdin.destroy(); }, budgets.jsMs);
  try {
    const actual = { platform: process.platform, arch: process.arch, bun: globalThis.Bun?.version,
      imageSha256: image.digest, fixtureSha256: hash(readFileSync(fixture)), sourceCommit: m.sourceCommit };
    identity(actual, m); image.verify();
    const modulePaths = {};
    for (const leaf of m.moduleLeaves) {
      const path = contained(root, resolve(root, leaf.path));
      need(hash(readFileSync(path)) === leaf.actualSHA256, 'actual_source_bytes');
      modulePaths[leaf.path] = path;
    }
    const stdin = createInterface({ input: process.stdin, crlfDelay: Infinity }); lines = stdin;
    const iterator = stdin[Symbol.asyncIterator]();
    async function receive(kind) {
      const next = await iterator.next();
      need(!next.done && Buffer.byteLength(next.value) <= budgets.frameBytes, 'parent_frame_limit_or_eof');
      const v = JSON.parse(next.value); need(v.kind === kind, 'parent_frame_order'); return v;
    }
    send({ kind: 'loader', ...actual, pid: process.pid, ppid: process.ppid, executable: imagePath,
      fixture, modulePaths, imageFileIdentity: image.fileIdentity, loader: 'stock_BUN_BE_BUN_private_source_import' });
    await receive('begin');
    const operationStart = performance.now();
    const dispatch = await import(pathToFileURL(modulePaths['opencode-plugin/platform-clock.mjs']).href);
    budgetCheck(performance.now(), operationStart, samples, 0);
    clock = await dispatch.createPlatformClock(); // The only clock implementation called here.
    need(clock && Object.isFrozen(clock) && typeof clock.sample === 'function' && typeof clock.dispose === 'function', 'actual_module_lifecycle');
    budgetCheck(performance.now(), operationStart, samples, 0);
    function sample() {
      budgetCheck(performance.now(), operationStart, ++samples, 0); image.verify();
      const value = productSample(clock.sample(), process.platform);
      if (previous) need(value.boot === previous.boot && value.domain === previous.domain && value.kind === previous.kind &&
        value.lo >= previous.lo && value.wall >= previous.wall, 'actual_sampler_nonregression');
      previous = value; image.verify(); budgetCheck(performance.now(), operationStart, samples, 0);
      return value;
    }
    const comparisons = [], raw = [], datePredicates = [];
    for (let round = 0; round < budgets.rounds; round++) {
      for (let chunk = 0; chunk < budgets.chunkSize; chunk++) {
        budgetCheck(performance.now(), operationStart, samples, chunk + 1); sample();
      }
      const before = sample();
      send({ kind: 'helper_request', round, before });
      const reply = await receive('helper_response');
      need(reply.round === round && typeof reply.raw === 'string' && reply.raw.length <= 1368, 'helper_response_identity');
      budgetCheck(performance.now(), operationStart, samples, 0);
      const after = sample(), bytes = Buffer.from(reply.raw, 'base64');
      need(bytes.toString('base64') === reply.raw, 'canonical_helper_base64');
      const go = helperFrame(bytes, process.platform);
      const comparison = compare(before, go, after, process.platform); comparisons.push(comparison);
      const record = { before, go, after, helper: bytes.toString('utf8'),
        types: { sourceCounter: typeof before.lo, sourceWall: typeof before.wall,
          goCounter: 'protocol1_decimal_string', goWall: 'protocol1_decimal_string' } };
      raw.push(record);
      // Windows native precise wall vs Date is reported separately, with zero
      // quantum/error grants and no required successful Date predicate.
      if (process.platform === 'win32') {
        const nativeBefore = sample(), dateMs = Date.now(), nativeAfter = sample();
        const predicate = preciseDate(nativeBefore.wall, dateMs, nativeAfter.wall);
        datePredicates.push(predicate);
        record.nativeDateComparison = { nativeBefore, dateMs, nativeAfter, predicate, dateType: typeof dateMs };
      }
    }
    clock.dispose(); disposed = true; clock.dispose();
    let refused = false;
    try { clock.sample(); } catch (e) { refused = e instanceof TypeError && e.message === 'clock_unavailable'; }
    need(refused, 'actual_dispose_is_sticky');
    image.verify(); image.close();
    budgetCheck(performance.now(), operationStart, samples, 0);
    const evidence = { kind: 'disposed', status: 'module_prequalification_observed', actualModuleBound: true,
      rounds: budgets.rounds, samples, comparisons, datePredicates, disposeCalls: 2,
      sampleAfterDisposeRefused: true, operationElapsedMs: performance.now() - operationStart,
      jsElapsedMs: performance.now() - globalStart, checks: { actualTupleParity: true,
        causalCounterContainment: true, causalWallContainment: true, actualWallCounterTypes: true,
        currentImageHeldFile: true, sampleNonregression: true, samplerBounds: true, stickyDisposal: true },
      ...qualifications };
    writeFileSync(resolve(root, 'samples.private.json'), JSON.stringify({ actual, raw, evidence },
      (_, v) => typeof v === 'bigint' ? String(v) : v) + '\n', { flag: 'wx', mode: 0o600 });
    send(evidence); await receive('finish');
    budgetCheck(performance.now(), operationStart, samples, 0);
    need(performance.now() - globalStart < budgets.jsMs, 'original_js_deadline');
  } catch (error) {
    try { send({ kind: 'failure', reason: error?.message === 'clock_unavailable' ? 'actual_create_platform_clock_unavailable' :
      /^[a-z0-9_]{1,80}$/.test(error?.message ?? '') ? error.message : 'actual_module_exception', ...qualifications }); } catch {}
    process.exitCode = 1;
  } finally {
    clearTimeout(watchdog);
    if (clock && !disposed) { try { clock.dispose(); } catch { process.exitCode = 1; } }
    try { image.close(); } catch { process.exitCode = 1; }
    lines?.close(); process.stdin.destroy();
  }
}
if (process.argv[2] === '--private-fixture' && process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  need(process.argv.length === 4 && process.argv[2] === '--private-fixture', 'explicit_parent_fixture_only');
  await execute(process.argv[3]);
}
