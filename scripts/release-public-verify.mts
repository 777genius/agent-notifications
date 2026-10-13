import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { GitHub, need, validate, type Manifest } from './basic-release-assets.mts';
import { parseChannels } from './release-preflight.mts';

type ObjectValue = Record<string, unknown>;
export interface PublicReleaseInputs {
  sealedDirectory: string;
  mainSHA: string;
  loaderURL: string;
  pagesRunID: string;
}
export const scope = {
  verified: 'Public release bytes, Latest identity, channel/source references, exact Pages deployment and public loader snapshot only.',
  actualInstallation: 'Separate evidence required; no installer, native agent or notification delivery is executed.',
  delivery: 'Separate business/native/visible-desktop evidence required; this check does not upgrade the basic qualification receipt.',
  remoteEffects: 'Read-only GitHub API and HTTP GET requests.',
};
const hash = (bytes: Uint8Array | string): string => createHash('sha256').update(bytes).digest('hex');
function object(value: unknown, reason: string): ObjectValue {
  need(value !== null && typeof value === 'object' && !Array.isArray(value), reason);
  return value as ObjectValue;
}
function positiveID(value: unknown, reason: string): number {
  need(Number.isSafeInteger(value) && Number(value) > 0, reason); return Number(value);
}
function read(gh: GitHub, endpoint: string): ObjectValue {
  return object(gh.read(endpoint), `missing/invalid remote object: ${endpoint}`);
}
function ref(gh: GitHub, kind: 'heads' | 'tags', name: string, sha: string): ObjectValue {
  const value = read(gh, `git/ref/${kind}/${name}`);
  const target = object(value.object, `invalid ref target: ${name}`);
  need(value.ref === `refs/${kind}/${name}` && target.type === 'commit' && target.sha === sha,
    `remote ${kind}/${name} must point directly to ${sha}`);
  return { ref: value.ref, sha: target.sha };
}
function releaseIdentity(value: ObjectValue, m: Manifest, label: string): number {
  need(value.tag_name === m.releaseTag && value.target_commitish === m.candidateSHA &&
    value.draft === false && value.prerelease === false, `${label} release identity/state mismatch`);
  return positiveID(value.id, `${label} release id required`);
}
function source(gh: GitHub, path: string, sha: string): Buffer {
  const file = read(gh, `contents/${path}?ref=${sha}`);
  need(file.type === 'file' && file.path === path && file.encoding === 'base64' && typeof file.content === 'string',
    `invalid exact-controller source: ${path}`);
  const bytes = Buffer.from(file.content, 'base64');
  need(bytes.length > 0, `empty exact-controller source: ${path}`);
  return bytes;
}
async function httpBytes(url: URL): Promise<Buffer> {
  const response = await fetch(url, { signal: AbortSignal.timeout(30_000), redirect: 'error', cache: 'no-store' });
  need(response.ok, `public route HTTP ${response.status}: ${url.pathname}`);
  return Buffer.from(await response.arrayBuffer());
}
function stagedLoader(bytes: Buffer, sha: string): Buffer {
  const marker = 'controller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"';
  const body = bytes.toString('utf8');
  need(body.split(marker).length === 2 && body.startsWith('#!/usr/bin/env bash\n') &&
    !body.includes('\0') && bytes.length <= 262144, 'invalid controller loader staging contract');
  return Buffer.from(body.replace(marker, `controller="\${BOOTSTRAP_CONTROLLER_COMMIT:-${sha}}"`));
}

