import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { inventory, validate, type Asset, type Manifest } from './basic-release-assets.mts';

const scripts = dirname(fileURLToPath(import.meta.url));
const tool = resolve(scripts, 'release-public-verify.mts');
const fake = resolve(scripts, 'testdata/release-public-verify/fake-gh.mts');
const C = 'c'.repeat(40), M = 'b'.repeat(40), O = 'a'.repeat(40), tag = 'v2.7.19';
const digest = (bytes: Buffer | string): string => createHash('sha256').update(bytes).digest('hex');
type State = Record<string, unknown>;
interface RemoteAsset extends Asset { id: number; digest?: string; state: string }
async function fixture() {
  const temp = mkdtempSync(join(tmpdir(), 'TEST-public-release-verifier-'));
  const sealed = join(temp, 'sealed'), statePath = join(temp, 'remote.json'), callsPath = join(temp, 'calls.jsonl');
  mkdirSync(join(sealed, 'assets'), { recursive: true }); mkdirSync(join(temp, 'bin'));
  writeFileSync(join(temp, 'bin/gh'), `#!/bin/sh\nexec '${process.execPath}' '${fake}' "$@"\n`, { mode: 0o700 });
  writeFileSync(callsPath, '');
  for (const name of inventory().filter(name => name !== 'checksums.txt')) writeFileSync(join(sealed, 'assets', name), `TEST ${name}\n`);
  writeFileSync(join(sealed, 'assets/checksums.txt'), inventory().filter(name => name !== 'checksums.txt')
    .map(name => `${digest(readFileSync(join(sealed, 'assets', name)))}  ${name}\n`).join(''));
  const entry = (directory: string, name: string): Asset => {
    const bytes = readFileSync(join(directory, name)); return { name, size: bytes.length, sha256: digest(bytes) };
  };
  writeFileSync(join(sealed, 'qualification.json'), JSON.stringify({ status: 'basic_five_platform_artifact_checks_complete',
    releaseTag: tag, candidateSHA: C, operatorSHA: O, runID: '43', runAttempt: '2', signingRun: '41', signingAttempt: '3',
    assetCount: 29, platformCount: 5, installedBusinessQualified: false, fullNativeQualified: false,
    visibleDesktopQualified: false, macosE2EQualified: false, publicationAuthorized: false }));
  writeFileSync(join(sealed, 'signing.json'), JSON.stringify({ id: 41, run_attempt: 3, head_sha: C, conclusion: 'success',
    status: 'completed', event: 'workflow_dispatch', head_branch: 'release/macos-signing', path: '.github/workflows/macos-qualification.yml',
    actor: { login: '777genius' }, triggering_actor: { login: '777genius' } }));
  writeFileSync(join(sealed, 'helper-custody.json'), JSON.stringify({ sourceSHA: C, workflowSHA: C, runID: '41', runAttempt: '3',
    signingTeam: '86399583GS', bundleID: 'com.777genius.agent-notifications', signatureVerified: true,
    archiveSHA256: digest(readFileSync(join(sealed, 'assets/ClaudeNotifier.app.zip'))) }));
  const manifest: Manifest = { schema: 1, repository: '777genius/agent-notifications', releaseTag: tag,
    candidateSHA: C, operatorSHA: O, runID: '43', runAttempt: '2', signingRun: '41', signingAttempt: '3',
    assets: inventory().map(name => entry(join(sealed, 'assets'), name)),
    receipts: ['qualification.json', 'signing.json', 'helper-custody.json'].map(name => entry(sealed, name)) };
  writeFileSync(join(sealed, 'manifest.json'), JSON.stringify(manifest)); validate(sealed);
  const sealBefore = readFileSync(join(sealed, 'manifest.json'));
  const assets: RemoteAsset[] = manifest.assets.map((asset, index) => ({ ...asset, id: index + 1, state: 'uploaded', digest: `sha256:${asset.sha256}` }));
  // One missing API digest exercises the real gh download/hash fallback in every successful run.
  delete assets[0]!.digest;
  const release = { id: 17, tag_name: tag, target_commitish: C, draft: false, prerelease: false };
  const gitRef = (kind: string, name: string, sha: string) => ({ ref: `refs/${kind}/${name}`, object: { type: 'commit', sha } });
  const setup = '#!/usr/bin/env bash\ncontroller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"\nprintf "controller snapshot\\n"\n';
  const expectedLoader = setup.replace('controller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"', `controller="\${BOOTSTRAP_CONTROLLER_COMMIT:-${M}}"`);
  let loader = expectedLoader, legacyMissing = false, legacyCorrupt = false;
  const legacySHA = 'd'.repeat(40);
  const legacySetup = '#!/bin/bash\necho TEST legacy setup\n';
  const state: State = {
    [`releases/tags/${tag}`]: release, 'releases/latest': release,
    'releases/17/assets?per_page=100': assets,
    'releases/assets/1': { bytes: readFileSync(join(sealed, 'assets', assets[0]!.name)).toString('base64') },
    [`git/ref/tags/${tag}`]: gitRef('tags', tag, C), 'git/ref/heads/main': gitRef('heads', 'main', M),
    'git/ref/heads/release/platform-macos': gitRef('heads', 'release/platform-macos', C),
    'git/ref/heads/release/platform-linux-windows': gitRef('heads', 'release/platform-linux-windows', C),
    [`git/ref/tags/dist/platform-source/${C}`]: gitRef('tags', `dist/platform-source/${C}`, C),
    'git/ref/heads/gh-pages': gitRef('heads', 'gh-pages', legacySHA),
    'actions/runs/51': { id: 51, run_attempt: 2, head_sha: M, head_branch: 'main', event: 'push',
      path: '.github/workflows/landing.yml', status: 'completed', conclusion: 'success' },
    'actions/runs/51/attempts/2/jobs?per_page=100': { total_count: 2,
      jobs: ['verify', 'deploy'].map(name => ({ name, head_sha: M, status: 'completed', conclusion: 'success' })) },
  };
  const channels = '# agent-notifications-platform-channels-v1\n' + ['darwin\tamd64', 'darwin\tarm64', 'linux\tamd64', 'linux\tarm64', 'windows\tamd64']
    .map(platform => `${platform}\t${tag}\t${C}\t${C}\trelease/platform-${platform.startsWith('darwin') ? 'macos' : 'linux-windows'}\n`).join('');
  for (const [path, body] of [['release-channels.tsv', channels], ['bin/setup.sh', setup], ['bin/bootstrap.sh', '#!/usr/bin/env bash\n'], ['bin/release-channel.sh', '#!/usr/bin/env bash\n']])
    state[`contents/${path}?ref=${M}`] = { type: 'file', path, encoding: 'base64', content: Buffer.from(body!).toString('base64') };
  for (const [path, body] of [['setup.sh', legacySetup]])
    state[`contents/${path}?ref=${legacySHA}`] = { type: 'file', path, encoding: 'base64', content: Buffer.from(body!).toString('base64') };
  writeFileSync(statePath, JSON.stringify(state));
  const server = createServer((request, response) => {
    assert.equal(request.method, 'GET');
    if (request.url === '/install.sh') response.end(loader);
    else if (request.url === '/setup.sh' && !legacyMissing) response.end(legacyCorrupt ? 'CORRUPT legacy script' : legacySetup);
    else { response.statusCode = 404; response.end('Missing TEST route'); }
  });
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  const address = server.address(); assert.ok(address && typeof address === 'object');
  const url = `http://127.0.0.1:${address.port}/install.sh`;
  const update = (patch: State) => writeFileSync(statePath, JSON.stringify({ ...state, ...patch }));
  const run = () => new Promise<{ status: number | null; stdout: string; stderr: string }>((resolve, reject) => {
    const child = spawn(process.execPath, [tool, sealed, M, url, '51'], { cwd: temp,
      env: { ...process.env, PATH: `${join(temp, 'bin')}:${process.env.PATH}`, TEST_PUBLIC_STATE: statePath, TEST_PUBLIC_CALLS: callsPath },
      stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    child.stdout.on('data', bytes => { stdout += bytes; }); child.stderr.on('data', bytes => { stderr += bytes; });
    child.on('error', reject); child.on('close', status => resolve({ status, stdout, stderr }));
  });
  const clean = async () => {
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    assert.deepEqual(readFileSync(join(sealed, 'manifest.json')), sealBefore); validate(sealed);
    chmodSync(temp, 0o700); rmSync(temp, { recursive: true, force: true });
  };
  return { state, assets, run, update, expectedLoader, setLoader: (body: string) => { loader = body; }, callsPath, setLegacyCorrupt: (value: boolean) => { legacyCorrupt = value; }, setLegacyMissing: () => { legacyMissing = true; }, clean };
}

// These subprocess boundaries catch false green audits caused by stale publication,
// missing/corrupt bytes or a successful deployment belonging to another main commit.
test('public verifier CLI validates publication and fails closed on incomplete or stale evidence', async t => {
  const f = await fixture();
  try {
    await t.test('happy path: all 29 bytes, missing-digest download, exact controller and Pages jobs', async () => {
      const result = await f.run(); assert.equal(result.status, 0, result.stderr);
      const output = JSON.parse(result.stdout);
      assert.equal(output.status, 'public_release_verified'); assert.equal(output.publicRelease.assetCount, 29);
      assert.match(output.verifiedAt, /^\d{4}-\d{2}-\d{2}T/); assert.match(output.sealManifestSHA256, /^[a-f0-9]{64}$/);
      assert.equal(output.retainedLegacy.setup.sha256, output.retainedLegacy.setup.expectedSHA256);
      assert.equal(output.mainSHA, M); assert.equal(output.sourceSHA, C);
      assert.equal(output.publicRelease.assets.find((asset: { id: number }) => asset.id === 1).digestEvidence, 'downloaded bytes');
      assert.equal(output.loader.sha256, digest(f.expectedLoader)); assert.match(output.scope.actualInstallation, /Separate evidence/);
      const calls = readFileSync(f.callsPath, 'utf8');
      assert.match(calls, /Accept: application\/octet-stream/); assert.match(calls, /attempts\/2\/jobs/);
      assert.ok(calls.split('\n').filter(Boolean).every(line => JSON.parse(line)[0] === 'api'));
    });
    const cases: Array<[string, State, RegExp]> = [
      ['missing asset', { 'releases/17/assets?per_page=100': f.assets.slice(1) }, /all 29 assets/],
      ['public release is still a draft', { [`releases/tags/${tag}`]: { ...(f.state[`releases/tags/${tag}`] as object), draft: true } }, /public release identity/],
      ['wrong Latest', { 'releases/latest': { ...(f.state['releases/latest'] as object), tag_name: 'v2.7.18' } }, /Latest release identity/],
      ['wrong asset size', { 'releases/17/assets?per_page=100': f.assets.map((asset, index) => index === 1 ? { ...asset, size: 1 } : asset) }, /state\/size mismatch/],
      ['duplicate asset name', { 'releases/17/assets?per_page=100': [f.assets[1], ...f.assets.slice(1)] }, /duplicate\/invalid public asset/],
      ['API digest mismatch', { 'releases/17/assets?per_page=100': f.assets.map((asset, index) => index === 1 ? { ...asset, digest: 'sha256:' + '0'.repeat(64) } : asset) }, /digest mismatch/],
      ['downloaded bytes mismatch', { 'releases/assets/1': { bytes: Buffer.from('CORRUPT').toString('base64') } }, /digest mismatch/],
      ['stale main', { 'git/ref/heads/main': { ref: 'refs/heads/main', object: { type: 'commit', sha: O } } }, /heads\/main must point/],
      ['Pages wrong SHA', { 'actions/runs/51': { ...(f.state['actions/runs/51'] as object), head_sha: O } }, /Pages run must/],
      ['Pages skipped deploy despite globally green run', { 'actions/runs/51/attempts/2/jobs?per_page=100': { total_count: 2,
        jobs: [{ name: 'verify', head_sha: M, status: 'completed', conclusion: 'success' },
          { name: 'deploy', head_sha: M, status: 'completed', conclusion: 'skipped' }] } }, /Pages deploy job/],
      ['source branch moved', { 'git/ref/heads/release/platform-macos': { ref: 'refs/heads/release/platform-macos', object: { type: 'commit', sha: O } } }, /heads\/release\/platform-macos/],
      ['channel source differs from release', { [`contents/release-channels.tsv?ref=${M}`]: { type: 'file', path: 'release-channels.tsv', encoding: 'base64',
        content: Buffer.from('# agent-notifications-platform-channels-v1\n' + ['darwin\tamd64', 'darwin\tarm64', 'linux\tamd64', 'linux\tarm64', 'windows\tamd64']
          .map(platform => `${platform}\t${tag}\t${C}\t${O}\trelease/platform-${platform.startsWith('darwin') ? 'macos' : 'linux-windows'}\n`).join('')).toString('base64') } }, /channel darwin\/amd64 must select/],
      ['wrong immutable source tag', { [`git/ref/tags/dist/platform-source/${C}`]: { ref: `refs/tags/dist/platform-source/${C}`, object: { type: 'commit', sha: O } } }, /tags\/dist\/platform-source/],
    ];
    for (const [name, patch, error] of cases) await t.test(name, async () => {
      f.update(patch); const result = await f.run(); assert.notEqual(result.status, 0);
      assert.equal(result.stdout, ''); assert.match(JSON.parse(result.stderr).error, error); f.update({});
    });
    await t.test('legacy HTTP 200 with different bytes fails', async () => {
      f.setLegacyCorrupt(true); const result = await f.run(); assert.notEqual(result.status, 0);
      assert.match(JSON.parse(result.stderr).error, /legacy \/setup.sh bytes differ/); f.setLegacyCorrupt(false);
    });
    await t.test('missing retained legacy setup script fails despite current loader', async () => {
      f.setLegacyMissing(); const result = await f.run(); assert.notEqual(result.status, 0);
      assert.match(JSON.parse(result.stderr).error, /HTTP 404: \/setup.sh/);
    });
    await t.test('lagging loader: version label cannot substitute for exact staged bytes', async () => {
      f.setLoader(f.expectedLoader.replace(M, O) + `# ${tag}\n`);
      const result = await f.run(); assert.notEqual(result.status, 0); assert.match(JSON.parse(result.stderr).error, /public loader bytes differ/);
    });
  } finally { await f.clean(); }
});
