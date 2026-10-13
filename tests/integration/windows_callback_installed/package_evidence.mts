import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import type { Tuple } from './evidence.mts';

export const packageSelectedTests = [
  {
    name: 'TestWindowsNativeRecoverFixedMutationBoundaries',
    subtests: [
      'windows:mutated:apply-clsid',
      'windows:mutated:apply-aumid',
      'windows:mutated:apply-shortcut',
      'windows:commit_decided',
      'windows:binding_published',
    ],
  },
  {name: 'TestWindowsNativeForeignDecisionRecoveryRefuses', subtests: []},
] as const;
export const packageTopLevelTests = packageSelectedTests.map(row => row.name);
export const packageExpectedRuns = packageSelectedTests.flatMap(row =>
  [row.name,...row.subtests.map(name => `${row.name}/${name}`)]);
export const packageExpectedTerminals = packageSelectedTests.flatMap(row =>
  [...row.subtests.map(name => `${row.name}/${name}`),row.name]);
export const packageTestPattern =
  '^TestWindowsNative(RecoverFixedMutationBoundaries|ForeignDecisionRecoveryRefuses)$';

export type DispatchFacts = {
  eventName: string;
  repository: string;
  runAttempt: number;
  admission: string;
  phase: string;
  scenario: string;
  coreRef: string;
};

export type FileIdentity = {
  bytes: number;
  sha256: string;
  gitBlob: string;
};

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
  coreFiles: {
    testSource: FileIdentity;
  };
  harnessFiles: {
    observer: FileIdentity;
    observerProject: FileIdentity;
    controller: FileIdentity;
    packageEvidence: FileIdentity;
    evidence: FileIdentity;
    strictArchive: FileIdentity;
    supervisor: FileIdentity;
  };
};

export type BuildProvenance = {
  testCommand: readonly string[];
  testExitCode: number;
  testBuildInfoSHA256: string;
  shippingCommand: readonly string[];
  shippingExitCode: number;
  shippingBuildInfoSHA256: string;
  raceSymbolsSHA256: string;
};

export type ActualBuildProvenance = {
  testBuildInfo: string;
  shippingBuildInfo: string;
  raceSymbols: string;
  testSettings: Readonly<Record<string,string>>;
  shippingSettings: Readonly<Record<string,string>>;
};

export type BinaryFacts = {
  helperSHA256: string;
  shippingBinarySHA256: string;
  testBinarySHA256: string;
  helperMachine: number;
  shippingMachine: number;
  testMachine: number;
};

export type EmbeddedProof = {
  helperSHA256: string;
  binarySHA256: string;
  embeddedOffset: number;
  helperExecuted: boolean;
  installedQualified: boolean;
};

export type NativeBuildEvidence = {
  sourceSHA: string;
  helperSHA256: string;
  readerVersion: number;
  architecture: string;
  helperExecuted: boolean;
  installedQualified: boolean;
};

export type CompileManifest = Tuple & {
  sources: SourceFacts;
  binaries: BinaryFacts;
  buildProvenance: BuildProvenance;
  nativeBuildEvidence: NativeBuildEvidence;
  shippingEmbeddedProof: EmbeddedProof;
  testEmbeddedProof: EmbeddedProof;
};

export type ArchiveFacts = Tuple & {
  archive_sha: string;
  full_name: string;
  family: string;
  signature: string;
};

export type SignatureFacts = Tuple & {
  archive_sha: string;
  winverifytrust_result: number;
  signer_cert_sha: string;
  signer_display_name: string;
  chain_cert_count: number;
  counter_signers: number;
  trust_state_close_result: number;
};

export type IntakeFacts = Tuple & {
  archive_sha: string;
  name: string;
  manifest_sha: string;
  manifest_first4_hex: string;
  publisher: string;
  family: string;
  full_name: string;
  architecture: number;
  resource_absent: boolean;
  dependencies_present: boolean;
  codex_protocol_count: number;
  signature: string;
};

export type ArchiveEvidence = {
  signature: SignatureFacts;
  intake: IntakeFacts;
  deploymentSignature: SignatureFacts;
  deploymentIntake: IntakeFacts;
};

