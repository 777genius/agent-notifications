import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { inventory } from '../basic-release-assets.mts';

const tool = resolve(dirname(fileURLToPath(import.meta.url)), '../basic-release-assets.mts');
const fake = resolve(dirname(fileURLToPath(import.meta.url)), 'fake-gh.mts');
const C = 'c'.repeat(40), O = 'a'.repeat(40), tag = 'v2.7.19';
const digest = (value: string) => createHash('sha256').update(value).digest('hex');
interface State { tag: unknown; release: Record<string, unknown> | null; assets: Array<Record<string, unknown>>; mutations: string[]; [key: string]: unknown }
function fixture() {
  const temp = mkdtempSync(join(tmpdir(), 'TEST-basic-release-assets-')); chmodSync(temp, 0o700);
  const root = join(temp, 'sealed'), statePath = join(temp, 'remote.json');
  mkdirSync(join(root, 'assets'), { recursive: true }); mkdirSync(join(temp, 'bin'));
  writeFileSync(join(temp, 'bin/gh'), `#!/bin/sh\nexec '${process.execPath}' '${fake}' "$@"\n`, { mode: 0o700 });
  const environment: NodeJS.ProcessEnv = { ...process.env, PATH: `${join(temp, 'bin')}:${process.env.PATH}`, TEST_GH_STATE: statePath,
    GITHUB_REPOSITORY: '777genius/agent-notifications', RELEASE_TAG: tag, RELEASE_CANDIDATE_SHA: C,
    RELEASE_ORIGIN_RECEIPT: join(temp, 'origin.json'), RELEASE_ANCESTRY_RECEIPT: join(temp, 'ancestry.json'),
    GITHUB_SHA: O, GITHUB_RUN_ID: '43', GITHUB_RUN_ATTEMPT: '2', RELEASE_SIGNING_RUN: '41',
    RELEASE_ASSET_RUN: '43', RELEASE_ASSET_ATTEMPT: '2' };
  delete environment.GH_TOKEN; delete environment.GITHUB_TOKEN;
  for (const name of inventory().filter(name => name !== 'checksums.txt')) writeFileSync(join(root, 'assets', name), `TEST bytes ${name}\n`);
  const checksums = inventory().filter(n => n !== 'checksums.txt').map(name => `${digest(`TEST bytes ${name}\n`)}  ${name}\n`).join('');
  writeFileSync(join(root, 'assets/checksums.txt'), checksums);
  writeFileSync(join(root, 'qualification.json'), JSON.stringify({ status: 'basic_five_platform_artifact_checks_complete',
    releaseTag: tag, candidateSHA: C, operatorSHA: O, runID: '43', runAttempt: '2', signingRun: '41', signingAttempt: '3',
    assetCount: 29, platformCount: 5, installedBusinessQualified: false, fullNativeQualified: false, visibleDesktopQualified: false,
    macosE2EQualified: false, publicationAuthorized: false }));
  writeFileSync(join(root, 'signing.json'), JSON.stringify({ id: 41, run_attempt: 3, head_sha: C, conclusion: 'success', status: 'completed',
    event: 'workflow_dispatch', head_branch: 'release/macos-signing', path: '.github/workflows/macos-qualification.yml',
    actor: { login: '777genius' }, triggering_actor: { login: '777genius' } }));
  writeFileSync(join(root, 'helper-custody.json'), JSON.stringify({ sourceSHA: C, workflowSHA: C, runID: '41', runAttempt: '3',
    signingTeam: '86399583GS', bundleID: 'com.777genius.agent-notifications', signatureVerified: true,
    archiveSHA256: digest('TEST bytes ClaudeNotifier.app.zip\n') }));
  writeFileSync(join(temp, 'origin.json'), JSON.stringify({ id: 43, run_attempt: 2, head_sha: O,
    event: 'workflow_dispatch', head_branch: 'main', path: '.github/workflows/basic-release.yml',
    actor: { login: '777genius' }, triggering_actor: { login: '777genius' } }));
  writeFileSync(join(temp, 'ancestry.json'), JSON.stringify({ base_commit: { sha: O }, merge_base_commit: { sha: O }, status: 'identical' }));
  writeFileSync(statePath, JSON.stringify({ tag: null, release: null, assets: [], mutations: [] }));
  const run = (stage: string, overrides: Record<string, string> = {}) => spawnSync(process.execPath, [tool, stage, ...(stage === 'unused' ? [] : [root])],
    { cwd: temp, env: { ...environment, ...overrides }, encoding: 'utf8' });
  const read = () => JSON.parse(readFileSync(statePath, 'utf8')) as State;
  const update = (patch: Partial<State>) => writeFileSync(statePath, JSON.stringify({ ...read(), ...patch }));
  const ok = (stage: string) => { const result = run(stage); assert.equal(result.status, 0, result.stderr); };
  const clean = () => { chmodSync(root, 0o700); chmodSync(join(root, 'assets'), 0o700); rmSync(temp, { recursive: true, force: true }); };
  return { root, temp, run, read, update, ok, clean };
}
// A response lost after server acceptance must not cause a duplicate write; an interrupted process
// must resume from the exact seal and reject every conflicting remote object.
test('CLI from outside checkout: immutable seal, partial-upload interruption and exact resume', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('verify'); f.ok('tag'); f.ok('draft');
    const publishedLookup = spawnSync(process.execPath, [fake, 'api', '--include', `repos/777genius/agent-notifications/releases/tags/${tag}`],
      { env: { TEST_GH_STATE: join(f.temp, 'remote.json') }, encoding: 'utf8' });
    assert.equal(publishedLookup.status, 1); assert.match(publishedLookup.stdout, /HTTP\/2.0 404/);
    assert.equal(f.read().release?.draft, true);
    f.update({ interruptAfterUpload: true });
    const failed = f.run('upload'); assert.notEqual(failed.status, 0); assert.match(failed.stderr, /remote read failed/);
    assert.equal(f.read().assets.length, 1);
    const sealBytes = readFileSync(join(f.root, 'manifest.json'));
    f.ok('verify'); f.ok('tag'); f.ok('draft'); f.ok('upload');
    assert.equal(f.read().assets.length, 29);
    assert.equal(f.read().mutations.length, 31);
    assert.equal(new Set(f.read().mutations).size, 31);
    assert.deepEqual(readFileSync(join(f.root, 'manifest.json')), sealBytes);
    f.ok('upload'); assert.equal(f.read().mutations.length, 31);
    for (const overrides of [{ RELEASE_ASSET_ATTEMPT: '1' }, { RELEASE_ASSET_RUN: '44' }, { GITHUB_SHA: C }, { RELEASE_CANDIDATE_SHA: O }] as Record<string, string>[]) {
      assert.notEqual(f.run('verify', overrides).status, 0);
    }
  } finally { f.clean(); }
});
test('lost upload response re-reads server acceptance without duplicate retry', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('tag'); f.ok('draft'); f.update({ loseWriteResponse: true }); f.ok('upload');
    assert.equal(f.read().mutations.filter(x => x.startsWith('upload:')).length, 29);
  } finally { f.clean(); }
});
test('mismatched tag, published draft, unexpected asset, state, size and SHA fail before any mutation', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('tag'); f.ok('draft'); f.ok('upload');
    const good = f.read();
    const variants: Partial<State>[] = [
      { tag: { ref: `refs/tags/${tag}`, object: { type: 'commit', sha: O } } },
      { release: { ...good.release, draft: false } },
      { release: { ...good.release, target_commitish: O } },
      { assets: [...good.assets, { id: 99, name: 'unexpected', state: 'uploaded', size: 1, digest: 'sha256:' + 'a'.repeat(64) }] },
      ...[{ state: 'starter' }, { size: 1 }, { digest: 'sha256:' + '0'.repeat(64) }].map(patch => ({ assets: [{ ...good.assets[0], ...patch }, ...good.assets.slice(1)] })),
    ];
    for (const variant of variants) {
      f.update({ ...good, ...variant }); const before = f.read().mutations;
      const result = f.run('upload'); assert.notEqual(result.status, 0, JSON.stringify(variant));
      assert.deepEqual(f.read().mutations, before);
    }
  } finally { f.clean(); }
});
test('seal rejects changed bytes, missing files and a signing receipt from another attempt', () => {
  const f = fixture();
  try {
    f.ok('seal');
    const asset = join(f.root, 'assets/ClaudeNotifier.app.zip'); chmodSync(asset, 0o600); writeFileSync(asset, 'changed');
    assert.notEqual(f.run('verify').status, 0);
  } finally { f.clean(); }
  const changed = fixture();
  try {
    const receipt = join(changed.root, 'signing.json');
    const signing = JSON.parse(readFileSync(receipt, 'utf8')); signing.run_attempt = 4; writeFileSync(receipt, JSON.stringify(signing));
    assert.notEqual(changed.run('seal').status, 0);
  } finally { changed.clean(); }
  const missing = fixture();
  try { rmSync(join(missing.root, 'assets/checksums.txt')); assert.notEqual(missing.run('seal').status, 0); }
  finally { missing.clean(); }
});

