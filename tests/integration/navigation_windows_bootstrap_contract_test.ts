import { createHash, randomUUID } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';
import test, { type TestContext } from 'node:test';
import {
  bindSameJobBootstrap,
  revalidateSameJobBootstrap,
  requireSameJobBootstrapMode,
  type BootstrapExpectedIdentity,
} from './navigation_windows_bootstrap_contract.ts';

type Json = Record<string, unknown>;
type Fixture = {
  options: Parameters<typeof bindSameJobBootstrap>[0];
  expected: BootstrapExpectedIdentity;
  userSid: string;
  session: number;
  observed: (overrides?: Partial<{ sourceSHA: string; observerSHA256: string; userSid: string; session: number }>) =>
    { sourceSHA: string; observerSHA256: string; userSid: string; session: number };
};

function sha256(data: Buffer | string): string {
  return createHash('sha256').update(data).digest('hex');
}

function makeFixture(t: TestContext, sourceSHA = 'a'.repeat(40)): Fixture {
  const runnerTemp = realpathSync(mkdtempSync(join(tmpdir(), 'navigation-bootstrap-contract-')));
  t.after(() => rmSync(runnerTemp, { recursive: true, force: true }));
  const bootstrapId = randomUUID(), currentNonce = randomUUID();
  const bootstrapRoot = join(runnerTemp, `navigation-windows-test-${bootstrapId}`);
  const currentRoot = join(runnerTemp, `navigation-windows-test-${currentNonce}`);
  mkdirSync(bootstrapRoot); mkdirSync(currentRoot);
  const observerBytes = Buffer.from('fixed TEST observer ' + randomUUID() + '\n');
  const observerPath = join(currentRoot, 'navigation-native-probe.exe');
  writeFileSync(observerPath, observerBytes, { flag: 'wx' });
  writeFileSync(join(bootstrapRoot, '.owned-test-root'), `TEST navigation Windows ${bootstrapId}\n`, { flag: 'wx' });

  const observerSHA256 = sha256(observerBytes), imageVersion = '26100.1-test', runnerLabel = 'windows-11-vs2026-arm';
  const expected: BootstrapExpectedIdentity = { sourceSHA, observerSHA256, imageVersion, runnerLabel };
  const userSid = 'S-1-5-21-1000-2000-3000-1001', session = 7, actorPID = 4100;
  const steps: Json[] = ['center-policy', 'preflight', 'oobe-setup', 'desktop-capture'].map((mode, index) => ({
    mode, pid: actorPID + index, status: 0, signal: null,
  }));
  const records: Json = {
    'oobe-setup-intent.json': { pid: actorPID + 2, nonce: bootstrapId },
    'oobe-setup-result.json': { pid: actorPID + 2, nonce: bootstrapId },
    'oobe-setup-after-preflight.json': { pid: actorPID + 2, nonce: bootstrapId },
  };
  const recordsPacket = Buffer.from(JSON.stringify({ nonce: bootstrapId, actorPID: actorPID + 2, records }) + '\n');
  const result: Json = {
    status: 'guest_setup_complete',
    root: bootstrapRoot,
    nonce: bootstrapId,
    sourceSHA,
    binarySHA256: observerSHA256,
    imageVersion,
    runnerLabel,
    oobeSetupQualified: true,
    captureQualified: true,
    oobeSetupActorCollected: true,
    captureActorCollected: true,
    invokeEffectUncertain: false,
    nativeEffectUncertain: false,
    showAttempts: 0,
    noKeyboardOrPointerInputProved: true,
    setupRecordsSHA256: sha256(recordsPacket),
    setupRecordCount: Object.keys(records).length,
    oobeSetupResult: {
      setupQualified: true,
      ownedOOBEWindowGone: true,
      invokeEffectUncertain: false,
      acceptCallsEntered: 1,
      invokeCallsEntered: 3,
      invokeCallsReturned: 3,
      progressionsObserved: 3,
    },
    'desktop-capture.json': {
      session,
      sourceSHA,
      binarySHA256: observerSHA256,
    },
    steps,
  };
  const resultBytes = Buffer.from(JSON.stringify(result, null, 2) + '\n');
  writeFileSync(join(bootstrapRoot, 'oobe-setup-records.json'), recordsPacket, { flag: 'wx' });
  writeFileSync(join(bootstrapRoot, 'evidence.json'), resultBytes, { flag: 'wx' });
  const handoff: Json = {
    root: bootstrapRoot,
    nonce: bootstrapId,
    sourceSHA,
    runID: process.env.GITHUB_RUN_ID,
    job: process.env.GITHUB_JOB,
    attempt: '1',
    userSid,
    session,
    observerSHA256,
    evidenceSHA256: sha256(resultBytes),
    recordsSHA256: sha256(recordsPacket),
  };
  writeFileSync(join(bootstrapRoot, 'same-job-bootstrap.json'), JSON.stringify(handoff) + '\n', { flag: 'wx' });
  return {
    expected,
    userSid,
    session,
    options: {
      suppliedRoot: bootstrapRoot,
      runnerTemp,
      currentRoot,
      currentNonce,
      observerPath,
      expected,
    },
    observed: (overrides = {}) => ({
      sourceSHA,
      observerSHA256,
      userSid,
      session,
      ...overrides,
    }),
  };
}

