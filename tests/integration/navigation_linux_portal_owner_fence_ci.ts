// One disposable GitHub Actions native TEST invocation; no production client qualification.
import { spawnSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { mkdirSync, mkdtempSync, chmodSync, realpathSync, lstatSync, readFileSync, writeFileSync, readdirSync } from 'node:fs';
import { basename, dirname, join, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

type Json = Record<string, unknown>;
const upstream = 'repos/flatpak/xdg-desktop-portal';
const commit = '1d20fadc304f6601452b5db65ed91197dba77041';
const tag = 'fd69e91052c03fa05ec4fb971328ec9cbe2f4682';
const archiveSHA = 'd4879ddb3d65ff1a8f19187497e6f13dc5d267bcac404a5d501218be355753d3';
const checksumSHA = '432edc2df71e6216f2003d46914c3f7049f9b0d091618c96d291da72e30d485a';
const report: Json = { status: 'failed', scope: 'owned TEST portal owner fence and B cold recovery only',
  passed: false, ownerFenceQualified: false, productionClientActivated: false, navigationQualified: false,
  waylandQualified: false, atomicProviderBindingQualified: false, retryAllowed: false, steps: [] };
let root: string | undefined, tmp: string | undefined, nativeRoot: string | undefined, artifact: string | undefined;
let inputHashes: Record<string, string> = {}, exitCode = 1;
const digest = (bytes: Buffer): string => createHash('sha256').update(bytes).digest('hex');
function bytes(path: string, cap: number): Buffer {
  const st = lstatSync(path);
  if (!st.isFile() || st.isSymbolicLink() || st.size > cap || realpathSync(path) !== path) throw new Error('bounded regular file required');
  const value = readFileSync(path); if (value.length !== st.size || value.length > cap) throw new Error('file changed across bounded read');
  return value;
}
function object(value: unknown): Json {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('JSON object required');
  return value as Json;
}
function json(path: string): Json { return object(JSON.parse(bytes(path, 1048576).toString('utf8'))); }
function command(phase: string, executable: string, args: string[], timeout: number, gh = false, cap = 1048576) {
  if (!root || !tmp) throw new Error('owned controller root absent');
  // Deliberately do not inherit auth, Docker configuration or GitHub command-file variables.
  const env: NodeJS.ProcessEnv = { PATH: process.env.PATH, LANG: 'C.UTF-8', HOME: join(root, 'home'), TMPDIR: tmp,
    XDG_CONFIG_HOME: join(root, 'home', 'config'), XDG_CACHE_HOME: join(root, 'home', 'cache') };
  if (gh) { env.GH_TOKEN = process.env.GH_TOKEN; env.GH_HOST = 'github.com'; env.GH_CONFIG_DIR = join(root, 'gh'); env.GH_PROMPT_DISABLED = '1'; }
  const started = performance.now();
  const value = spawnSync(executable, args, { cwd: root, env, timeout, maxBuffer: cap, killSignal: 'SIGKILL' });
  if (phase === 'native') {
    report.outerActorCollected = !value.error && !value.signal && value.status !== null;
    report.actorOutcomeUnknown = report.outerActorCollected !== true;
  }
  const step = { phase, pid: value.pid, status: value.status, signal: value.signal, error: value.error?.message ?? null,
    elapsedMs: performance.now() - started, stdoutBytes: value.stdout?.length ?? 0, stderrBytes: value.stderr?.length ?? 0,
    stdoutSHA256: digest(value.stdout ?? Buffer.alloc(0)), stderrSHA256: digest(value.stderr ?? Buffer.alloc(0)) };
  (report.steps as unknown[]).push(step);
  if (!gh) for (const stream of ['stdout', 'stderr'] as const) writeFileSync(join(artifact!, `${phase}.${stream}`), value[stream] ?? Buffer.alloc(0), { flag: 'wx' });
  if (value.error || value.signal || value.status !== 0) {
    throw new Error(`${phase} failed; partial stream hashes retained`);
  }
  return value.stdout;
}
function publicJSON(phase: string, path: string): Json {
  return object(JSON.parse(command(phase, 'gh', ['api', path], 30000, true, 65536).toString('utf8')));
}
function ownedNativeRoot(candidate: string): string {
  if (!tmp || dirname(candidate) !== tmp || !/^navigation-linux-portal-test-[a-z0-9_]{8}$/.test(basename(candidate))) throw new Error('native evidence outside owned TMPDIR');
  const st = lstatSync(candidate);
  if (!st.isDirectory() || st.isSymbolicLink() || st.uid !== process.getuid!() || (st.mode & 0o777) !== 0o700 || realpathSync(candidate) !== candidate) throw new Error('native root identity invalid');
  if (!/^Linux portal container TEST [0-9a-f]{32}\n$/.test(bytes(join(candidate, 'container.marker'), 128).toString('utf8'))) throw new Error('native root marker missing');
  return candidate;
}
function collect(): void {
  if (!tmp || !artifact) return;
  if (!nativeRoot) {
    const names = readdirSync(tmp).filter(name => /^navigation-linux-portal-test-[a-z0-9_]{8}$/.test(name));
    if (names.length === 1) nativeRoot = ownedNativeRoot(join(tmp, names[0]!));
  }
  if (!nativeRoot) return;
  const files = ['evidence.json', 'native-evidence.json', 'build-progress.json', 'container.marker',
    ...['build', 'image_identity', 'run', 'container_owner', 'own_container_remove', 'container_absent_by_id', 'container_absent_by_name'].flatMap(label => [`${label}.stdout`, `${label}.stderr`]),
    'runtime/progress.json', 'runtime/monitor.stdout', 'runtime/bus.stderr', 'runtime/pre-click.png',
    ...['A', 'B'].flatMap(label => ['spec.json', 'provider-binding.json', 'provider-after-add.json', 'submitted.json',
      'registry-registered.json', 'removal-refused.json', 'callback-rejected.json', 'receipt.json', 'effect.json',
      'service-start.json', 'action-events.jsonl', 'sender-start.json'].map(name => `runtime/fixtures/${label}/${name}`))];
  const catalog: Json = {}; let total = 0;
  for (const name of files) {
    const source = join(nativeRoot, name);
    try {
      const st = lstatSync(source); if (!st.isFile() || st.isSymbolicLink() || st.size > 2097152 || total + st.size > 16777216) { catalog[name] = { omitted: 'type_or_size_bound', bytes: st.size }; continue; }
      const value = bytes(source, 2097152); const target = join(artifact, 'native', name);
      mkdirSync(dirname(target), { recursive: true, mode: 0o700 }); writeFileSync(target, value, { flag: 'wx' });
      total += value.length; catalog[name] = { bytes: value.length, sha256: digest(value) };
    } catch (error: unknown) { catalog[name] = { unavailable: error instanceof Error ? error.message : String(error) }; }
  }
  report.collectedArtifacts = catalog; report.collectedArtifactBytes = total;
  if (report.passed === true && (!object(catalog['evidence.json']).sha256 || !object(catalog['native-evidence.json']).sha256)) throw new Error('required passing evidence not retained');
}
async function main(): Promise<void> {
  if (process.platform !== 'linux' || process.env.CI !== 'true' || process.env.GITHUB_ACTIONS !== 'true'
      || process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications' || process.env.NAVIGATION_LINUX_OWNER_FENCE_CI !== '1'
      || process.argv.length !== 3 || process.argv[2] !== '--execute-one-owner-fence-test'
      || Number(process.versions.node.split('.')[0]) !== 24 || !process.env.GH_TOKEN
      || !process.getuid || process.getuid() === 0 || !/^[0-9a-f]{40}$/.test(process.env.NAVIGATION_SOURCE_SHA ?? '')) throw new Error('explicit isolated Linux GHA TEST authority required');
  const repo = realpathSync(process.env.GITHUB_WORKSPACE ?? '');
  if (realpathSync(process.cwd()) !== repo || realpathSync(fileURLToPath(import.meta.url)) !== join(repo, 'tests/integration/navigation_linux_portal_owner_fence_ci.ts')) throw new Error('exact checkout controller required');
  root = mkdtempSync(join(realpathSync(process.env.RUNNER_TEMP ?? ''), 'navigation-linux-owner-fence-test-'));
  chmodSync(root, 0o700);
  // Only new controller-owned directories and Python TMPDIR are writable inputs here.
  const nonce = randomUUID(); report.nonce = nonce; report.sourceSHA = process.env.NAVIGATION_SOURCE_SHA;
  for (const name of ['home', 'gh', 'tmp', 'artifact']) mkdirSync(join(root, name), { mode: 0o700 });
  tmp = join(root, 'tmp'); artifact = join(root, 'artifact');
  writeFileSync(join(artifact, '.owned-test-root'), `TEST Linux owner fence CI ${nonce}\n`, { flag: 'wx' });
  if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${artifact}\n`, { flag: 'a' });
  if (command('source', 'git', ['-C', repo, 'rev-parse', 'HEAD'], 5000).toString('utf8').trim() !== report.sourceSHA) throw new Error('checkout SHA mismatch');
  inputHashes = Object.fromEntries(['scripts/navigation-linux-portal-callback-probe.py', 'tests/integration/navigation_linux_portal_test_app.py',
    'tests/integration/navigation-linux-portal.Dockerfile', 'tests/integration/navigation_linux_portal_owner_fence_ci.ts',
    '.github/workflows/navigation-linux-portal-owner-fence.yml'].map(name => [name, digest(bytes(join(repo, name), 1048576))]));
  report.checkoutInputSHA256 = inputHashes;
  const ref = publicJSON('tag-ref', `${upstream}/git/ref/tags/1.22.1`), tagObject = object(ref.object);
  const annotated = publicJSON('annotated-tag', `${upstream}/git/tags/${tag}`), target = object(annotated.object);
  if (tagObject.type !== 'tag' || tagObject.sha !== tag || annotated.sha !== tag || annotated.tag !== '1.22.1'
      || target.type !== 'commit' || target.sha !== commit) throw new Error('publisher annotated tag binding mismatch');
  const archive = publicJSON('archive-metadata', `${upstream}/releases/assets/450403113`);
  const checksum = publicJSON('checksum-metadata', `${upstream}/releases/assets/450403110`);
  for (const [value, id, size, sha] of [[archive, 450403113, 1264992, archiveSHA], [checksum, 450403110, 99, checksumSHA]] as const) {
    if (value.id !== id || value.state !== 'uploaded' || value.size !== size || value.digest !== `sha256:${sha}`
        || typeof value.name !== 'string' || !/^[a-zA-Z0-9._-]{1,128}$/.test(value.name)) throw new Error('fixed publisher asset metadata mismatch');
  }
  const archiveBytes = command('archive-download', 'gh', ['api', '-H', 'Accept: application/octet-stream', `${upstream}/releases/assets/450403113`], 30000, true, 2097152);
  const checksumBytes = command('checksum-download', 'gh', ['api', '-H', 'Accept: application/octet-stream', `${upstream}/releases/assets/450403110`], 30000, true, 65536);
  if (archiveBytes.length !== 1264992 || checksumBytes.length !== 99 || digest(archiveBytes) !== archiveSHA || digest(checksumBytes) !== checksumSHA) throw new Error('publisher source/checksum bytes mismatch');
  const checksumLine = /^([0-9a-f]{64})\s+\*?([^\r\n]+)\r?\n?$/.exec(checksumBytes.toString('utf8'));
  if (!checksumLine || checksumLine[1] !== archiveSHA || checksumLine[2] !== archive.name) throw new Error('publisher checksum file does not bind archive name');
  const provenance = { commit, version: '1.22.1', archiveSHA256: archiveSHA, annotatedTagSHA: tag,
    archiveAsset: { id: archive.id, name: archive.name, bytes: archive.size, digest: archive.digest },
    checksumAsset: { id: checksum.id, name: checksum.name, bytes: checksum.size, digest: checksum.digest },
    ghCommand: `gh api -H 'Accept: application/octet-stream' ${upstream}/releases/assets/450403113`,
    scope: 'publisher digest/checksum and annotated tag; per-file Git blob comparison not repeated in this CI' };
  const archivePath = join(artifact, 'portal-source.tar.xz'), provenancePath = join(artifact, 'portal-source-provenance.json');
  writeFileSync(archivePath, archiveBytes, { flag: 'wx' }); writeFileSync(join(artifact, 'publisher-checksum.txt'), checksumBytes, { flag: 'wx' });
  writeFileSync(provenancePath, JSON.stringify(provenance, null, 2) + '\n', { flag: 'wx' }); report.provenance = provenance;
  writeFileSync(join(artifact, 'native-invocation-intent.json'), JSON.stringify({ nonce, sourceSHA: report.sourceSHA, invocationCount: 1, retryAllowed: false }) + '\n', { flag: 'wx', flush: true });
  report.nativeInvocationAttempted = true;
  report.outerActorCollected = false; report.actorOutcomeUnknown = true;
  const stdout = command('native', 'python3', [join(repo, 'scripts/navigation-linux-portal-callback-probe.py'), '--build-and-execute-test',
    '--restart-owner-fence-test', '--portal-source-archive', archivePath, '--portal-source-sha256', archiveSHA,
    '--portal-source-provenance', provenancePath], 1950000, false, 4194304);
  report.outerActorCollected = true; report.actorOutcomeUnknown = false;
  const summary = object(JSON.parse(stdout.toString('utf8').trim()));
  if (typeof summary.evidence !== 'string' || basename(summary.evidence) !== 'evidence.json') throw new Error('runner final summary invalid');
  nativeRoot = ownedNativeRoot(dirname(summary.evidence));
  const outer = json(join(nativeRoot, 'evidence.json')), native = json(join(nativeRoot, 'native-evidence.json'));
  report.outerEvidenceSHA256 = digest(bytes(join(nativeRoot, 'evidence.json'), 1048576));
  report.nativeEvidenceSHA256 = digest(bytes(join(nativeRoot, 'native-evidence.json'), 1048576));
  const cleanup = object(outer.containerCleanup), counts = object(native.counts), birth = object(native.kernelBirthProof);
  const callback = object(native.callback), rejected = object(native.directTESTActionA), oldRemoval = object(native.refusedOldRemoval);
  if (summary.passed !== true || outer.passed !== true || outer.snapshotUnchanged !== true || outer.ownerFenceQualified !== true
      || cleanup.verified !== true || cleanup.status !== 'owned_id_removed_and_exact_name_absent' || !/^[0-9a-f]{64}$/.test(String(cleanup.ownedContainerID))
      || native.passed !== true || native.ownerFenceQualified !== true || native.helperUnchanged !== true || native.immutableFixturesUnchanged !== true
      || native.scenario !== 'daemon_restart_TEST_owner_fence' || native.passedScope !== 'owned_TEST_owner_fence_and_B_cold_recovery'
      || native.productionClientActivated !== false || native.atomicProviderBindingQualified !== false || native.callbackExited !== true
      || counts.nativeNotify !== 2 || counts.officialRemove !== 0 || counts.nativeClose !== 0 || counts.nativeClick !== 1
      || birth.afterCollectedSenderExit !== true || callback.effectCount !== 1 || callback.targetMatches !== true || callback.providerMatches !== true
      || rejected.genuineClick !== false || rejected.callbackExited !== true || rejected.effectCount !== 0
      || oldRemoval.removeAttempted !== false || native.numericIDReused !== true) throw new Error('actual owner fence, cold B effect or cleanup unproved');
  for (const label of ['senderAExit', 'senderBExit', 'refusedRemovalExit']) {
    const value = object(native[label]); if (value.collected !== true || value.exitCode !== 0) throw new Error('owned sender/refusal collection missing');
  }
  if (!Array.isArray(native.processes) || native.processes.length > 128 || native.processes.some(item => typeof object(item).reapedExitCode !== 'number')) throw new Error('native tracked-child collection unproved');
  const snapshots = object(outer.sourceSHA256);
  for (const [name, source] of [['navigation-linux-portal-callback-probe.py', 'scripts/navigation-linux-portal-callback-probe.py'],
    ['navigation_linux_portal_test_app.py', 'tests/integration/navigation_linux_portal_test_app.py'], ['Dockerfile', 'tests/integration/navigation-linux-portal.Dockerfile']]) {
    if (snapshots[name!] !== inputHashes[source!]) throw new Error('native snapshot source binding mismatch');
  }
  report.status = 'owned_TEST_owner_fence_and_B_cold_recovery'; report.passed = true; report.ownerFenceQualified = true; exitCode = 0;
}
try { await main(); }
catch (error: unknown) { report.error = error instanceof Error ? error.message : String(error); }
finally {
  try { collect(); } catch (error: unknown) { report.collectionError = error instanceof Error ? error.message : String(error); report.passed = false; exitCode = 1; }
  for (const [name, sha] of Object.entries(inputHashes)) try {
    if (digest(bytes(join(realpathSync(process.env.GITHUB_WORKSPACE!), name), 1048576)) !== sha) throw new Error('checkout input changed');
  } catch (error: unknown) { report.inputIntegrityError = error instanceof Error ? error.message : String(error); report.passed = false; exitCode = 1; }
  report.ownerFenceQualified = report.passed === true && report.ownerFenceQualified === true;
  if (report.passed !== true) report.status = 'failed';
  if (artifact) writeFileSync(join(artifact, 'ci-evidence.json'), JSON.stringify(report, null, 2) + '\n', { flag: 'wx' });
  console.log(JSON.stringify(report)); process.exitCode = exitCode;
}