export async function verifyPublicRelease(input: PublicReleaseInputs) {
  need(/^[a-f0-9]{40}$/.test(input.mainSHA), 'explicit lowercase 40-character MAIN_SHA required');
  need(/^[1-9][0-9]*$/.test(input.pagesRunID), 'explicit PAGES_RUN_ID required');
  const url = new URL(input.loaderURL);
  need(url.protocol === 'https:' || (url.protocol === 'http:' && ['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname)),
    'public loader requires HTTPS (HTTP allowed only for local test servers)');
  need(!url.username && !url.password, 'loader URL must not contain credentials');
  const m = validate(resolve(input.sealedDirectory));
  const gh = new GitHub(m.repository);
  const mainBefore = ref(gh, 'heads', 'main', input.mainSHA);
  const release = read(gh, `releases/tags/${m.releaseTag}`);
  const releaseID = releaseIdentity(release, m, 'public');
  const latest = read(gh, 'releases/latest');
  need(releaseIdentity(latest, m, 'Latest') === releaseID, 'Latest must identify the exact public release');
  const tag = ref(gh, 'tags', m.releaseTag, m.candidateSHA);
  const remote = gh.assets(releaseID);
  need(remote.length === m.assets.length, 'public release requires all 29 assets and no extras');
  const seen = new Set<string>();
  const assets = remote.map(asset => {
    need(typeof asset.name === 'string' && !seen.has(asset.name), 'duplicate/invalid public asset name');
    seen.add(asset.name);
    const local = m.assets.find(item => item.name === asset.name);
    need(local, `unexpected public asset: ${asset.name}`);
    positiveID(asset.id, `public asset id required: ${asset.name}`);
    need(asset.state === 'uploaded' && asset.size === local.size, `public asset state/size mismatch: ${asset.name}`);
    const digest = gh.digest(asset);
    need(digest === local.sha256, `public asset digest mismatch: ${asset.name}`);
    return { name: asset.name, id: asset.id, size: asset.size, sha256: digest,
      digestEvidence: typeof asset.digest === 'string' && /^sha256:[a-f0-9]{64}$/.test(asset.digest) ? 'GitHub API' : 'downloaded bytes' };
  });
  const channelsBytes = source(gh, 'release-channels.tsv', input.mainSHA);
  const channels = parseChannels(channelsBytes.toString('utf8'));
  for (const row of channels) need(row.tag === m.releaseTag && row.release === m.candidateSHA && row.source === m.candidateSHA,
    `channel ${row.key} must select ${m.releaseTag} with release/source ${m.candidateSHA}`);
  const sourceRefs = [...new Set(channels.map(row => row.ref))].map(name => ref(gh, 'heads', name, m.candidateSHA));
  const sourceTag = ref(gh, 'tags', `dist/platform-source/${m.candidateSHA}`, m.candidateSHA);
  const controller = ['bin/setup.sh', 'bin/bootstrap.sh', 'bin/release-channel.sh'].map(path => {
    const bytes = source(gh, path, input.mainSHA); return { path, bytes, sha256: hash(bytes) };
  });
  const expectedLoader = stagedLoader(controller[0]!.bytes, input.mainSHA);
  const pages = read(gh, `actions/runs/${input.pagesRunID}`);
  need(String(pages.id) === input.pagesRunID && pages.head_sha === input.mainSHA && pages.head_branch === 'main' &&
    pages.event === 'push' && pages.path === '.github/workflows/landing.yml' &&
    pages.status === 'completed' && pages.conclusion === 'success', 'Pages run must be successful Landing push for the exact main SHA');
  positiveID(pages.run_attempt, 'Pages run attempt required');
  const jobsResponse = read(gh, `actions/runs/${input.pagesRunID}/attempts/${pages.run_attempt}/jobs?per_page=100`);
  need(Array.isArray(jobsResponse.jobs) && jobsResponse.jobs.length < 100 &&
    jobsResponse.total_count === jobsResponse.jobs.length, 'complete exact-attempt Pages jobs required');
  const jobs = jobsResponse.jobs.map(value => object(value, 'invalid Pages job'));
  for (const name of ['verify', 'deploy']) {
    const matching = jobs.filter(job => job.name === name);
    need(matching.length === 1 && matching[0]!.head_sha === input.mainSHA && matching[0]!.status === 'completed' &&
      matching[0]!.conclusion === 'success', `Pages ${name} job must succeed for the exact main SHA`);
  }
  const loader = await httpBytes(url);
  need(loader.equals(expectedLoader), `public loader bytes differ from staged controller ${input.mainSHA}`);
  const legacyRef = read(gh, 'git/ref/heads/gh-pages');
  const legacyTarget = object(legacyRef.object, 'invalid legacy Pages ref');
  need(legacyRef.ref === 'refs/heads/gh-pages' && legacyTarget.type === 'commit' &&
    typeof legacyTarget.sha === 'string' && /^[a-f0-9]{40}$/.test(legacyTarget.sha), 'exact legacy Pages source required');
  const legacySetup = source(gh, 'setup.sh', legacyTarget.sha);
  const publicSetup = await httpBytes(new URL('/setup.sh', url));
  need(publicSetup.equals(legacySetup), 'legacy /setup.sh bytes differ from gh-pages source');
  const mainAfter = ref(gh, 'heads', 'main', input.mainSHA);
  return {
    status: 'public_release_verified', verifiedAt: new Date().toISOString(),
    sealManifestSHA256: hash(readFileSync(resolve(input.sealedDirectory, 'manifest.json'))), scope, repository: m.repository, releaseTag: m.releaseTag,
    candidateSHA: m.candidateSHA, sourceSHA: m.candidateSHA, mainSHA: input.mainSHA,
    producer: { runID: m.runID, runAttempt: m.runAttempt, operatorSHA: m.operatorSHA },
    publicRelease: { id: releaseID, latestID: latest.id, tag, assetCount: assets.length, assets },
    channels: { sha256: hash(channelsBytes), rows: channels, sourceRefs, immutableSourceTag: sourceTag },
    controller: controller.map(({ path, sha256 }) => ({ path, sha256 })),
    pages: { runID: input.pagesRunID, runAttempt: pages.run_attempt, headSHA: pages.head_sha,
      workflow: pages.path, event: pages.event, conclusion: pages.conclusion,
      jobs: jobs.map(job => ({ name: job.name, headSHA: job.head_sha, conclusion: job.conclusion })) },
    loader: { url: url.href, size: loader.length, sha256: hash(loader), expectedSHA256: hash(expectedLoader) },
    retainedLegacy: { sourceSHA: legacyTarget.sha,
      setup: { path: '/setup.sh', sha256: hash(publicSetup), expectedSHA256: hash(legacySetup) } },
    mainBefore, mainAfter,
  };
}
export async function main(argv: string[]): Promise<void> {
  need(argv.length === 4, 'usage: release-public-verify.mts SEALED_DIR MAIN_SHA PUBLIC_LOADER_URL PAGES_RUN_ID');
  const [sealedDirectory, mainSHA, loaderURL, pagesRunID] = argv as [string, string, string, string];
  console.log(JSON.stringify(await verifyPublicRelease({ sealedDirectory, mainSHA, loaderURL, pagesRunID }), null, 2));
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch(error => {
    console.error(JSON.stringify({ status: 'public_release_verification_failed', scope,
      error: error instanceof Error ? error.message : 'unknown verification failure' }, null, 2));
    process.exitCode = 1;
  });
}
