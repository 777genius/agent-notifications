import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { inventory, type Manifest } from './basic-release-assets.mts';
import { hostAsset } from './release-canary.mts';
import type { FakeRemote } from './testdata/release-canary/fake-gh.mts';

const tool = fileURLToPath(new URL('./release-canary.mts', import.meta.url));
const fakeGH = fileURLToPath(new URL('./testdata/release-canary/fake-gh.mts', import.meta.url));
const fakeBinary = fileURLToPath(new URL('./testdata/release-canary/fake-binary.mts', import.meta.url));
const C = 'c'.repeat(40), O = 'a'.repeat(40), tag = 'v2.7.19';
const sha = (bytes: Uint8Array | string) => createHash('sha256').update(bytes).digest('hex');
test('local operator selects macOS/Linux assets and refuses unsupported Windows hosts', () => {
  assert.equal(hostAsset('darwin', 'arm64'), 'claude-notifications-darwin-arm64');
  assert.equal(hostAsset('linux', 'x64'), 'claude-notifications-linux-amd64');
  assert.throws(() => hostAsset('win32', 'x64'), /unsupported local canary host; macOS\/Linux/);
  assert.throws(() => hostAsset('win32', 'arm64'), /unsupported local canary host; macOS\/Linux/);
});
function fixture(mode?: 'version' | 'missing' | 'duplicate') {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'TEST-release-canary-contract-'))); chmodSync(root, 0o700);
  const seal = join(root, 'seal'), scratch = join(root, 'scratch'), bin = join(root, 'bin'), remote = join(root, 'remote.json');
  mkdirSync(seal); mkdirSync(join(seal, 'assets')); mkdirSync(scratch, { mode: 0o700 }); mkdirSync(bin);
  const binary = Buffer.from(`#!/bin/sh\nexec '${process.execPath}' '${fakeBinary}' ${mode ? '--TEST-' + mode + ' ' : ''}"$@"\n`);
  const asset = (dir: string, name: string) => { const bytes = readFileSync(join(dir, name)); return { name, size: bytes.length, sha256: sha(bytes) }; };
  for (const name of inventory().filter(name => name !== 'checksums.txt')) {
    writeFileSync(join(seal, 'assets', name), name === hostAsset() ? binary : Buffer.from(`TEST ${name}\n`));
  }
  writeFileSync(join(seal, 'assets/checksums.txt'), inventory().filter(name => name !== 'checksums.txt').map(name =>
    `${asset(join(seal, 'assets'), name).sha256}  ${name}\n`).join(''));
  writeFileSync(join(seal, 'qualification.json'), JSON.stringify({ status: 'basic_five_platform_artifact_checks_complete',
    releaseTag: tag, candidateSHA: C, operatorSHA: O, runID: '43', runAttempt: '2', signingRun: '41', signingAttempt: '3',
    assetCount: 29, platformCount: 5, installedBusinessQualified: false, fullNativeQualified: false,
    visibleDesktopQualified: false, macosE2EQualified: false, publicationAuthorized: false }));
  writeFileSync(join(seal, 'signing.json'), JSON.stringify({ id: 41, run_attempt: 3, head_sha: C, conclusion: 'success', status: 'completed',
    event: 'workflow_dispatch', head_branch: 'release/macos-signing', path: '.github/workflows/macos-qualification.yml',
    actor: { login: '777genius' }, triggering_actor: { login: '777genius' } }));
  writeFileSync(join(seal, 'helper-custody.json'), JSON.stringify({ sourceSHA: C, workflowSHA: C, runID: '41', runAttempt: '3',
    signingTeam: '86399583GS', bundleID: 'com.777genius.agent-notifications', signatureVerified: true,
    archiveSHA256: asset(join(seal, 'assets'), 'ClaudeNotifier.app.zip').sha256 }));
  const m: Manifest = { schema: 1, repository: '777genius/agent-notifications', releaseTag: tag, candidateSHA: C, operatorSHA: O,
    runID: '43', runAttempt: '2', signingRun: '41', signingAttempt: '3', assets: inventory().map(name => asset(join(seal, 'assets'), name)),
    receipts: ['qualification.json', 'signing.json', 'helper-custody.json'].map(name => asset(seal, name)) };
  writeFileSync(join(seal, 'manifest.json'), JSON.stringify(m));
  const state: FakeRemote = { release: { id: 17, tag_name: tag, target_commitish: C, draft: true, prerelease: false },
    tag: { ref: `refs/tags/${tag}`, object: { type: 'commit', sha: C } }, binary: binary.toString('base64'), reads: [],
    assets: m.assets.map((asset, index) => ({ id: asset.name === hostAsset() ? 71 : index + 100, name: asset.name,
      state: 'uploaded', size: asset.size, digest: `sha256:${asset.sha256}` })) };
  writeFileSync(remote, JSON.stringify(state));
  writeFileSync(join(bin, 'gh'), `#!/bin/sh\nexec '${process.execPath}' '${fakeGH}' "$@"\n`, { mode: 0o700 });
  const read = () => JSON.parse(readFileSync(remote, 'utf8')) as FakeRemote;
  const update = (patch: Partial<FakeRemote>) => writeFileSync(remote, JSON.stringify({ ...read(), ...patch }));
  const run = (extra: string[] = [], repository = '777genius/agent-notifications') => new Promise<{ code: number | null; stdout: string; stderr: string }>(accept => {
    const child = spawn(process.execPath, [tool, '--seal', seal, '--release-id', '17', '--scratch', scratch, ...extra],
      { env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, TEST_RELEASE_CANARY_REMOTE: remote,
        GITHUB_REPOSITORY: repository, TEST_RELEASE_CANARY_REPOSITORY: repository,
        GH_TOKEN: 'TEST-NO-REAL-TOKEN', OPENAI_API_KEY: 'TEST-NO-REAL-KEY', PYTHONOPTIMIZE: '1', NODE_OPTIONS: '' }, cwd: root });
    let stdout = '', stderr = ''; child.stdout.on('data', value => stdout += value); child.stderr.on('data', value => stderr += value);
    child.on('close', code => accept({ code, stdout, stderr }));
  });
  return { root, seal, scratch, read, update, run, clean: () => rmSync(root, { recursive: true, force: true }) };
}

