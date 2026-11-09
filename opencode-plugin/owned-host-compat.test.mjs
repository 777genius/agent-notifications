import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, realpathSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readFileSync } from 'node:fs';
import { build } from 'esbuild';

// Check the actual owned-host preparation (with only the child-process port
// stubbed), not a fabricated public runtimeEligibility flag. No native events,
// secrets, notifications or host binaries are created by this test.
test('verified V1/V2 host versions are dynamic; denied profiles report only after ownership proof', async (t) => {
  if (realpathSync(process.execPath) !== process.execPath) return t.skip('Node executable is an alias, not an owned native image');
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'TEST-opencode-host-')));
  const executable = join(root, 'helper');
  writeFileSync(executable, '', { mode: 0o700 });
  const key = 'TEST-opencode-host-profile-' + process.pid;
  const source = readFileSync(new URL('./owned-host.mjs', import.meta.url), 'utf8')
    .replace('"__AGENT_NOTIFICATIONS_EXECUTABLE__"', JSON.stringify(executable))
    .replace('"__AGENT_NOTIFICATIONS_CONTROL_ROOT__"', JSON.stringify(root))
    .replace('"__AGENT_NOTIFICATIONS_ORIGIN__"', JSON.stringify('a'.repeat(64)));
  const built = await build({ stdin: { contents: source, resolveDir: fileURLToPath(new URL('.', import.meta.url)),
      sourcefile: 'owned-host-compat-fixture.mjs' },
    bundle: true, write: false, platform: 'node', format: 'esm',
    plugins: [{ name: 'fixture-registry-port', setup(builder) {
      builder.onResolve({ filter: /process-registry[.]mjs$/, }, () => ({ path: 'registry', namespace: 'fixture' }));
      builder.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({ loader: 'js', contents:
        'export const absoluteNativePath = p => typeof p === "string" && p.length > 0;' +
        'export const createProcessRegistry = () => ({ profile: async () => globalThis[' + JSON.stringify(key) +
        '].response, dispose: async () => ({reaped:true}) });' }));
    } }] });
  const { prepareOwnedHost } = await import('data:text/javascript;base64,' +
    Buffer.from(built.outputFiles[0].text).toString('base64'));
  const receipt = (semantic, generation, version) => Buffer.from(JSON.stringify({
    protocol: 1, semantic, generation, resourceClosure: 'reaped_or_not_started',
    ...(version === undefined ? {} : { version }),
  }));
  try {
    for (const [generation, version] of [['v1', '1.18.99'], ['v2', '2.2.6'], ['v2', '2.99.0']]) {
      globalThis[key] = { response: { status: 'ok', output: receipt('eligible', generation, version) } };
      const warnings = [];
      const owned = await prepareOwnedHost(root, generation, version, false, code => warnings.push(code));
      assert.ok(owned, generation + ' ' + version);
      assert.equal(owned.hostVersion, version);
      assert.equal(owned.runtimeEligibility(), 'supported');
      assert.deepEqual(warnings, []);
      assert.deepEqual(await owned.dispose(), { reaped: true });
    }
    globalThis[key] = { response: { status: 'ok', output: receipt('eligible', 'v2', '2.2.6') } };
    const mismatchWarnings = [];
    assert.equal(await prepareOwnedHost(root, 'v2', '2.2.7', false, code => mismatchWarnings.push(code)), undefined);
    assert.deepEqual(mismatchWarnings, ['runtime_unverified']);
    globalThis[key].response = { status: 'ok', output: receipt('unverified', 'none') };
    const deniedWarnings = [];
    assert.equal(await prepareOwnedHost(root, 'v1', undefined, false, code => deniedWarnings.push(code)), undefined);
    assert.deepEqual(deniedWarnings, ['runtime_unverified']);
    globalThis[key].response.status = 'ipc_termination_unproved';
    const untrustedWarnings = [];
    assert.equal(await prepareOwnedHost(root, 'v2', '2.2.6', false, code => untrustedWarnings.push(code)), undefined);
    assert.deepEqual(untrustedWarnings, [], 'untrusted helper exit is not an authenticated diagnostic');
  } finally {
    delete globalThis[key];
    rmSync(root, { recursive: true, force: true });
  }
});
