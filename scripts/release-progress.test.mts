import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { mkdtemp, writeFile, readFile, symlink, chmod } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { record, watchPR } from './release-progress.mts';
const binding = { candidateSHA: 'a'.repeat(40), operatorSHA: 'b'.repeat(40), version: 'v1.48.6' };
test('journal fences identity, no implicit retry, and hashes actual evidence', async () => {
  const root = await mkdtemp(join(tmpdir(), 'TEST-release-journal-'));
  const file = join(root, 'progress.json'), proof = join(root, 'proof.json');
  await writeFile(proof, '{"passed":false}\n');
  await record(file, binding, 'canary', 'start');
  await assert.rejects(record(file, binding, 'canary', 'start'), /phase_already_started/);
  await assert.rejects(record(file, { ...binding, operatorSHA: 'c'.repeat(40) }, 'canary', 'fail', [proof]), /identity_changed/);
  await assert.rejects(record(file, binding, 'canary', 'pass'), /evidence_receipt_required/);
  await record(file, binding, 'canary', 'fail', [proof]);
  await assert.rejects(record(file, binding, 'canary', 'pass', [proof]), /open_phase_required/);
  const journal = JSON.parse(await readFile(file, 'utf8'));
  assert.equal(journal.phases.canary.outcome, 'fail');
  assert.equal(journal.phases.canary.receipts[0].sha256, createHash('sha256').update(await readFile(proof)).digest('hex'));
  assert(journal.phases.canary.completedAt >= journal.phases.canary.startedAt);
  const alias = join(root, 'alias.json'); await symlink(file, alias);
  await assert.rejects(record(alias, binding, 'other', 'start'), /bounded_regular_journal/);
  assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), journal);
});
test('one gh watcher must retain exact head and all expected successful checks', async () => {
  const root = await mkdtemp(join(tmpdir(), 'TEST-release-watch-'));
  async function fake(scenario: string): Promise<string> {
    const gh = join(root, `gh-${scenario}`);
    const head = scenario === 'changed' ? 'c'.repeat(40) : binding.candidateSHA;
    const after = join(root, `calls-${scenario}`);
    const bucket = scenario === 'failed' ? 'fail' : scenario === 'skipped' ? 'skipping' : 'pass';
    const state = scenario === 'failed' ? 'FAILURE' : scenario === 'skipped' ? 'SKIPPED' : 'SUCCESS';
    // Only this fixture replaces gh. Production always invokes the installed gh CLI.
    await writeFile(gh, `#!/bin/sh\ncase "$*" in\n *--watch*) echo watch >> '${join(root, `calls-${scenario}`)}'; exit 0 ;;\n *headRefOid*) if [ '${scenario}' = after ] && [ -f '${after}' ]; then echo '{"headRefOid":"${'c'.repeat(40)}"}'; else echo '{"headRefOid":"${head}"}'; fi ;;\n *name,bucket,state*) echo '[{"name":"CI","bucket":"${bucket}","state":"${state}"}]' ;;\n *) exit 2 ;;\nesac\n`);
    await chmod(gh, 0o700); return gh;
  }
  await watchPR('TEST/repo', '1', binding.candidateSHA, ['CI'], await fake('pass'));
  assert.equal((await readFile(join(root, 'calls-pass'), 'utf8')).trim(), 'watch');
  await assert.rejects(watchPR('TEST/repo', '1', binding.candidateSHA, ['CI'], await fake('changed')), /head_changed/);
  await assert.rejects(watchPR('TEST/repo', '1', binding.candidateSHA, ['CI'], await fake('after')), /head_changed_after_watch/);
  await assert.rejects(watchPR('TEST/repo', '1', binding.candidateSHA, ['CI'], await fake('failed')), /failed_pending/);
  await assert.rejects(watchPR('TEST/repo', '1', binding.candidateSHA, ['CI'], await fake('skipped')), /non_success/);
  await assert.rejects(watchPR('TEST/repo', '1', binding.candidateSHA, ['missing'], await fake('missing')), /missing_or_non_success/);
});
