// Local release timing/evidence journal. Records evidence; never grants qualification.
import { strict as assert } from 'node:assert';
import { createHash } from 'node:crypto';
import { readFile, open, rename, lstat, unlink } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

export type Binding = { candidateSHA: string; operatorSHA: string; version: string };
type Receipt = { path: string; sha256: string };
type Phase = { startedAt: string; completedAt?: string; outcome?: 'pass' | 'fail'; receipts?: Receipt[] };
type Journal = Binding & { schemaVersion: 1; phases: Record<string, Phase> };
const sha = /^[a-f0-9]{40}$/;
export function validateBinding(b: Binding): void {
  assert(sha.test(b.candidateSHA) && sha.test(b.operatorSHA), 'exact_candidate_and_operator_required');
  assert(/^v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)$/.test(b.version), 'stable_version_required');
}
export async function record(file: string, binding: Binding, phase: string, action: 'start' | 'pass' | 'fail', receipts: string[] = []): Promise<void> {
  validateBinding(binding);
  assert(/^[a-z][a-z0-9-]{0,63}$/.test(phase), 'phase_name_required');
  file = resolve(file);
  const lock = await open(`${file}.lock`, 'wx', 0o600);
  const temp = `${file}.${process.pid}.tmp`;
  try {
    let journal: Journal;
    try {
      const stat = await lstat(file);
      assert(stat.isFile() && !stat.isSymbolicLink() && stat.size < 1024 * 1024, 'bounded_regular_journal');
      journal = JSON.parse(await readFile(file, 'utf8')) as Journal;
      assert(journal.schemaVersion === 1 && journal.phases && typeof journal.phases === 'object', 'journal_schema');
      assert(journal.candidateSHA === binding.candidateSHA && journal.operatorSHA === binding.operatorSHA && journal.version === binding.version, 'journal_identity_changed');
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error;
      journal = { ...binding, schemaVersion: 1, phases: {} };
    }
    assert(!Object.hasOwn(Object.prototype, phase), 'reserved_phase_name');
    const existing = journal.phases[phase];
    if (action === 'start') {
      assert(!existing && receipts.length === 0, 'phase_already_started_no_implicit_retry');
      journal.phases[phase] = { startedAt: new Date().toISOString() };
    } else {
      assert(existing && !existing.completedAt, 'open_phase_required');
      assert(receipts.length > 0, 'evidence_receipt_required');
      const evidence: Receipt[] = [];
      for (const path of receipts) {
        const absolute = resolve(path), stat = await lstat(absolute);
        assert(stat.isFile() && !stat.isSymbolicLink() && stat.size > 0 && stat.size <= 16 * 1024 * 1024, 'bounded_regular_evidence_required');
        evidence.push({ path: absolute, sha256: createHash('sha256').update(await readFile(absolute)).digest('hex') });
      }
      journal.phases[phase] = { ...existing, completedAt: new Date().toISOString(), outcome: action, receipts: evidence };
    }
    const output = await open(temp, 'wx', 0o600);
    try { await output.writeFile(`${JSON.stringify(journal, null, 2)}\n`); await output.sync(); } finally { await output.close(); }
    await rename(temp, file);
  } finally {
    await unlink(temp).catch(error => { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; });
    await lock.close(); await unlink(`${file}.lock`);
  }
}

// Watch once with gh; then independently fence head and expected check completeness.
export async function watchPR(repo: string, pr: string, expectedHead: string, expectedNames: string[], gh = 'gh'): Promise<void> {
  assert(/^[\w.-]+\/[\w.-]+$/.test(repo) && /^[1-9]\d*$/.test(pr) && sha.test(expectedHead), 'explicit_PR_identity_required');
  assert(expectedNames.length > 0 && new Set(expectedNames).size === expectedNames.length && expectedNames.every(Boolean), 'expected_checks_required');
  const run = async (args: string[], inherit = false): Promise<string> => {
    const child = spawn(gh, args, { stdio: inherit ? 'inherit' : ['ignore', 'pipe', 'inherit'] });
    let result = '';
    child.stdout?.on('data', (chunk: Buffer) => { result += chunk.toString(); if (result.length > 4 * 1024 * 1024) child.kill('SIGTERM'); });
    const code = await new Promise<number | null>((done, reject) => { child.once('error', reject); child.once('close', done); });
    assert(code === 0, 'gh_watcher_or_query_failed_no_automatic_rerun'); return result;
  };
  const head = async () => JSON.parse(await run(['pr', 'view', pr, '--repo', repo, '--json', 'headRefOid'])) as { headRefOid: string };
  assert((await head()).headRefOid === expectedHead, 'PR_head_changed_before_watch');
  await run(['pr', 'checks', pr, '--repo', repo, '--watch', '--interval', '30'], true);
  const checks = JSON.parse(await run(['pr', 'checks', pr, '--repo', repo, '--json', 'name,bucket,state'])) as Array<{ name: string; bucket: string; state: string }>;
  assert((await head()).headRefOid === expectedHead, 'PR_head_changed_after_watch');
  assert(checks.every(c => c.bucket === 'pass' || c.bucket === 'skipping'), 'failed_pending_or_unknown_check');
  for (const name of expectedNames) {
    const matched = checks.filter(c => c.name === name);
    assert(matched.length > 0 && matched.every(c => c.bucket === 'pass' && c.state === 'SUCCESS'), `missing_or_non_success_expected_check:${name}`);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [command, ...args] = process.argv.slice(2);
  if (command === 'watch-pr') {
    const [repo, pr, head, ...checks] = args;
    assert(repo && pr && head && checks.length, 'usage: watch-pr OWNER/REPO PR HEAD CHECK...');
    await watchPR(repo, pr, head, checks);
  } else if (command === 'start' || command === 'pass' || command === 'fail') {
    const [file, candidateSHA, operatorSHA, version, phase, ...receipts] = args;
    assert(file && candidateSHA && operatorSHA && version && phase, 'usage: start|pass|fail JOURNAL C O vX.Y.Z PHASE [EVIDENCE...]');
    await record(file, { candidateSHA, operatorSHA, version }, phase, command, receipts);
  } else {
    console.log('Release timing/evidence journal (does not grant qualification)\n  start|pass|fail JOURNAL C O vX.Y.Z PHASE [EVIDENCE...]\n  watch-pr OWNER/REPO PR HEAD EXPECTED_CHECK...');
    if (command && command !== '--help') process.exitCode = 2;
  }
}