export type ProcessSupervisionResult = {
  executable: string;
  command: readonly string[];
  rootPid: number;
  rootExitCode: number | null;
  jobObjectAssignedBeforeResume: boolean;
  processTreeContained: boolean;
  timedOut: boolean;
  unknown: boolean;
  effectState: 'known_collected' | 'unknown';
  retryAdmissible: false;
  activeProcessesAfter: number;
  streamsCollected: boolean;
  stdoutEOF: boolean;
  stderrEOF: boolean;
  error: string | null;
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
  process: ProcessSupervisionResult;
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
const expectedPublisher = 'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B';
const expectedName = 'OpenAI.Codex';
const expectedFamily = 'OpenAI.Codex_2p2nqsd0c76g0';
const x64AppxArchitecture = 9;

export function gitBlob(bytes: Uint8Array): string {
  const prefix = Buffer.from(`blob ${bytes.length}\0`);
  return createHash('sha1').update(prefix).update(bytes).digest('hex');
}

export function dispatchAdmissionJoin(value: DispatchFacts): void {
  assert.equal(value.eventName,'workflow_dispatch');
  assert.equal(value.repository,'777genius/agent-notifications');
  assert.equal(value.runAttempt,1);
  if (value.admission === 'one-fresh-disposable-package-noshow') {
    assert.equal(value.phase,'package_noshow');
    assert.equal(value.scenario,'package_noshow');
    assert(value.coreRef.match(lower40));
    return;
  }
  assert.equal(value.admission,'one-fresh-disposable-installed-callback');
  assert(value.phase === 'archive_intake' || value.phase === 'installed');
  assert(value.scenario === 'basic' || value.scenario === 'retained');
  assert.equal(value.coreRef,'');
}

function joinTuple(value: Tuple, tuple: Tuple): void {
  assert.equal(value.source,tuple.source);
  assert.equal(value.nonce,tuple.nonce);
}

function fileIdentityJoin(value: FileIdentity, actual: FileIdentity): void {
  assert(Number.isSafeInteger(value.bytes) && value.bytes > 0 && value.bytes <= 67108864);
  assert(value.sha256.match(lower64) && value.gitBlob.match(lower40));
  assert.deepEqual(value,actual);
}

function parseBuildSettings(raw: string): Record<string,string> {
  assert(raw.length > 0 && raw.length <= 65536);
  assert(!raw.includes('\0'));
  const settings: Record<string,string> = {};
  for (const line of raw.replace(/\r\n/gu,'\n').split('\n')) {
    const match = /^build\t-([^=\t]+)=(.*)$/u.exec(line);
    if (!match) continue;
    const key = match[1]!;
    assert(!(key in settings),`duplicate build setting ${key}`);
    settings[key] = match[2]!;
  }
  return settings;
}

export function actualBuildProvenance(
  testBuildInfo: Uint8Array,
  shippingBuildInfo: Uint8Array,
  raceSymbols: Uint8Array,
): ActualBuildProvenance {
  const testText = Buffer.from(testBuildInfo).toString('utf8');
  const shippingText = Buffer.from(shippingBuildInfo).toString('utf8');
  const raceText = Buffer.from(raceSymbols).toString('utf8');
  const raceLines = raceText.replace(/\r\n/gu,'\n').split('\n')
    .map(line => line.trim()).filter(line => line.length > 0);
  assert(raceLines.length > 0 && raceLines.length <= 8);
  assert(raceLines.some(line => line.includes('internal/race.')));
  return {
    testBuildInfo:testText,
    shippingBuildInfo:shippingText,
    raceSymbols:raceText,
    testSettings:parseBuildSettings(testText),
    shippingSettings:parseBuildSettings(shippingText),
  };
}

export function processSupervisionJoin(value: ProcessSupervisionResult): void {
  assert(value.executable.length > 0);
  assert(Number.isSafeInteger(value.rootPid) && value.rootPid > 0);
  assert.equal(value.jobObjectAssignedBeforeResume,true);
  assert.equal(value.processTreeContained,true);
  assert.equal(value.retryAdmissible,false);
  assert(Number.isSafeInteger(value.activeProcessesAfter) && value.activeProcessesAfter >= 0);
  assert.equal(value.streamsCollected,value.stdoutEOF && value.stderrEOF && value.activeProcessesAfter === 0);
  assert.equal(value.effectState,value.unknown ? 'unknown' : 'known_collected');
  if (!value.timedOut && !value.unknown) {
    const rootExitCode = value.rootExitCode;
    assert(rootExitCode !== null);
    assert(Number.isSafeInteger(rootExitCode) && rootExitCode >= 0);
    assert.equal(value.error,null);
  } else {
    assert(value.error !== null);
  }
}

export function selectedTestEvidenceJoin(value: SelectedTestResult): void {
  assert.deepEqual([...value.args],[
    '-test.v','-test.count=1','-test.run',packageTestPattern,'-test.timeout','6m',
  ]);
  assert.equal(value.deadlineMs,360000);
  processSupervisionJoin(value.process);
  assert.deepEqual([...value.process.command],[...value.args]);
  assert(value.collected && value.streamsCollected && !value.timedOut && !value.unknown);
  assert.equal(value.error,null);
  assert.equal(value.signal,null);
  assert.equal(value.exitCode,0);
  assert.equal(value.process.rootExitCode,0);
  const stdout = value.stdout.replace(/\r\n/gu,'\n');
  const stderr = value.stderr.replace(/\r\n/gu,'\n');
  assert(stdout.length + stderr.length <= 65536);
  assert(!stdout.includes('--- SKIP:'));
  assert(!stderr.includes('--- SKIP:'));
  assert(!/(?:^|\n)--- FAIL:/u.test(stdout));
  assert(!stdout.includes('no tests to run'));
  assert(!stdout.includes('test timed out'));
  assert(!stdout.includes('panic:'));
  const runs = [...stdout.matchAll(/^=== RUN   (Test[A-Za-z0-9_]+(?:\/[A-Za-z0-9_.:-]+)*)$/gmu)]
    .map(row => row[1]!);
  assert.deepEqual(runs,[...packageExpectedRuns]);
  const terminals = [...stdout.matchAll(
    /^(\s*)--- (PASS|FAIL|SKIP): (Test[A-Za-z0-9_]+(?:\/[A-Za-z0-9_.:-]+)*) \(([0-9]+(?:\.[0-9]+)?s)\)$/gmu,
  )];
  assert.deepEqual(terminals.map(row => row[3]),[...packageExpectedTerminals]);
  assert(terminals.every(row => row[2] === 'PASS'));
  assert(terminals.every(row => row[1] === (row[3]!.includes('/') ? '    ' : '')));
  assert(/(?:^|\n)PASS\n$/u.test(stdout));
}

export function buildProvenanceJoin(
  value: BuildProvenance,
  tuple: Tuple,
  actual: ActualBuildProvenance,
): void {
  void tuple;
  assert.deepEqual([...value.testCommand],[
    'test','-c','-race','-trimpath','-tags=windows_callback_asset','./internal/installruntime',
  ]);
  assert.deepEqual([...value.shippingCommand],[
    'build','-tags=windows_callback_asset','-trimpath','-ldflags=-s -w','./cmd/claude-notifications',
  ]);
  assert.equal(value.testExitCode,0);
  assert.equal(value.shippingExitCode,0);
  assert(value.testBuildInfoSHA256.match(lower64)
    && value.shippingBuildInfoSHA256.match(lower64)
    && value.raceSymbolsSHA256.match(lower64));
  assert.equal(value.testBuildInfoSHA256,createHash('sha256').update(actual.testBuildInfo).digest('hex'));
  assert.equal(value.shippingBuildInfoSHA256,createHash('sha256').update(actual.shippingBuildInfo).digest('hex'));
  assert.equal(value.raceSymbolsSHA256,createHash('sha256').update(actual.raceSymbols).digest('hex'));
  for (const settings of [actual.testSettings,actual.shippingSettings]) {
    assert.equal(settings['buildmode'],'exe');
    assert.equal(settings['trimpath'],'true');
    assert.equal(settings['tags'],'windows_callback_asset');
  }
  assert(actual.raceSymbols.split(/\r?\n/u).some(line => line.includes('internal/race.')));
}

export function compileManifestJoin(
  value: CompileManifest,
  tuple: Tuple,
  actualBuild: ActualBuildProvenance,
): SourceFacts {
  joinTuple(value,tuple);
  const source = value.sources;
  assert.equal(source.harnessSHA,tuple.source);
  assert(source.harnessSHA.match(lower40) && source.coreSHA.match(lower40) && source.coreResolvedSHA.match(lower40));
  assert(source.harnessTreeSHA.match(lower40) && source.coreTreeSHA.match(lower40));
  assert.equal(source.coreResolvedSHA,source.coreSHA);
  assert.equal(source.checkoutHeadSHA,source.coreSHA);
  assert.equal(source.compileSourceSHA,source.coreSHA);
  assert.equal(source.nativeBuildSourceSHA,source.coreSHA);
  assert.equal(source.testSourceCoreSHA,source.coreSHA);
  assert.equal(source.coreIsHarnessAncestor,true);
  assert(source.coreRefInput.match(lower40));
  assert.equal(source.coreRefExplicit,true);
  assert.equal(source.coreRefInput,source.coreResolvedSHA);
  assert.equal(source.coreRefInput,source.coreSHA);
  const identities: FileIdentity[] = [
    source.coreFiles.testSource,
    source.harnessFiles.observer,
    source.harnessFiles.observerProject,
    source.harnessFiles.controller,
    source.harnessFiles.packageEvidence,
    source.harnessFiles.evidence,
    source.harnessFiles.strictArchive,
    source.harnessFiles.supervisor,
  ];
  for (const identity of identities) {
    assert(Number.isSafeInteger(identity.bytes) && identity.bytes > 0);
    assert(identity.sha256.match(lower64) && identity.gitBlob.match(lower40));
  }
  buildProvenanceJoin(value.buildProvenance,tuple,actualBuild);
  const binary = value.binaries;
  for (const key of ['helperSHA256','shippingBinarySHA256','testBinarySHA256'] as const)
    assert(binary[key].match(lower64));
  assert.equal(binary.helperMachine,0x8664);
  assert.equal(binary.shippingMachine,0x8664);
  assert.equal(binary.testMachine,0x8664);
  assert.equal(value.nativeBuildEvidence.sourceSHA,source.coreSHA);
  assert.equal(value.nativeBuildEvidence.helperSHA256,binary.helperSHA256);
  assert.equal(value.nativeBuildEvidence.readerVersion,1);
  assert.equal(value.nativeBuildEvidence.architecture,'amd64');
  assert(!value.nativeBuildEvidence.helperExecuted && !value.nativeBuildEvidence.installedQualified);
  for (const proof of [value.shippingEmbeddedProof,value.testEmbeddedProof]) {
    assert.equal(proof.helperSHA256,binary.helperSHA256);
    assert(Number.isSafeInteger(proof.embeddedOffset) && proof.embeddedOffset >= 0);
    assert(!proof.helperExecuted && !proof.installedQualified);
  }
  assert.equal(value.shippingEmbeddedProof.binarySHA256,binary.shippingBinarySHA256);
  assert.equal(value.testEmbeddedProof.binarySHA256,binary.testBinarySHA256);
  return source;
}

function signatureJoin(value: SignatureFacts, tuple: Tuple, archiveSHA: string): void {
  joinTuple(value,tuple);
  assert.equal(value.archive_sha,archiveSHA);
  assert.equal(value.winverifytrust_result,0);
  assert.equal(value.trust_state_close_result,0);
  assert(value.signer_cert_sha.match(lower64));
  assert(value.signer_display_name.length > 0 && value.signer_display_name.length <= 1024);
  assert(Number.isSafeInteger(value.chain_cert_count) && value.chain_cert_count >= 1 && value.chain_cert_count <= 32);
  assert(Number.isSafeInteger(value.counter_signers) && value.counter_signers >= 0 && value.counter_signers <= 32);
}

function intakeJoin(value: IntakeFacts, tuple: Tuple, archiveSHA: string, fullName: string): void {
  joinTuple(value,tuple);
  assert.equal(value.archive_sha,archiveSHA);
  assert.equal(value.name,expectedName);
  assert.equal(value.publisher,expectedPublisher);
  assert.equal(value.family,expectedFamily);
  assert.equal(value.full_name,fullName);
  assert.equal(value.architecture,x64AppxArchitecture);
  assert.equal(value.resource_absent,true);
  assert.equal(value.dependencies_present,false);
  assert.equal(value.codex_protocol_count,1);
  assert.equal(value.signature,'system_trust');
  assert(value.manifest_sha.match(lower64));
  assert(value.manifest_first4_hex.match(/^[a-f0-9]{8}$/u));
}

export function packageEvidenceJoin(input: {
  tuple: Tuple;
  expectedArchiveSHA: string;
  expectedFullName: string;
  compile: CompileManifest;
  actualBuild: ActualBuildProvenance;
  acquisition: Tuple & {official_url: string; actual_url: string; bytes: number; sha256: string};
  archive: ArchiveFacts;
  archiveEvidence: ArchiveEvidence;
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
  const source = compileManifestJoin(input.compile,input.tuple,input.actualBuild);
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
    assert.equal(fact.family,expectedFamily);
    assert.equal(fact.signature,'system_trust');
  }
  for (const signature of [
    input.archiveEvidence.signature,
    input.archiveEvidence.deploymentSignature,
  ]) signatureJoin(signature,input.tuple,input.expectedArchiveSHA);
  for (const key of ["source","nonce","archive_sha","winverifytrust_result","trust_state_close_result",
    "signer_cert_sha","signer_display_name","chain_cert_count","counter_signers"] as const) {
    assert.equal(input.archiveEvidence.deploymentSignature[key],input.archiveEvidence.signature[key]);
  }
  for (const intake of [
    input.archiveEvidence.intake,
    input.archiveEvidence.deploymentIntake,
  ]) intakeJoin(intake,input.tuple,input.expectedArchiveSHA,input.expectedFullName);
  for (const key of ["source","nonce","archive_sha","name","publisher","family","full_name",
    "architecture","resource_absent","dependencies_present","codex_protocol_count","signature",
    "manifest_sha","manifest_first4_hex"] as const) {
    assert.equal(input.archiveEvidence.deploymentIntake[key],input.archiveEvidence.intake[key]);
  }
  assert.equal(input.deployment.outcome,'installed_exact');
  assert.equal(input.deployment.add_package_operations,1);
  assert.equal(input.deployment.immutable_archive_custody,true);
  assert.equal(input.census.selected_full_name,input.expectedFullName);
  assert.equal(input.census.family,expectedFamily);
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
  assert.equal(source.harnessSHA,input.tuple.source);
  assert(source.coreSHA.match(lower40));
  return input.scope;
}