test('immutable ID download runs canonical isolated version/Claude+Codex Stop and replay contract', async () => {
  const f = fixture();
  try {
    const before = readFileSync(join(f.seal, 'manifest.json'));
    const result = await f.run(['--state', 'draft', '--asset-id', '71'], 'universal-agent-plugins/agent-notifications'); assert.equal(result.code, 0, result.stderr);
    const report = JSON.parse(result.stdout);
    assert.equal(report.repository, '777genius/agent-notifications');
    assert.equal(report.verifiedRepository, 'universal-agent-plugins/agent-notifications');
    assert.equal(report.status, 'release_canary_passed'); assert.equal(report.before.selected.id, 71);
    assert.equal(report.canary.webhook_deliveries, 2); assert.equal(report.canary.duplicate_replays, 2);
    const commands = readFileSync(join(report.canary.root, 'TEST-commands.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line));
    assert.deepEqual(commands, [['version'], ['handle-hook', 'Stop', '--product', 'claude'], ['handle-hook', 'Stop', '--product', 'claude'],
      ['handle-hook', 'Stop', '--product', 'codex'], ['handle-hook', 'Stop', '--product', 'codex']]);
    assert.deepEqual(f.read().reads.filter(value => value.startsWith('releases/assets/')), ['releases/assets/71']);
    assert.deepEqual(report.before, report.after); assert.deepEqual(readFileSync(join(f.seal, 'manifest.json')), before);
    const second = await f.run(['--receipt', report.receipt]); assert.notEqual(second.code, 0); assert.match(second.stderr, /EEXIST/);
    assert.equal(JSON.parse(readFileSync(report.receipt, 'utf8')).status, 'release_canary_passed');
    f.update({ release: { ...f.read().release, draft: false }, reorderAssetsAfterDownload: true });
    const published = await f.run(['--state', 'public']); assert.equal(published.code, 0, published.stderr);
    assert.equal(JSON.parse(published.stdout).before.draft, false);
  } finally { f.clean(); }
});

test('wrong hashes, oversized bytes and immutable asset replacement fail before execution', async () => {
  for (const mode of ['hash', 'size', 'replacement']) {
    const f = fixture();
    try {
      if (mode === 'replacement') f.update({ replaceAssetAfterDownload: true });
      else { const bytes = Buffer.from(f.read().binary, 'base64'); f.update({ download: (mode === 'size' ? Buffer.concat([bytes, Buffer.from('extra')]) : Buffer.alloc(bytes.length, 88)).toString('base64') }); }
      const receipt = join(f.scratch, 'failed.json'); const result = await f.run(['--receipt', receipt]);
      assert.notEqual(result.code, 0); assert.match(result.stderr, /sealed size|identity changed/);
      const failure = JSON.parse(readFileSync(receipt, 'utf8')); assert.equal(failure.status, 'release_canary_failed');
      assert.equal(existsSync(join(failure.root, 'runtime')), false, 'failed custody executed the local harness');
      assert.equal(f.read().reads.filter(value => value === 'releases/assets/71').length, 1);
    } finally { f.clean(); }
  }
});

test('canonical local harness refuses wrong version, missing delivery and duplicate replay delivery', async () => {
  for (const mode of ['version', 'missing', 'duplicate'] as const) {
    const f = fixture(mode);
    try {
      const receipt = join(f.scratch, 'failed.json');
      const result = await f.run(['--receipt', receipt]); assert.notEqual(result.code, 0, mode);
      assert.match(result.stderr, /local synthetic Stop canary failed/);
      assert.equal(JSON.parse(readFileSync(receipt, 'utf8')).status, 'release_canary_failed');
    } finally { f.clean(); }
  }
});

test('source/state/inventory/asset pins and unsafe scratch reject without downloading', async () => {
  for (const mode of ['tag', 'state', 'inventory', 'asset', 'scratch']) {
    const f = fixture();
    try {
      let extra: string[] = [];
      if (mode === 'tag') f.update({ tag: { ref: `refs/tags/${tag}`, object: { type: 'commit', sha: O } } });
      if (mode === 'state') extra = ['--state', 'public'];
      if (mode === 'inventory') f.update({ assets: f.read().assets.slice(1) });
      if (mode === 'asset') extra = ['--asset-id', '72'];
      if (mode === 'scratch') { const actual = join(f.root, 'real-scratch'); mkdirSync(actual, { mode: 0o700 }); rmSync(f.scratch, { recursive: true }); symlinkSync(actual, f.scratch); }
      const result = await f.run(extra); assert.notEqual(result.code, 0, mode);
      assert.equal(f.read().reads.filter(value => value.startsWith('releases/assets/')).length, 0);
    } finally { f.clean(); }
  }
});
