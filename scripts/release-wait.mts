// Read-only source/signing/promotion fan-in. Never authorizes release publication.
import { strict as assert } from 'node:assert';
import { spawn } from 'node:child_process';
import { open, readFile, lstat } from 'node:fs/promises';
import { isAbsolute, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { watchPR, validateBinding } from './release-progress.mts';

export interface Input {
  repository: '777genius/agent-notifications'; candidateSHA: string; operatorSHA: string; version: string;
  candidate: { pr: string; checks: string[] };
  promotion: { pr: string; headSHA: string; checks: string[] };
  signing: { runID: string; attempt: string }; receipt: string;
}
function object(value: unknown, keys: string[]): Record<string, unknown> {
  assert(value && typeof value === 'object' && !Array.isArray(value), 'object_required');
  assert.deepEqual(Object.keys(value).sort(), [...keys].sort(), 'exact_input_fields_required');
  return value as Record<string, unknown>;
}
export function validateInput(value: unknown): Input {
  const input = object(value, ['repository', 'candidateSHA', 'operatorSHA', 'version', 'candidate', 'promotion', 'signing', 'receipt']);
  assert(input.repository === '777genius/agent-notifications', 'fixed_repository_required');
  for (const key of ['candidateSHA', 'operatorSHA', 'version']) assert(typeof input[key] === 'string', 'binding_strings_required');
  validateBinding(input as unknown as Input);
  const decimal = (value: unknown) => assert(typeof value === 'string' && /^[1-9]\d*$/.test(value), 'positive_decimal_identity_required');
  for (const [key, keys] of [['candidate', ['pr', 'checks']], ['promotion', ['pr', 'headSHA', 'checks']]] as const) {
    const pr = object(input[key], [...keys]); decimal(pr.pr);
    assert(Array.isArray(pr.checks) && pr.checks.length > 0 && pr.checks.length <= 100 &&
      pr.checks.every(check => typeof check === 'string' && check.trim() === check && check.length > 0 && check.length <= 200 && !/[\r\n\0]/.test(check)) &&
      new Set(pr.checks).size === pr.checks.length, 'unique_nonempty_expected_checks_required');
  }
  assert((input.candidate as Input['candidate']).pr !== (input.promotion as Input['promotion']).pr, 'distinct_candidate_promotion_PRs_required');
  assert(typeof (input.promotion as Input['promotion']).headSHA === 'string' && /^[a-f0-9]{40}$/.test((input.promotion as Input['promotion']).headSHA), 'exact_promotion_head_required');
  const signing = object(input.signing, ['runID', 'attempt']); decimal(signing.runID); decimal(signing.attempt);
  assert(typeof input.receipt === 'string' && isAbsolute(input.receipt) && !input.receipt.includes('\0'), 'absolute_receipt_path_required');
  return input as unknown as Input;
}
async function gh(args: string[], inherit = false): Promise<string> {
  const child = spawn('gh', args, { stdio: inherit ? 'inherit' : ['ignore', 'pipe', 'inherit'] });
  let output = '', exceeded = false;
  child.stdout?.on('data', (bytes: Buffer) => {
    output += bytes.toString();
    if (output.length > 4 * 1024 * 1024) { exceeded = true; child.kill('SIGTERM'); }
  });
  const code = await new Promise<number | null>((done, reject) => { child.once('error', reject); child.once('close', done); });
  assert(code === 0 && !exceeded, 'signing_observation_failed_no_automatic_rerun');
  return output;
}
function signingIdentity(run: Record<string, unknown>, input: Input): void {
  assert(String(run.id) === input.signing.runID && String(run.run_attempt) === input.signing.attempt &&
    run.head_sha === input.candidateSHA && run.event === 'workflow_dispatch' &&
    run.head_branch === 'release/macos-signing' && run.path === '.github/workflows/macos-qualification.yml' &&
    (run.actor as { login?: unknown } | null)?.login === '777genius' &&
    (run.triggering_actor as { login?: unknown } | null)?.login === '777genius', 'exact_owner_same_source_signing_run_required');
}
async function watchSigning(input: Input): Promise<void> {
  const read = async () => {
    const run: unknown = JSON.parse(await gh(['api', `repos/${input.repository}/actions/runs/${input.signing.runID}`]));
    assert(run && typeof run === 'object' && !Array.isArray(run), 'signing_run_object_required');
    signingIdentity(run as Record<string, unknown>, input);
    return run as Record<string, unknown>;
  };
  await read();
  await gh(['run', 'watch', input.signing.runID, '--repo', input.repository, '--exit-status', '--interval', '30'], true);
  const run = await read();
  assert(run.status === 'completed' && run.conclusion === 'success', 'signing_not_completed_success');
}
export async function wait(input: Input): Promise<void> {
  validateInput(input);
  // Reserve before observation: an existing receipt never triggers another watch.
  const receipt = await open(input.receipt, 'wx', 0o600);
  const startedAt = new Date().toISOString(), started = performance.now();
  try {
    const results = await Promise.allSettled([
      watchPR(input.repository, input.candidate.pr, input.candidateSHA, input.candidate.checks),
      watchPR(input.repository, input.promotion.pr, input.promotion.headSHA, input.promotion.checks),
      watchSigning(input),
    ]);
    const observations = results.map((result, index) => ({ phase: ['candidate-ci', 'promotion-ci', 'signing'][index],
      status: result.status === 'fulfilled' ? 'pass' : 'fail',
      ...(result.status === 'rejected' ? { error: String(result.reason instanceof Error ? result.reason.message : result.reason).slice(0, 4000) } : {}),
    }));
    const passed = results.every(result => result.status === 'fulfilled');
    await receipt.writeFile(JSON.stringify({ schemaVersion: 1, ...input, startedAt, completedAt: new Date().toISOString(),
      durationMilliseconds: Math.round(performance.now() - started), outcome: passed ? 'pass' : 'fail',
      scope: 'source-signing-promotion-ci-fan-in-only', artifactQualified: false, nativeQualified: false,
      publicationAuthorized: false, observations }, null, 2) + '\n');
    await receipt.sync();
    assert(passed, 'release_fan_in_failed_receipt_retained');
  } finally { await receipt.close(); }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    assert(process.argv.length === 3, 'usage: release-wait.mts INPUT.json');
    const path = resolve(process.argv[2]!); const stat = await lstat(path);
    assert(stat.isFile() && !stat.isSymbolicLink() && stat.size > 0 && stat.size <= 1024 * 1024, 'bounded_regular_input_required');
    await wait(validateInput(JSON.parse(await readFile(path, 'utf8'))));
  } catch (error) { console.error(error instanceof Error ? error.message : error); process.exitCode = 1; }
}
