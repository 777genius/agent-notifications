import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { mkdtemp, writeFile, readFile, readdir, chmod, stat, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, delimiter } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { validateInput, type Input } from './release-wait.mts';
const script = fileURLToPath(new URL('./release-wait.mts', import.meta.url));
const fixture = `
import { writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
const args = process.argv.slice(2), root = process.env.TEST_RELEASE_WAIT_ROOT;
const scenario = process.env.TEST_RELEASE_WAIT_CASE;
const marker = name => join(root, name);
const emit = value => process.stdout.write(JSON.stringify(value));
const delay = ms => new Promise(done => setTimeout(done, ms));
async function watcher(name) {
  if (existsSync(marker('start-' + name))) throw Error('duplicate watcher');
  writeFileSync(marker('start-' + name), 'started');
  const until = Date.now() + 4000;
  while (!['pr-1','pr-2','signing'].every(item => existsSync(marker('start-' + item)))) {
    if (Date.now() > until) throw Error('watchers did not start concurrently');
    await delay(10);
  }
  if (name === 'signing') await delay(180);
  writeFileSync(marker('done-' + name), 'finished');
}
if (args[0] === 'pr' && args[1] === 'view') {
  const changed = scenario === 'head-change' && args[2] === '2' && existsSync(marker('done-pr-2'));
  emit({headRefOid: (changed ? 'd' : args[2] === '1' ? 'a' : 'c').repeat(40)});
} else if (args[0] === 'pr' && args[1] === 'checks') {
  if (args.includes('--watch')) { await watcher('pr-' + args[2]); }
  else {
    const failing = args[2] === '1';
    const skipped = failing && scenario === 'skipped', failed = failing && scenario === 'failed';
    emit(failing && scenario === 'missing' ? [] : [{name:'CI',bucket: skipped ? 'skipping' : failed ? 'fail' : 'pass',state: skipped ? 'SKIPPED' : failed ? 'FAILURE' : 'SUCCESS'}]);
  }
} else if (args[0] === 'run' && args[1] === 'watch') {
  await watcher('signing');
} else if (args[0] === 'api') {
  const finished = existsSync(marker('done-signing'));
  emit({id:7,run_attempt: finished && scenario === 'attempt-change' ? 2 : 1, head_sha:'a'.repeat(40),
    event:'workflow_dispatch',head_branch:'release/macos-signing',path:'.github/workflows/macos-qualification.yml',
    actor:{login:'777genius'},triggering_actor:{login:'777genius'},
    status: finished ? 'completed' : 'in_progress', conclusion: finished ? (scenario === 'signing-failed' ? 'failure' : 'success') : null});
} else throw Error('unexpected gh call');
`;
function input(receipt: string): Input {
  return { repository: '777genius/agent-notifications', candidateSHA: 'a'.repeat(40), operatorSHA: 'b'.repeat(40), version: 'v1.48.6',
    candidate: { pr: '1', checks: ['CI'] }, promotion: { pr: '2', headSHA: 'c'.repeat(40), checks: ['CI'] },
    signing: { runID: '7', attempt: '1' }, receipt };
}
async function cli(path: string, root: string, scenario: string): Promise<{ code: number | null; output: string }> {
  const child = spawn(process.execPath, [script, path], { cwd: dirname(script),
    env: { ...process.env, PATH: root + delimiter + process.env.PATH, TEST_RELEASE_WAIT_ROOT: root, TEST_RELEASE_WAIT_CASE: scenario },
    stdio: ['ignore', 'pipe', 'pipe'] });
  let output = ''; child.stdout.on('data', chunk => { output += String(chunk); }); child.stderr.on('data', chunk => { output += String(chunk); });
  const code = await new Promise<number | null>((done, reject) => { child.once('error', reject); child.once('close', done); });
  return { code, output };
}
test('strict explicit identities and complete check contracts', () => {
  const valid = input('/private/tmp/TEST-release-receipt.json');
  assert.deepEqual(validateInput(valid), valid);
  for (const changed of [
    { repository: 'TEST/repo' }, { operatorSHA: 'main' }, { extra: true },
    { candidate: { pr: '1', checks: [] } }, { candidate: { pr: '1', checks: ['CI', 'CI'] } },
    { promotion: { ...valid.promotion, pr: '1' } }, { signing: { runID: '7', attempt: 'latest' } },
  ]) assert.throws(() => validateInput({ ...valid, ...changed }));
});
test('real CLI starts three watchers together, joins all and preserves exclusive success/failure receipts', { timeout: 30000 }, async () => {
  for (const scenario of ['pass', 'failed', 'missing', 'skipped', 'head-change', 'attempt-change', 'signing-failed']) {
    const root = await mkdtemp(join(tmpdir(), 'TEST-release-wait-'));
    try {
      const fake = join(root, 'fake-gh.mts'), gh = join(root, 'gh');
      await writeFile(fake, fixture);
      // Quote executable paths safely; this private TEST shim is the only gh replacement.
      const quote = (value: string) => "'" + value.replaceAll("'", "'\\''") + "'";
      await writeFile(gh, '#!/bin/sh\nexec ' + quote(process.execPath) + ' ' + quote(fake) + ' "$@"\n'); await chmod(gh, 0o700);
      const receipt = join(root, 'receipt.json'), config = join(root, 'input.json');
      await writeFile(config, JSON.stringify(input(receipt)));
      const result = await cli(config, root, scenario);
      assert.equal(result.code, scenario === 'pass' ? 0 : 1, result.output);
      const record = JSON.parse(await readFile(receipt, 'utf8'));
      assert.equal(record.outcome, scenario === 'pass' ? 'pass' : 'fail');
      assert.equal(record.candidateSHA, 'a'.repeat(40)); assert.equal(record.operatorSHA, 'b'.repeat(40));
      assert.equal(record.promotion.headSHA, 'c'.repeat(40)); assert.deepEqual(record.signing, { runID: '7', attempt: '1' });
      assert.equal(record.scope, 'source-signing-promotion-ci-fan-in-only');
      assert.equal(record.artifactQualified, false); assert.equal(record.nativeQualified, false); assert.equal(record.publicationAuthorized, false);
      assert.equal(record.observations.length, 3); assert(record.completedAt >= record.startedAt); assert(record.durationMilliseconds >= 150);
      assert.equal((await stat(receipt)).mode & 0o777, 0o600);
      assert.deepEqual((await readdir(root)).filter(name => name.startsWith('done-')).sort(), ['done-pr-1', 'done-pr-2', 'done-signing']);
      const retained = await readFile(receipt, 'utf8');
      assert.equal((await cli(config, root, scenario)).code, 1);
      assert.equal(await readFile(receipt, 'utf8'), retained, 'existing failure/success receipt cannot be overwritten');
    } finally { await rm(root, { recursive: true, force: true }); }
  }
});
