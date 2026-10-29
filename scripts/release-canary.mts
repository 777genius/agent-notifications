import { createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { chmodSync, closeSync, lstatSync, mkdirSync, mkdtempSync, openSync, readFileSync,
  realpathSync, statfsSync, writeFileSync, writeSync } from 'node:fs';
import { isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { GitHub, need, validate, releaseRepository, type Asset, type Manifest } from './basic-release-assets.mts';

type ObjectValue = Record<string, unknown>;
export interface CanaryInputs {
  seal: string; releaseID: number; scratch: string; state: 'draft' | 'public' | 'either';
  assetID?: number; receipt?: string;
}
export const scope = 'Downloaded immutable macOS/Linux host executable: version and synthetic Claude/Codex Stop loopback delivery, desktop disabled. No real agent turn, native registration, signed-helper execution or publication.';
const hash = (bytes: Uint8Array): string => createHash('sha256').update(bytes).digest('hex');
function object(value: unknown, label: string): ObjectValue {
  need(value !== null && typeof value === 'object' && !Array.isArray(value), `invalid ${label}`);
  return value as ObjectValue;
}
function id(value: unknown, label: string): number {
  need(Number.isSafeInteger(value) && Number(value) > 0, `positive safe ${label} required`);
  return Number(value);
}
function privateDirectory(path: string): string {
  need(isAbsolute(path) && resolve(path) === realpathSync(path), 'scratch must be an absolute path without symlinks');
  const stat = lstatSync(path);
  need(stat.isDirectory() && !stat.isSymbolicLink() &&
    stat.uid === process.getuid?.() && (stat.mode & 0o077) === 0,
  'scratch must be an existing private owned directory');
  return path;
}
function available(path: string): number {
  const stat = statfsSync(path, { bigint: true });
  const bytes = stat.bavail * stat.bsize;
  return Number(bytes > BigInt(Number.MAX_SAFE_INTEGER) ? BigInt(Number.MAX_SAFE_INTEGER) : bytes);
}
export function hostAsset(platform: NodeJS.Platform = process.platform, architecture: string = process.arch): string {
  need(['darwin', 'linux'].includes(platform) && ['arm64', 'x64'].includes(architecture),
    'unsupported local canary host; macOS/Linux arm64 or x64 required');
  return `claude-notifications-${platform}-${architecture === 'x64' ? 'amd64' : 'arm64'}`;
}
function snapshot(gh: GitHub, m: Manifest, input: CanaryInputs, name: string) {
  const release = object(gh.read(`releases/${input.releaseID}`), 'release');
  need(id(release.id, 'release ID') === input.releaseID && release.tag_name === m.releaseTag &&
    release.target_commitish === m.candidateSHA && release.prerelease === false && typeof release.draft === 'boolean',
  'release identity/state differs from seal');
  need(input.state === 'either' || release.draft === (input.state === 'draft'), 'release is not in requested state');
  const tag = object(gh.read(`git/ref/tags/${m.releaseTag}`), 'source tag');
  const target = object(tag.object, 'source tag target');
  need(tag.ref === `refs/tags/${m.releaseTag}` && target.type === 'commit' && target.sha === m.candidateSHA,
    'source tag differs from sealed candidate');
  const remote = gh.assets(input.releaseID);
  need(remote.length === m.assets.length, 'release requires complete sealed asset inventory');
  const names = new Set<string>(), ids = new Set<number>();
  const assets = remote.map(value => {
    const assetID = id(value.id, 'asset ID');
    need(typeof value.name === 'string' && !names.has(value.name) && !ids.has(assetID), 'duplicate remote asset name/ID');
    names.add(value.name); ids.add(assetID);
    const local = m.assets.find(asset => asset.name === value.name);
    need(local && value.size === local.size && value.state === 'uploaded', 'remote asset inventory differs from seal');
    if (value.digest !== undefined && value.digest !== null && value.digest !== '') {
      need(value.digest === `sha256:${local.sha256}`, 'remote asset digest differs from seal');
    }
    return { name: local.name, id: assetID, size: local.size, sha256: local.sha256 };
  });
  assets.sort((a, b) => a.name.localeCompare(b.name));
  const selected = assets.find(asset => asset.name === name); need(selected, 'host executable missing');
  need(input.assetID === undefined || selected.id === input.assetID, 'selected immutable asset ID differs');
  return { releaseID: input.releaseID, draft: release.draft, assets, selected };
}
async function download(repo: string, asset: Asset & { id: number }, output: string): Promise<void> {
  const fd = openSync(output, 'wx', 0o600); const digest = createHash('sha256');
  let bytes = 0;
  try {
    await new Promise<void>((accept, reject) => {
      const child = spawn('gh', ['api', '-H', 'Accept: application/octet-stream', `repos/${repo}/releases/assets/${asset.id}`],
        { stdio: ['ignore', 'pipe', 'pipe'] });
      let failure: Error | undefined;
      // Downloads are bounded by sealed size and a finite transport budget; no retry replays the download.
      const timer = setTimeout(() => { failure = new Error('asset download exceeded 300 second transport budget'); child.kill('SIGKILL'); }, 300_000);
      child.stderr.on('data', () => { /* gh diagnostics may include local auth details; do not retain them. */ });
      child.stdout.on('data', (chunk: Buffer) => {
        if (failure) return;
        try {
          bytes += chunk.length; need(bytes <= asset.size, 'asset download exceeds sealed size');
          let offset = 0;
          while (offset < chunk.length) offset += writeSync(fd, chunk, offset);
          digest.update(chunk);
        } catch (error) { failure = error instanceof Error ? error : new Error('asset write failed'); child.kill('SIGKILL'); }
      });
      child.on('error', error => { failure = error; });
      child.on('close', code => {
        clearTimeout(timer);
        if (failure) reject(failure);
        else if (code !== 0) reject(new Error(`immutable asset download failed (${code})`));
        else accept();
      });
    });
  } finally { closeSync(fd); }
  need(bytes === asset.size && digest.digest('hex') === asset.sha256, 'downloaded bytes differ from sealed size/SHA256');
  chmodSync(output, 0o700);
}
function runtimeEnvironment(root: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {};
  for (const key of ['PATH']) {
    if (process.env[key]) env[key] = process.env[key];
  }
  for (const key of ['HOME', 'USERPROFILE', 'CODEX_HOME', 'CLAUDE_HOME', 'CLAUDE_CONFIG_DIR', 'APPDATA', 'LOCALAPPDATA',
    'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'XDG_RUNTIME_DIR', 'TMPDIR', 'TMP', 'TEMP']) {
    env[key] = join(root, key); mkdirSync(env[key], { mode: 0o700 });
  }
  return env;
}
export async function runCanary(input: CanaryInputs) {
  const selectedName = hostAsset();
  id(input.releaseID, 'release ID'); if (input.assetID !== undefined) id(input.assetID, 'asset ID');
  need(['draft', 'public', 'either'].includes(input.state), 'invalid requested release state');
  const scratch = privateDirectory(input.scratch);
  const m = validate(resolve(input.seal));
  const manifestSHA256 = hash(readFileSync(join(resolve(input.seal), 'manifest.json')));
  const gh = new GitHub(releaseRepository(m.repository));
  const before = snapshot(gh, m, input, selectedName);
  const spaceBefore = available(scratch);
  // The measured transfer is the selected executable only. Profiles/logs have a
  // separate 16 MiB estimate; this is admission headroom, not an enforced quota
  // or measured peak. No fixed multi-gigabyte admission threshold is applied.
  const requiredBytes = before.selected.size + 16 * 1024 * 1024;
  need(spaceBefore >= requiredBytes, `scratch requires ${requiredBytes} bytes; available ${spaceBefore}`);
  const root = mkdtempSync(join(scratch, 'TEST-release-canary-')); chmodSync(root, 0o700);
  const receipt = input.receipt ? resolve(input.receipt) : join(root, 'report.json');
  const receiptFD = openSync(receipt, 'wx', 0o600);
  try {
    const binary = join(root, before.selected.name);
    await download(gh.repo, before.selected, binary);
    const downloaded = snapshot(gh, m, input, before.selected.name);
    need(JSON.stringify(downloaded) === JSON.stringify(before), 'release/asset identity changed during download');
    const runtimeRoot = join(root, 'runtime'); mkdirSync(runtimeRoot, { mode: 0o700 });
    const harness = fileURLToPath(new URL('./release-artifact-e2e.py', import.meta.url));
    const result = spawnSync('python3', ['-I', harness, '--binary', binary,
      '--version', m.releaseTag, '--synthetic-stop-only', '--scratch', runtimeRoot],
    { cwd: runtimeRoot, env: runtimeEnvironment(runtimeRoot), encoding: 'utf8', maxBuffer: 1024 * 1024, timeout: 180_000 });
    need(!result.error && result.status === 0, `local synthetic Stop canary failed: ${result.error?.message ?? result.stderr.slice(-4096)}`);
    const canary = object(JSON.parse(result.stdout.trim()), 'canonical canary receipt');
    need(canary.result === 'PASS' && canary.version === m.releaseTag && canary.binary_sha256 === before.selected.sha256 &&
      canary.webhook_deliveries === 2 && canary.duplicate_replays === 2 &&
      JSON.stringify(canary.products) === '["claude","codex"]' && JSON.stringify(canary.events) === '["Stop","Stop"]',
    'canonical canary receipt differs from requested contract');
    const after = snapshot(gh, m, input, before.selected.name);
    need(JSON.stringify(after) === JSON.stringify(before), 'release/asset identity changed during canary');
    need(hash(readFileSync(binary)) === before.selected.sha256 &&
      hash(readFileSync(join(resolve(input.seal), 'manifest.json'))) === manifestSHA256,
    'local binary or seal manifest changed during canary');
    const report = { status: 'release_canary_passed', at: new Date().toISOString(), scope, repository: m.repository, verifiedRepository: gh.repo,
      releaseTag: m.releaseTag, candidateSHA: m.candidateSHA, manifestSHA256,
      producer: { operatorSHA: m.operatorSHA, runID: m.runID, runAttempt: m.runAttempt },
      root, receipt, scratch: { availableBeforeBytes: spaceBefore, requiredBytes, downloadBytes: before.selected.size,
        profileEstimateBytes: 16 * 1024 * 1024, availableAfterBytes: available(scratch) },
      before, after, canary };
    writeFileSync(receiptFD, JSON.stringify(report, null, 2) + '\n');
    return report;
  } catch (error) {
    writeFileSync(receiptFD, JSON.stringify({ status: 'release_canary_failed', scope, root, receipt,
      error: error instanceof Error ? error.message : 'unknown canary failure' }, null, 2) + '\n');
    throw error;
  } finally { closeSync(receiptFD); }
}
export function parseInputs(argv: string[]): CanaryInputs {
  const args = new Map<string, string>();
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index], value = argv[index + 1];
    need(['--seal', '--release-id', '--scratch', '--state', '--asset-id', '--receipt'].includes(key) &&
      value && !value.startsWith('--') && !args.has(key), 'usage: release-canary.mts --seal DIR --release-id ID --scratch DIR [--state draft|public|either] [--asset-id ID] [--receipt NEW_FILE]');
    args.set(key, value);
  }
  const seal = args.get('--seal'), scratch = args.get('--scratch'); need(seal && scratch, '--seal and --scratch required');
  const state = args.get('--state') ?? 'either'; need(['draft', 'public', 'either'].includes(state), 'invalid --state');
  for (const key of ['--release-id', '--asset-id']) {
    need((key === '--asset-id' && !args.has(key)) || /^[1-9][0-9]*$/.test(args.get(key) ?? ''), `invalid ${key}`);
  }
  return { seal, scratch, state: state as CanaryInputs['state'], releaseID: id(Number(args.get('--release-id')), 'release ID'),
    assetID: args.has('--asset-id') ? id(Number(args.get('--asset-id')), 'asset ID') : undefined, receipt: args.get('--receipt') };
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  Promise.resolve().then(() => runCanary(parseInputs(process.argv.slice(2)))).then(report => console.log(JSON.stringify(report, null, 2)))
    .catch(error => { console.error(error instanceof Error ? error.message : 'release canary failed'); process.exitCode = 1; });
}