test('missing API digest hashes remote bytes; failed upload retries only twice after proven absence', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('tag'); f.ok('draft');
    const manifest = JSON.parse(readFileSync(join(f.root, 'manifest.json'), 'utf8')) as { assets: Array<{ name: string; size: number }> };
    const assets = manifest.assets.slice(0, -1).map((asset, index) => ({ id: index + 1, name: asset.name,
      size: asset.size, state: 'uploaded', bytes: readFileSync(join(f.root, 'assets', asset.name)).toString('base64') }));
    const bad = [...assets]; bad[0] = { ...bad[0], bytes: Buffer.alloc(bad[0].size, 88).toString('base64') };
    f.update({ assets: bad }); assert.notEqual(f.run('upload').status, 0); assert.equal(f.read().mutations.length, 2);
    f.update({ assets, failUploads: true }); assert.notEqual(f.run('upload').status, 0);
    assert.equal(f.read().uploadAttempts, 2); assert.equal(f.read().assets.length, 28);
    f.update({ failUploads: false, missingDigest: true }); f.ok('upload');
    assert.equal(f.read().uploadAttempts, 3); assert.equal(f.read().assets.length, 29);
  } finally { f.clean(); }
});

test('resume preserves producer O under a descendant controller and rejects wrong origin/attempt/nonancestor', () => {
  const f = fixture(); const controller = 'b'.repeat(40);
  try {
    f.ok('seal');
    const ancestry = { base_commit: { sha: O }, merge_base_commit: { sha: O }, status: 'ahead', total_commits: 1,
      commits: [{ sha: controller }] };
    writeFileSync(join(f.temp, 'ancestry.json'), JSON.stringify(ancestry));
    assert.equal(f.run('verify', { GITHUB_SHA: controller }).status, 0);
    for (const bad of [{ ...ancestry, merge_base_commit: { sha: C } }, { ...ancestry, commits: [{ sha: C }] },
      { ...ancestry, total_commits: 2 }]) {
      writeFileSync(join(f.temp, 'ancestry.json'), JSON.stringify(bad));
      assert.notEqual(f.run('verify', { GITHUB_SHA: controller }).status, 0);
    }
    writeFileSync(join(f.temp, 'ancestry.json'), JSON.stringify(ancestry));
    const origin = JSON.parse(readFileSync(join(f.temp, 'origin.json'), 'utf8'));
    for (const patch of [{ run_attempt: 1 }, { head_sha: controller }, { actor: { login: 'other' } }, { head_branch: 'other' }]) {
      writeFileSync(join(f.temp, 'origin.json'), JSON.stringify({ ...origin, ...patch }));
      assert.notEqual(f.run('verify', { GITHUB_SHA: controller }).status, 0);
    }
    assert.equal(f.read().mutations.length, 0);
  } finally { f.clean(); }
});

