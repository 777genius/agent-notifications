import { createHash } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import { chmodSync, closeSync, lstatSync, mkdtempSync, openSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export interface Asset { name: string; size: number; sha256: string }
export interface Manifest {
  schema: 1; repository: string; releaseTag: string; candidateSHA: string; operatorSHA: string;
  runID: string; runAttempt: string; signingRun: string; signingAttempt: string;
  assets: Asset[]; receipts: Asset[];
}
type ObjectValue = Record<string, unknown>;
const receipts = ['qualification.json', 'signing.json', 'helper-custody.json'];
const sha = /^[a-f0-9]{64}$/;
const commit = /^[a-f0-9]{40}$/;
const decimal = /^[1-9][0-9]*$/;
const semver = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
export function need(value: unknown, reason: string): asserts value {
  if (!value) throw new Error(reason);
}
function object(value: unknown): ObjectValue {
  need(value !== null && typeof value === 'object' && !Array.isArray(value), 'expected JSON object');
  return value as ObjectValue;
}
function json(path: string): ObjectValue { return object(JSON.parse(readFileSync(path, 'utf8'))); }
function env(name: string): string { const value = process.env[name]; need(value, `missing ${name}`); return value; }
function hash(bytes: Buffer | string): string { return createHash('sha256').update(bytes).digest('hex'); }
export function inventory(): string[] {
  const names: string[] = [];
  for (const target of ['linux-amd64', 'linux-arm64', 'windows-amd64', 'darwin-amd64', 'darwin-arm64']) {
    for (const command of ['claude-notifications', 'sound-preview', 'list-devices', 'list-sounds']) {
      names.push(`${command}-${target}${target.startsWith('windows') ? '.exe' : ''}`);
    }
    names.push(`agent-notify-portable-${target}.zip`);
  }
  return [...names, 'claude-notifications-windows-amd64-focus.exe', 'ClaudeNotifier.app.zip', 'THIRD_PARTY_NOTICES.txt', 'checksums.txt'].sort();
}
function leaf(root: string, name: string): Asset {
  need(!name.includes('/') && !name.includes('\\') && name !== '.' && name !== '..', 'unsafe leaf');
  const path = join(root, name); const stat = lstatSync(path);
  need(stat.isFile() && !stat.isSymbolicLink() && stat.size > 0, `nonempty regular file required: ${name}`);
  return { name, size: stat.size, sha256: hash(readFileSync(path)) };
}
function exactNames(root: string, names: string[]): void {
  need(JSON.stringify(readdirSync(root).sort()) === JSON.stringify([...names].sort()), `unexpected inventory: ${root}`);
}
function validateIdentity(m: ObjectValue): void {
  need(m.schema === 1 && m.repository === '777genius/agent-notifications', 'unsupported sealed repository/schema');
  need(typeof m.releaseTag === 'string' && semver.test(m.releaseTag), 'stable semver tag required');
  need(typeof m.candidateSHA === 'string' && commit.test(m.candidateSHA), 'exact candidate SHA required');
  need(typeof m.operatorSHA === 'string' && commit.test(m.operatorSHA), 'exact operator SHA required');
  for (const key of ['runID', 'runAttempt', 'signingRun', 'signingAttempt']) {
    need(typeof m[key] === 'string' && decimal.test(m[key]), `invalid ${key}`);
  }
}
function validateReceipts(root: string, m: Manifest): void {
  const report = json(join(root, 'qualification.json'));
  need(report.status === 'basic_five_platform_artifact_checks_complete' && report.releaseTag === m.releaseTag &&
    report.candidateSHA === m.candidateSHA && report.operatorSHA === m.operatorSHA &&
    report.runID === m.runID && report.runAttempt === m.runAttempt &&
    report.signingRun === m.signingRun && report.signingAttempt === m.signingAttempt &&
    report.assetCount === 29 && report.platformCount === 5, 'qualification receipt identity mismatch');
  for (const key of ['installedBusinessQualified', 'fullNativeQualified', 'visibleDesktopQualified', 'macosE2EQualified', 'publicationAuthorized']) {
    need(report[key] === false, `invalid basic qualification scope: ${key}`);
  }
  const signing = json(join(root, 'signing.json'));
  need(String(signing.id) === m.signingRun && String(signing.run_attempt) === m.signingAttempt &&
    signing.head_sha === m.candidateSHA && signing.conclusion === 'success' && signing.status === 'completed' &&
    signing.event === 'workflow_dispatch' && signing.head_branch === 'release/macos-signing' &&
    signing.path === '.github/workflows/macos-qualification.yml' &&
    object(signing.actor).login === '777genius' && object(signing.triggering_actor).login === '777genius',
    'owner same-source signing receipt required');
  const custody = json(join(root, 'helper-custody.json'));
  need(custody.sourceSHA === m.candidateSHA && custody.workflowSHA === m.candidateSHA &&
    custody.runID === m.signingRun && custody.runAttempt === m.signingAttempt &&
    custody.signingTeam === '86399583GS' && custody.bundleID === 'com.777genius.agent-notifications' &&
    custody.signatureVerified === true && custody.archiveSHA256 === m.assets.find(a => a.name === 'ClaudeNotifier.app.zip')?.sha256,
    'same-run signed helper receipt required');
}
export function validate(root: string, expected: Partial<Manifest> = {}): Manifest {
  need(lstatSync(root).isDirectory() && !lstatSync(root).isSymbolicLink(), 'regular sealed directory required');
  exactNames(root, ['assets', 'manifest.json', ...receipts]);
  need(lstatSync(join(root, 'assets')).isDirectory() && !lstatSync(join(root, 'assets')).isSymbolicLink(), 'regular assets directory required');
  leaf(root, 'manifest.json');
  const data = json(join(root, 'manifest.json')); validateIdentity(data);
  const m = data as unknown as Manifest;
  for (const [key, value] of Object.entries(expected)) need(data[key] === value, `sealed ${key} mismatch`);
  exactNames(join(root, 'assets'), inventory());
  for (const [items, names, dir] of [[m.assets, inventory(), join(root, 'assets')], [m.receipts, receipts, root]] as const) {
    need(Array.isArray(items) && items.length === names.length, 'manifest inventory count mismatch');
    need(JSON.stringify(items.map(a => a.name).sort()) === JSON.stringify([...names].sort()), 'manifest names mismatch');
    for (const a of items) {
      need(Number.isSafeInteger(a.size) && a.size > 0 && sha.test(a.sha256), 'invalid asset metadata');
      need(JSON.stringify(leaf(dir, a.name)) === JSON.stringify(a), `sealed bytes changed: ${a.name}`);
    }
  }
  const checksums = m.assets.filter(a => a.name !== 'checksums.txt').map(a => `${a.sha256}  ${a.name}\n`).sort().join('');
  need(readFileSync(join(root, 'assets/checksums.txt'), 'utf8').split('\n').filter(Boolean).sort().join('\n') + '\n' ===
    checksums.split('\n').filter(Boolean).sort().join('\n') + '\n', 'checksums contract mismatch');
  validateReceipts(root, m);
  return m;
}
export function seal(root: string): Manifest {
  exactNames(root, ['assets', ...receipts]);
  exactNames(join(root, 'assets'), inventory());
  const signing = json(join(root, 'signing.json'));
  const m: Manifest = { schema: 1, repository: env('GITHUB_REPOSITORY'), releaseTag: env('RELEASE_TAG'),
    candidateSHA: env('RELEASE_CANDIDATE_SHA'), operatorSHA: env('GITHUB_SHA'),
    runID: env('GITHUB_RUN_ID'), runAttempt: env('GITHUB_RUN_ATTEMPT'),
    signingRun: env('RELEASE_SIGNING_RUN'), signingAttempt: String(signing.run_attempt),
    assets: inventory().map(name => leaf(join(root, 'assets'), name)), receipts: receipts.map(name => leaf(root, name)) };
  validateIdentity(m as unknown as ObjectValue);
  writeFileSync(join(root, 'manifest.json'), JSON.stringify(m, null, 2) + '\n', { flag: 'wx' });
  validate(root);
  for (const a of m.assets) chmodSync(join(root, 'assets', a.name), 0o444);
  for (const a of [...m.receipts, { name: 'manifest.json' }]) chmodSync(join(root, a.name), 0o444);
  chmodSync(join(root, 'assets'), 0o555); chmodSync(root, 0o555);
  return m;
}

// Every mutation is preceded and followed by a read. A failed write may have succeeded remotely.
// Never retry a write until a successful read proves the intended object is still absent.
export class GitHub {
  repo: string;
  private digestCache = new Map<number, string>();
  constructor(repo: string) { this.repo = repo; }
  call(args: string[]): Buffer {
    return execFileSync('gh', args, { maxBuffer: 128 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] });
  }
  read(endpoint: string): unknown | null {
    const response = spawnSync('gh', ['api', '--include', `repos/${this.repo}/${endpoint}`], { encoding: 'utf8', maxBuffer: 4 * 1024 * 1024 });
    if (response.error) throw response.error;
    const stdout = response.stdout ?? '';
    const status = /^HTTP\/\S+\s+(\d+)/m.exec(stdout)?.[1];
    if (response.status !== 0) {
      if (status === '404') return null;
      throw new Error(`remote read failed (${status ?? response.status}): ${endpoint}`);
    }
    need(status === '200', `unexpected read status ${status}`);
    const separator = /\r?\n\r?\n/.exec(stdout); need(separator, 'missing HTTP response body');
    return JSON.parse(stdout.slice(separator.index + separator[0].length));
  }
  assets(releaseID: number): ObjectValue[] {
    const result = this.read(`releases/${releaseID}/assets?per_page=100`);
    need(Array.isArray(result) && result.length < 100, 'asset list incomplete');
    return result.map(object);
  }
  digest(asset: ObjectValue): string {
    if (typeof asset.digest === 'string' && /^sha256:[a-f0-9]{64}$/.test(asset.digest)) return asset.digest.slice(7);
    const id = number(asset.id, 'asset id required');
    // GitHub release asset IDs identify immutable uploaded bytes; deletion/re-upload creates a new ID.
    const cached = this.digestCache.get(id); if (cached) return cached;
    const temp = mkdtempSync(join(tmpdir(), 'basic-release-asset-'));
    const path = join(temp, 'remote'); const fd = openSync(path, 'wx', 0o600);
    try {
      const result = spawnSync('gh', ['api', '-H', 'Accept: application/octet-stream', `repos/${this.repo}/releases/assets/${asset.id}`],
        { stdio: ['ignore', fd, 'pipe'] });
      if (result.error) throw result.error;
      need(result.status === 0, `asset download failed: ${asset.name}`);
      const digest = hash(readFileSync(path)); this.digestCache.set(id, digest); return digest;
    } finally { closeSync(fd); rmSync(temp, { recursive: true, force: true }); }
  }
}
function number(value: unknown, reason: string): number { need(Number.isSafeInteger(value) && Number(value) > 0, reason); return Number(value); }
type ReleaseIdentity = Pick<Manifest, 'releaseTag' | 'candidateSHA'>;
function tagState(gh: GitHub, m: ReleaseIdentity): boolean {
  const tag = gh.read(`git/ref/tags/${m.releaseTag}`); if (tag === null) return false;
  const ref = object(tag); const target = object(ref.object);
  need(ref.ref === `refs/tags/${m.releaseTag}` && target.type === 'commit' && target.sha === m.candidateSHA, 'remote immutable tag mismatch');
  return true;
}
function draftIdentity(value: unknown, m: ReleaseIdentity): ObjectValue {
  const release = object(value);
  need(release.tag_name === m.releaseTag && release.draft === true && release.prerelease === false &&
    release.target_commitish === m.candidateSHA, 'remote draft identity/state mismatch');
  number(release.id, 'release id required');
  return release;
}
function releaseState(gh: GitHub, m: ReleaseIdentity): ObjectValue | null {
  // The tag endpoint only returns published releases. Authenticated listing includes drafts.
  // Exhaust the bounded listing before accepting absence or uniqueness, even after a match.
  const ids = new Set<number>(); let match: ObjectValue | null = null;
  for (let page = 1; page <= 20; page++) {
    const values = gh.read(`releases?per_page=100&page=${page}`);
    need(Array.isArray(values) && values.length <= 100, 'release list incomplete');
    for (const value of values) {
      const release = object(value); const id = number(release.id, 'release id required');
      need(typeof release.tag_name === 'string' && !ids.has(id), 'invalid or repeated release listing');
      ids.add(id);
      if (release.tag_name === m.releaseTag) {
        need(match === null, 'duplicate remote release tag');
        match = draftIdentity(release, m);
      }
    }
    if (values.length < 100) {
      if (!match) return null;
      const id = number(match.id, 'release id required');
      const release = draftIdentity(gh.read(`releases/${id}`), m);
      need(release.id === id, 'remote release id mismatch');
      return release;
    }
  }
  throw new Error('release list exceeds bounded pagination');
}
function checkRemote(gh: GitHub, m: Manifest, release: ObjectValue): Set<string> {
  const seen = new Set<string>(); const expected = new Map(m.assets.map(a => [a.name, a]));
  for (const remote of gh.assets(number(release.id, 'release id required'))) {
    need(typeof remote.name === 'string' && !seen.has(remote.name), 'duplicate remote asset');
    seen.add(remote.name); const local = expected.get(remote.name); need(local, `unexpected remote asset: ${remote.name}`);
    need(remote.state === 'uploaded' && remote.size === local.size && gh.digest(remote) === local.sha256,
      `remote asset mismatch: ${remote.name}`);
  }
  return seen;
}
function boundedWrite(read: () => boolean, write: () => void): void {
  for (let attempt = 0; attempt < 2; attempt++) {
    if (read()) return;
    let failure: unknown;
    try { write(); } catch (error) { failure = error; }
    if (read()) return;
    if (attempt === 1) throw failure ?? new Error('remote write not visible after bounded retry');
  }
}
export function reconcile(root: string, stage: 'tag' | 'draft' | 'upload', gh: GitHub, m: Manifest): void {
  if (stage === 'tag') {
    const release = releaseState(gh, m);
    if (release) { need(tagState(gh, m), 'draft without matching tag'); checkRemote(gh, m, release); }
    boundedWrite(() => tagState(gh, m), () => { gh.call(['api', '--method', 'POST', `repos/${gh.repo}/git/refs`,
      '-f', `ref=refs/tags/${m.releaseTag}`, '-f', `sha=${m.candidateSHA}`]); });
    return;
  }
  need(tagState(gh, m), 'matching tag required');
  if (stage === 'draft') {
    boundedWrite(() => { const r = releaseState(gh, m); if (!r) return false; checkRemote(gh, m, r); return true; }, () => {
      gh.call(['release', 'create', m.releaseTag, '--repo', gh.repo, '--verify-tag', '--target', m.candidateSHA,
        '--draft', '--prerelease=false', '--latest=false', '--title', m.releaseTag, '--notes',
        'Basic five-platform artifact checks and same-source signed macOS custody only. Publication requires separate current-head review, draft-asset canaries and macOS/Codex E2E evidence. No full native, visible desktop or cold-start qualification.']);
    });
    return;
  }
  const initialRelease = releaseState(gh, m); need(initialRelease, 'draft required');
  const present = checkRemote(gh, m, initialRelease);
  // Verified existing assets need no writes. Every missing asset still gets fresh full checks,
  // and the final scan detects conflicts even when this stage has no mutations to perform.
  for (const asset of m.assets.filter(asset => !present.has(asset.name))) {
    boundedWrite(() => {
      need(tagState(gh, m), 'tag changed during upload');
      const release = releaseState(gh, m); need(release, 'draft required');
      return checkRemote(gh, m, release).has(asset.name);
    }, () => { gh.call(['release', 'upload', m.releaseTag, '--repo', gh.repo, join(root, 'assets', asset.name)]); });
  }
  need(tagState(gh, m), 'tag changed after upload');
  const release = releaseState(gh, m); need(release && checkRemote(gh, m, release).size === 29, 'complete draft required');
}
export function validateOrigin(m: Manifest, origin: ObjectValue, comparison: ObjectValue, controller: string): void {
  need(commit.test(controller), 'exact controller SHA required');
  need(String(origin.id) === m.runID && String(origin.run_attempt) === m.runAttempt &&
    origin.head_sha === m.operatorSHA && origin.event === 'workflow_dispatch' && origin.head_branch === 'main' &&
    origin.path === '.github/workflows/basic-release.yml' && object(origin.actor).login === '777genius' &&
    object(origin.triggering_actor).login === '777genius', 'owner exact producer run/attempt required');
  need(object(comparison.base_commit).sha === m.operatorSHA && object(comparison.merge_base_commit).sha === m.operatorSHA,
    'producer must be ancestor of current controller');
  if (m.operatorSHA === controller) need(comparison.status === 'identical', 'controller comparison mismatch');
  else {
    need(comparison.status === 'ahead' && Array.isArray(comparison.commits) && comparison.commits.length > 0 &&
      comparison.total_commits === comparison.commits.length && object(comparison.commits.at(-1)).sha === controller,
      'complete producer to controller ancestry required');
  }
}
export function main(argv: string[]): void {
  const [stage, directory] = argv;
  if (stage === 'unused') {
    need(argv.length === 1, 'usage: basic-release-assets.mts unused');
    const repo = env('GITHUB_REPOSITORY');
    need(repo === '777genius/agent-notifications', 'unsupported repository');
    const identity: ReleaseIdentity = { releaseTag: env('RELEASE_TAG'), candidateSHA: env('RELEASE_CANDIDATE_SHA') };
    need(semver.test(identity.releaseTag) && commit.test(identity.candidateSHA), 'stable semver tag and exact candidate SHA required');
    const gh = new GitHub(repo);
    need(!tagState(gh, identity), 'release tag already exists');
    need(releaseState(gh, identity) === null, 'release already exists');
    return;
  }
  need(directory && argv.length === 2, 'usage: basic-release-assets.mts seal|verify|tag|draft|upload SEALED_DIR');
  const root = resolve(directory);
  if (stage === 'seal') { seal(root); return; }
  const m = validate(root, { repository: env('GITHUB_REPOSITORY'), releaseTag: env('RELEASE_TAG'),
    candidateSHA: env('RELEASE_CANDIDATE_SHA'), signingRun: env('RELEASE_SIGNING_RUN'),
    runID: env('RELEASE_ASSET_RUN'), runAttempt: env('RELEASE_ASSET_ATTEMPT') });
  validateOrigin(m, json(env('RELEASE_ORIGIN_RECEIPT')), json(env('RELEASE_ANCESTRY_RECEIPT')), env('GITHUB_SHA'));
  console.log(JSON.stringify({ stage, producerSHA: m.operatorSHA, controllerSHA: env('GITHUB_SHA'),
    manifestSHA256: hash(readFileSync(join(root, 'manifest.json'))), runID: m.runID, runAttempt: m.runAttempt }));
  if (stage === 'verify') return;
  need(stage === 'tag' || stage === 'draft' || stage === 'upload', 'unknown stage');
  reconcile(root, stage, new GitHub(m.repository), m);
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try { main(process.argv.slice(2)); } catch (error) { console.error(error instanceof Error ? error.message : error); process.exitCode = 1; }
}