test.before(() => {
  process.env.GITHUB_RUN_ID = 'contract-run-1';
  process.env.GITHUB_JOB = 'native-client';
});

test('valid producer identity binds immutable same-job proof and survives fresh revalidation', t => {
  const fixture = makeFixture(t);
  const binding = bindSameJobBootstrap(fixture.options);
  revalidateSameJobBootstrap(binding, fixture.observed(), fixture.options.observerPath);
  assert.equal(binding.summary.sourceSHA, fixture.expected.sourceSHA);
  assert.match(binding.summary.handoffSHA256, /^[0-9a-f]{64}$/);
  assert.match(binding.summary.evidenceSHA256, /^[0-9a-f]{64}$/);
  assert.match(binding.summary.recordsSHA256, /^[0-9a-f]{64}$/);
});

test('missing same-job handoff is rejected in composed mode', t => {
  const fixture = makeFixture(t);
  rmSync(join(fixture.options.suppliedRoot, 'same-job-bootstrap.json'));
  assert.throws(() => bindSameJobBootstrap(fixture.options), /bounded immutable bootstrap member required/);
});

test('tampered producer evidence cannot inherit the signed handoff digest', t => {
  const fixture = makeFixture(t);
  const evidencePath = join(fixture.options.suppliedRoot, 'evidence.json');
  const evidence = JSON.parse(readFileSync(evidencePath, 'utf8')) as Json;
  evidence.status = 'tampered';
  writeFileSync(evidencePath, JSON.stringify(evidence) + '\n');
  assert.throws(() => bindSameJobBootstrap(fixture.options), /bootstrap custody\/terminal proof absent/);
});

test('cross-source bootstrap is rejected against the exact callback source', t => {
  const fixture = makeFixture(t, 'b'.repeat(40));
  fixture.options.expected.sourceSHA = 'a'.repeat(40);
  assert.throws(() => bindSameJobBootstrap(fixture.options), /bootstrap custody\/terminal proof absent/);
});

test('cross-session bootstrap is rejected at fresh pre-submission revalidation', t => {
  const fixture = makeFixture(t);
  const binding = bindSameJobBootstrap(fixture.options);
  assert.throws(() => revalidateSameJobBootstrap(binding,
    fixture.observed({ session: fixture.session + 1 }), fixture.options.observerPath),
  /bootstrap runtime identity changed/);
});

test('composed mode requires its handoff and unrelated mode refuses one', () => {
  assert.throws(() => requireSameJobBootstrapMode('oobe_then_native_callback', ['oobe_then_native_callback'], undefined),
    /same-job bootstrap handoff required in composed mode/);
  assert.throws(() => requireSameJobBootstrapMode('native_callback', ['oobe_then_native_callback'], '/tmp/unrelated'),
    /unexpected bootstrap in unrelated mode/);
});
