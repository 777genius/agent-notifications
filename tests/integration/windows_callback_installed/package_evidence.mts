import assert from 'node:assert/strict';
import type { Tuple } from './evidence.mts';

export const packageSelectedTests = [
  'TestWindowsNativeRecoverFixedMutationBoundaries',
  'TestWindowsNativeForeignDecisionRecoveryRefuses',
] as const;
export const packageTestPattern =
  '^TestWindowsNative(RecoverFixedMutationBoundaries|ForeignDecisionRecoveryRefuses)$';

export type SourceFacts = {
  harnessSHA: string;
  harnessTreeSHA: string;
  coreRefInput: string;
  coreRefExplicit: boolean;
  coreResolvedSHA: string;
  coreSHA: string;
  coreTreeSHA: string;
  coreIsHarnessAncestor: boolean;
  checkoutHeadSHA: string;
  compileSourceSHA: string;
  nativeBuildSourceSHA: string;
  testSourceCoreSHA: string;
  testSourceSHA256: string;
  observerSHA256: string;
  observerHarnessSHA: string;
  controllerSHA256: string;
  controllerHarnessSHA: string;
};

export type BinaryFacts = {
  helperSHA256: string;
  shippingBinarySHA256: string;
  testBinarySHA256: string;
  helperMachine: number;
  shippingMachine: number;
  testMachine: number;
  helperEmbeddedInShipping: boolean;
  helperEmbeddedInTest: boolean;
};

export type CompileManifest = Tuple & {
  sources: SourceFacts;
  binaries: BinaryFacts;
  nativeBuildEvidence: {
    sourceSHA: string;
    helperSHA256: string;
    architecture: string;
    helperExecuted: boolean;
    installedQualified: boolean;
  };
  shippingEmbeddedProof: {
    helperSHA256: string;
    binarySHA256: string;
    helperExecuted: boolean;
    installedQualified: boolean;
  };
  testEmbeddedProof: {
    helperSHA256: string;
    binarySHA256: string;
    helperExecuted: boolean;
    installedQualified: boolean;
  };
};

export type ArchiveFacts = Tuple & {
  archive_sha: string;
  full_name: string;
  family: string;
  signature: string;
};

export type SelectedTestResult = {
  collected: boolean;
  streamsCollected: boolean;
  timedOut: boolean;
  unknown: boolean;
  error: string | null;
  signal: NodeJS.Signals | null;
  exitCode: number | null;
  deadlineMs: number;
  args: readonly string[];
  stdout: string;
  stderr: string;
};

export type PackageScopeFacts = {
  coreSourceSHA: string;
  harnessSourceSHA: string;
  nativeAmd64Qualified: false;
  installedCallbackQualified: false;
  notificationQualified: false;
  callbackQualified: false;
  rebootQualified: false;
  noShowScope: true;
};

const lower40 = /^[a-f0-9]{40}$/;
const lower64 = /^[a-f0-9]{64}$/;

function joinTuple(value: Tuple, tuple: Tuple): void {
  assert.equal(value.source,tuple.source);
  assert.equal(value.nonce,tuple.nonce);
}

export function selectedTestEvidenceJoin(value: SelectedTestResult): void {
  assert.deepEqual([...value.args],[
    '-test.v','-test.count=1','-test.run',packageTestPattern,'-test.timeout','6m',
  ]);
  assert.equal(value.deadlineMs,360000);
  assert(value.collected && value.streamsCollected && !value.timedOut && !value.unknown);
  assert.equal(value.error,null);
  assert.equal(value.signal,null);
  assert.equal(value.exitCode,0);
  const stdout = value.stdout.replace(/\r\n/gu,'\n');
  const stderr = value.stderr.replace(/\r\n/gu,'\n');
  assert(stdout.length + stderr.length <= 65536);
  assert(!stdout.includes('--- SKIP:'));
  assert(!stderr.includes('--- SKIP:'));
  assert(!/(?:^|\n)--- FAIL:/u.test(stdout));
  assert(!stdout.includes('no tests to run'));
  assert(!stdout.includes('test timed out'));
  assert(!stdout.includes('panic:'));
  for (const name of packageSelectedTests) {
    const started = stdout.indexOf(`=== RUN   ${name}\n`);
    const passed = stdout.indexOf(`--- PASS: ${name} `);
    assert(started >= 0 && passed > started);
    assert(stdout.slice(started,passed).trim().length > 0);
  }
  const terminals = [...stdout.matchAll(/^--- (?:PASS|FAIL|SKIP): ([A-Za-z0-9_]+) /gmu)];
  assert.deepEqual(terminals.map(row => row[1]),[...packageSelectedTests]);
  assert(/(?:^|\n)PASS\n$/u.test(stdout));
}