// A draft on a later API page must resume; an early match never proves uniqueness.
test('authenticated draft listing paginates past 100 unrelated releases and detects later duplicates', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('tag'); f.ok('draft');
    const good = f.read();
    const older = Array.from({ length: 100 }, (_, i) => ({ id: i + 100, tag_name: `v0.0.${i}`, draft: false }));
    f.update({ otherReleases: older });
    f.ok('draft'); assert.deepEqual(f.read().mutations, good.mutations);
    assert.ok((f.read().reads as string[]).includes('releases?per_page=100&page=2'));
    f.update({ otherReleases: [{ ...good.release, id: 18 }, ...older] });
    const result = f.run('draft'); assert.notEqual(result.status, 0); assert.match(result.stderr, /duplicate remote release tag/);
    assert.deepEqual(f.read().mutations, good.mutations);
  } finally { f.clean(); }
});

// Unknown or incomplete discovery must never be interpreted as absence and create a draft.
// A listing match must also be rechecked directly so stale list state cannot authorize uploads.
test('incomplete listing and changed direct-ID state fail closed before mutation', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('tag'); f.ok('draft'); const good = f.read();
    const page = Array.from({ length: 100 }, (_, i) => ({ id: i + 100, tag_name: `v0.0.${i}` }));
    const fullPages = Array.from({ length: 20 }, (_, p) => page.map((r, i) => ({ ...r, id: 100 + p * 100 + i })));
    const variants: Array<{ patch: Partial<State>; error: RegExp }> = [
      { patch: { releasePages: [null] }, error: /release list incomplete/ },
      { patch: { releasePages: [page], failListPage: 2 }, error: /remote read failed/ },
      { patch: { releasePages: fullPages }, error: /bounded pagination/ },
      { patch: { releasePages: [[good.release, good.release]] }, error: /repeated release listing/ },
      { patch: { directRelease: null }, error: /expected JSON object/ },
      ...[{ id: 18 }, { draft: false }, { prerelease: true }, { tag_name: 'v0.0.1' }, { target_commitish: O }]
        .map(patch => ({ patch: { directRelease: { ...good.release, ...patch } }, error: /remote (draft identity\/state|release id) mismatch/ })),
      { patch: { release: { ...good.release, prerelease: true } }, error: /remote draft identity\/state mismatch/ },
    ];
    for (const { patch, error } of variants) {
      writeFileSync(join(f.temp, 'remote.json'), JSON.stringify({ ...good, ...patch }));
      const result = f.run('draft'); assert.notEqual(result.status, 0); assert.match(result.stderr, error);
      assert.deepEqual(f.read().mutations, good.mutations);
    }
  } finally { f.clean(); }
});