export function compileManifestJoin(value: CompileManifest, tuple: Tuple): SourceFacts {
  joinTuple(value,tuple);
  const source = value.sources;
  assert.equal(source.harnessSHA,tuple.source);
  assert(source.harnessSHA.match(lower40) && source.coreSHA.match(lower40) && source.coreResolvedSHA.match(lower40));
  assert(source.harnessTreeSHA.match(lower64) && source.coreTreeSHA.match(lower64));
  assert.equal(source.coreResolvedSHA,source.coreSHA);
  assert.equal(source.checkoutHeadSHA,source.coreSHA);
  assert.equal(source.compileSourceSHA,source.coreSHA);
  assert.equal(source.nativeBuildSourceSHA,source.coreSHA);
  assert.equal(source.testSourceCoreSHA,source.coreSHA);
  assert.equal(source.coreIsHarnessAncestor,true);
  assert(source.coreRefInput === '' || source.coreRefInput.match(lower40));
  assert.equal(source.coreRefExplicit,source.coreRefInput !== '');
  if (source.coreRefExplicit) assert.equal(source.coreRefInput,source.coreResolvedSHA);
  assert(source.testSourceSHA256.match(lower64));
  assert(source.observerSHA256.match(lower64) && source.controllerSHA256.match(lower64));
  assert.equal(source.observerHarnessSHA,source.harnessSHA);
  assert.equal(source.controllerHarnessSHA,source.harnessSHA);
  const binary = value.binaries;
  for (const key of ['helperSHA256','shippingBinarySHA256','testBinarySHA256'] as const)
    assert(binary[key].match(lower64));
  assert.equal(binary.helperMachine,0x8664);
  assert.equal(binary.shippingMachine,0x8664);
  assert.equal(binary.testMachine,0x8664);
  assert(binary.helperEmbeddedInShipping && binary.helperEmbeddedInTest);
  assert.equal(value.nativeBuildEvidence.sourceSHA,source.coreSHA);
  assert.equal(value.nativeBuildEvidence.helperSHA256,binary.helperSHA256);
  assert.equal(value.nativeBuildEvidence.architecture,'amd64');
  assert(!value.nativeBuildEvidence.helperExecuted && !value.nativeBuildEvidence.installedQualified);
  for (const proof of [value.shippingEmbeddedProof,value.testEmbeddedProof]) {
    assert.equal(proof.helperSHA256,binary.helperSHA256);
    assert(!proof.helperExecuted && !proof.installedQualified);
  }
  assert.equal(value.shippingEmbeddedProof.binarySHA256,binary.shippingBinarySHA256);
  assert.equal(value.testEmbeddedProof.binarySHA256,binary.testBinarySHA256);
  return source;
}

export function packageEvidenceJoin(input: {
  tuple: Tuple;
  expectedArchiveSHA: string;
  expectedFullName: string;
  compile: CompileManifest;
  acquisition: Tuple & {official_url: string; actual_url: string; bytes: number; sha256: string};
  archive: ArchiveFacts;
  deployment: ArchiveFacts & {
    outcome: string;
    add_package_operations: number;
    immutable_archive_custody: boolean;
  };
  census: Tuple & {
    selected_full_name: string;
    family: string;
    selection_scope: string;
    architecture: string;
    selected_current_user_package_count: number;
    official_identity: boolean;
    read_only: boolean;
  };
  selectedTests: SelectedTestResult;
  observerMachine: number;
  scope: PackageScopeFacts;
}): PackageScopeFacts {
  assert(input.expectedArchiveSHA.match(lower64));
  assert(input.expectedFullName.length > 0 && input.expectedFullName.length <= 512);
  const source = compileManifestJoin(input.compile,input.tuple);
  joinTuple(input.acquisition,input.tuple);
  assert.equal(input.acquisition.official_url,'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix');
  const actual = new URL(input.acquisition.actual_url);
  assert(actual.protocol === 'https:'
    && (actual.hostname === 'persistent.oaistatic.com' || actual.hostname.endsWith('.oaistatic.com')));
  assert(Number.isSafeInteger(input.acquisition.bytes) && input.acquisition.bytes > 0);
  assert.equal(input.acquisition.sha256,input.expectedArchiveSHA);
  joinTuple(input.archive,input.tuple);
  joinTuple(input.deployment,input.tuple);
  joinTuple(input.census,input.tuple);
  for (const fact of [input.archive,input.deployment]) {
    assert.equal(fact.archive_sha,input.expectedArchiveSHA);
    assert.equal(fact.full_name,input.expectedFullName);
    assert.equal(fact.family,'OpenAI.Codex_2p2nqsd0c76g0');
    assert.equal(fact.signature,'system_trust');
  }
  assert.equal(input.deployment.outcome,'installed_exact');
  assert.equal(input.deployment.add_package_operations,1);
  assert.equal(input.deployment.immutable_archive_custody,true);
  assert.equal(input.census.selected_full_name,input.expectedFullName);
  assert.equal(input.census.family,'OpenAI.Codex_2p2nqsd0c76g0');
  assert.equal(input.census.selection_scope,'current_user');
  assert.equal(input.census.architecture,'x64');
  assert.equal(input.census.selected_current_user_package_count,1);
  assert(input.census.official_identity && input.census.read_only);
  assert.equal(input.observerMachine,0xAA64);
  selectedTestEvidenceJoin(input.selectedTests);
  assert.deepEqual(input.scope,{
    coreSourceSHA:source.coreSHA,
    harnessSourceSHA:source.harnessSHA,
    nativeAmd64Qualified:false,
    installedCallbackQualified:false,
    notificationQualified:false,
    callbackQualified:false,
    rebootQualified:false,
    noShowScope:true,
  });
  // Both identities remain visible even when an empty coreRef resolves to the
  // harness commit; neither is derived from or renamed to the other.
  assert.equal(source.harnessSHA,input.tuple.source);
  assert(source.coreSHA.match(lower40));
  return input.scope;
}