test('remote conflicts introduced during an upload stop the next write', () => {
  const f = fixture();
  try {
    f.ok('seal'); f.ok('tag'); f.ok('draft'); f.update({ addConflictAfterUpload: true });
    const result = f.run('upload'); assert.notEqual(result.status, 0); assert.match(result.stderr, /unexpected remote asset/);
    assert.equal(f.read().uploadAttempts, 1);
    assert.equal(f.read().mutations.length, 3);
  } finally { f.clean(); }
});

// Prebuild absence checks must reject a draft even when it has no Git tag and is
// invisible to the published-only tag endpoint, without needing a local seal.
test('unused CLI rejects any existing tag or release and uncertain discovery without mutations', () => {
  const f = fixture();
  try {
    const empty = f.read(); f.ok('unused');
    const release = { id: 17, tag_name: tag, target_commitish: C, draft: true, prerelease: false };
    const variants: Partial<State>[] = [
      { tag: { ref: `refs/tags/${tag}`, object: { type: 'commit', sha: C } } },
      { release },
      { release: { ...release, draft: false } },
      { release: { ...release, target_commitish: O } },
      { release: { ...release, prerelease: true } },
      { failListPage: 1 },
      { failReads: 1 },
      { releasePages: [null] },
    ];
    for (const variant of variants) {
      writeFileSync(join(f.temp, 'remote.json'), JSON.stringify({ ...empty, ...variant }));
      const result = f.run('unused'); assert.notEqual(result.status, 0, JSON.stringify(variant));
      assert.deepEqual(f.read().mutations, []);
    }
    writeFileSync(join(f.temp, 'remote.json'), JSON.stringify(empty));
    const invalidInputs: Record<string, string>[] = [{ GITHUB_REPOSITORY: 'other/repo' }, { RELEASE_TAG: 'unstable' }, { RELEASE_CANDIDATE_SHA: 'main' }];
    for (const overrides of invalidInputs) {
      assert.notEqual(f.run('unused', overrides).status, 0);
      assert.deepEqual(f.read().mutations, []);
    }
  } finally { f.clean(); }
});
