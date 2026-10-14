import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  actualBuildProvenance,
  compileManifestJoin,
  dispatchAdmissionJoin,
  packageEvidenceJoin,
  packageExpectedRuns,
  packageExpectedTerminals,
  packageTestPattern,
  processSupervisionJoin,
  selectedTestEvidenceJoin,
  type ActualBuildProvenance,
  type ArchiveEvidence,
  type CompileManifest,
  type FileIdentity,
  type PackageScopeFacts,
  type ProcessSupervisionResult,
  type SelectedTestResult,
} from './package_evidence.mts';

const tuple = {source:'1'.repeat(40),nonce:'2'.repeat(32)};
const digest = (letter: string): string => letter.repeat(64);
const oid = (letter: string): string => letter.repeat(40);
const file = (letter: string): FileIdentity => ({bytes:123,sha256:digest(letter),gitBlob:oid(letter)});
const testBuildInfo = [
  '\tbuild\t-race=true',
  '\tpath\tTEST/installruntime.test.exe',
  '\tmod\tgithub.com/777genius/agent-notifications',
  '\tbuild\t-buildmode=exe',
  '\tbuild\t-compiler=gc',
  '\tbuild\t-tags=windows_callback_asset',
  '\tbuild\t-trimpath=true',
  '\tbuild\tGOOS=windows',
  '\tbuild\tGOARCH=amd64',
  '\tbuild\tCGO_ENABLED=1',
  '\tbuild\tvcs=git',
  '\tbuild\tvcs.revision='+oid('4'),
  '\tbuild\tvcs.modified=false',
  '',
].join('\n');
const shippingBuildInfo = [
  '\tpath\tTEST/claude-notifications.exe',
  '\tmod\tgithub.com/777genius/agent-notifications',
  '\tbuild\t-buildmode=exe',
  '\tbuild\t-compiler=gc',
  '\tbuild\t-tags=windows_callback_asset',
  '\tbuild\t-trimpath=true',
  '\tbuild\tGOOS=windows',
  '\tbuild\tGOARCH=amd64',
  '\tbuild\tCGO_ENABLED=1',
  '\tbuild\tvcs=git',
  '\tbuild\tvcs.revision='+oid('4'),
  '\tbuild\tvcs.modified=false',
  '',
].join('\n');
const raceSymbols = 'internal/race.runtimeAcquire\nruntime.raceinit\n';
const actualBuild: ActualBuildProvenance = actualBuildProvenance(
  Buffer.from(testBuildInfo),Buffer.from(shippingBuildInfo),Buffer.from(raceSymbols),
);
const hashText = (value: string): string => createHash('sha256').update(value).digest('hex');
const compile: CompileManifest = {
  source:tuple.source,
  sources:{
    harnessSHA:tuple.source,harnessTreeSHA:oid('3'),
    coreRefInput:'4'.repeat(40),coreRefExplicit:true,coreResolvedSHA:'4'.repeat(40),coreSHA:'4'.repeat(40),
    coreTreeSHA:oid('5'),coreIsHarnessAncestor:true,checkoutHeadSHA:'4'.repeat(40),
    compileSourceSHA:'4'.repeat(40),nativeBuildSourceSHA:'4'.repeat(40),testSourceCoreSHA:'4'.repeat(40),
    coreFiles:{testSource:file('6')},
    harnessFiles:{
      observer:file('7'),observerProject:file('8'),controller:file('9'),
      packageEvidence:file('a'),evidence:file('b'),strictArchive:file('c'),supervisor:file('d'),
      supervisorTest:file('7'),packageEvidenceTest:file('7'),evidenceTest:file('7'),installedController:file('7'),documentation:file('7'),
    },
  },
  binaries:{
    helperSHA256:digest('e'),shippingBinarySHA256:digest('f'),testBinarySHA256:digest('0'),
    helperMachine:0x8664,shippingMachine:0x8664,testMachine:0x8664,
  },
  buildProvenance:{
    testCommand:['test','-c','-buildvcs=true','-race','-trimpath','-tags=windows_callback_asset','./internal/installruntime'],
    testExitCode:0,testBuildInfoSHA256:hashText(testBuildInfo),
    shippingCommand:['build','-buildvcs=true','-tags=windows_callback_asset','-trimpath','-ldflags=-s -w','./cmd/claude-notifications'],
    shippingExitCode:0,shippingBuildInfoSHA256:hashText(shippingBuildInfo),
    raceSymbolsSHA256:hashText(raceSymbols),
  },
  nativeBuildEvidence:{sourceSHA:'4'.repeat(40),helperSHA256:digest('e'),readerVersion:1,architecture:'amd64',
    helperExecuted:false,installedQualified:false},
  shippingEmbeddedProof:{helperSHA256:digest('e'),binarySHA256:digest('f'),embeddedOffset:4096,
    helperExecuted:false,installedQualified:false},
  testEmbeddedProof:{helperSHA256:digest('e'),binarySHA256:digest('0'),embeddedOffset:8192,
    helperExecuted:false,installedQualified:false},
};
const command = ['-test.v','-test.count=1','-test.run',packageTestPattern,'-test.timeout','6m'] as const;
const processResult: ProcessSupervisionResult = {
  executable:'TEST/installruntime.test.exe',command,rootPid:4242,rootExitCode:0,
  jobObjectAssignedBeforeResume:true,processTreeContained:true,timedOut:false,unknown:false,
  effectState:'known_collected',retryAdmissible:false,activeProcessesAfter:0,
  streamsCollected:true,stdoutEOF:true,stderrEOF:true,error:null,
};
function terminal(): string {
  // Captured from the real Windows Go 1.26 production test run 38510063215.
  // Keep this independent of packageSelectedTests: generated output previously
  // repeated the parser's incorrect assumption about parent/subtest ordering.
  return `=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries
=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-clsid
=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-aumid
=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-shortcut
=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries/windows:commit_decided
=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries/windows:binding_published
--- PASS: TestWindowsNativeRecoverFixedMutationBoundaries (7.47s)
    --- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-clsid (1.94s)
    --- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-aumid (1.43s)
    --- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-shortcut (1.46s)
    --- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:commit_decided (1.31s)
    --- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:binding_published (1.32s)
=== RUN   TestWindowsNativeForeignDecisionRecoveryRefuses
--- PASS: TestWindowsNativeForeignDecisionRecoveryRefuses (0.93s)
PASS
`;
}

const selectedTests: SelectedTestResult = {
  collected:true,streamsCollected:true,timedOut:false,unknown:false,error:null,signal:null,exitCode:0,
  deadlineMs:360000,args:command,stdout:terminal(),stderr:'',process:processResult,
};
const scope: PackageScopeFacts = {
  coreSourceSHA:'4'.repeat(40),harnessSourceSHA:tuple.source,
  nativeAmd64Qualified:false,installedCallbackQualified:false,notificationQualified:false,
  callbackQualified:false,rebootQualified:false,noShowScope:true,
};
const fullName = 'OpenAI.Codex_1.2.3.4_x64__2p2nqsd0c76g0';
const archive = {...tuple,archive_sha:digest('c'),full_name:fullName,family:'OpenAI.Codex_2p2nqsd0c76g0',signature:'system_trust'};
const signature = {
  ...tuple,archive_sha:digest('c'),winverifytrust_result:0,signer_cert_sha:digest('1'),
  signer_display_name:'OpenAI',chain_cert_count:2,counter_signers:0,trust_state_close_result:0,
};
const intake = {
  ...tuple,archive_sha:digest('c'),name:'OpenAI.Codex',manifest_sha:digest('2'),manifest_first4_hex:'3c9a01bf',
  publisher:'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B',family:'OpenAI.Codex_2p2nqsd0c76g0',
  full_name:fullName,architecture:9,resource_absent:true,dependencies_present:false,
  codex_protocol_count:1,signature:'system_trust',
};
const archiveEvidence: ArchiveEvidence = {
  signature,intake,deploymentSignature:signature,deploymentIntake:intake,
};
const baseJoin = {
  tuple,expectedArchiveSHA:digest('c'),expectedFullName:fullName,compile,actualBuild,
  acquisition:{...tuple,official_url:'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',
    actual_url:'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',bytes:4096,sha256:digest('c')},
  archive,archiveEvidence,
  deployment:{...archive,outcome:'installed_exact',add_package_operations:1,immutable_archive_custody:true},
  census:{...tuple,selected_full_name:fullName,family:'OpenAI.Codex_2p2nqsd0c76g0',selection_scope:'current_user',
    architecture:'x64',selected_current_user_package_count:1,official_identity:true,read_only:true},
  selectedTests,scope,observerMachine:0xAA64,
};
assert.deepEqual(packageEvidenceJoin(baseJoin),scope);
// Observations have different clocks; identity and trust facts must still join.
const timedArchiveEvidence = {
  signature:{...signature,boot_ms:10}, deploymentSignature:{...signature,boot_ms:20},
  intake:{...intake,boot_ms:11}, deploymentIntake:{...intake,boot_ms:21},
};
assert.deepEqual(packageEvidenceJoin({...baseJoin,archiveEvidence:timedArchiveEvidence}),scope);

const dispatch = {
  eventName:'workflow_dispatch',repository:'777genius/agent-notifications',runAttempt:1,
  admission:'one-fresh-disposable-package-noshow',phase:'package_noshow',scenario:'package_noshow',
  coreRef:'4'.repeat(40),
} as const;
dispatchAdmissionJoin(dispatch);
dispatchAdmissionJoin({
  eventName:'workflow_dispatch',repository:'777genius/agent-notifications',runAttempt:1,
  admission:'one-fresh-disposable-installed-callback',phase:'archive_intake',scenario:'retained',coreRef:'',
});
assert.throws(() => dispatchAdmissionJoin({...dispatch,phase:'installed'}));
assert.throws(() => dispatchAdmissionJoin({...dispatch,scenario:'basic'}));
assert.throws(() => dispatchAdmissionJoin({...dispatch,coreRef:''}));
assert.throws(() => dispatchAdmissionJoin({...dispatch,coreRef:'A'.repeat(40)}));
assert.throws(() => dispatchAdmissionJoin({...dispatch,runAttempt:2}));
assert.throws(() => dispatchAdmissionJoin({
  eventName:'workflow_dispatch',repository:'777genius/agent-notifications',runAttempt:1,
  admission:'one-fresh-disposable-installed-callback',phase:'package_noshow',scenario:'package_noshow',coreRef:'',
}));

assert.deepEqual(packageExpectedRuns,[
  'TestWindowsNativeRecoverFixedMutationBoundaries',
  'TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-clsid',
  'TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-aumid',
  'TestWindowsNativeRecoverFixedMutationBoundaries/windows:mutated:apply-shortcut',
  'TestWindowsNativeRecoverFixedMutationBoundaries/windows:commit_decided',
  'TestWindowsNativeRecoverFixedMutationBoundaries/windows:binding_published',
  'TestWindowsNativeForeignDecisionRecoveryRefuses',
]);
assert.equal(packageExpectedTerminals.length,7);
assert.equal(packageExpectedTerminals.at(-1),'TestWindowsNativeForeignDecisionRecoveryRefuses');

// Explicit reviewed core identity is mandatory. Empty head fallback and loose
// Git tree syntax are behaviorally refused.
assert.throws(() => compileManifestJoin({...compile,sources:{
  ...compile.sources,coreRefInput:'',coreRefExplicit:false,coreResolvedSHA:tuple.source,coreSHA:tuple.source,
}},tuple,actualBuild));
assert.throws(() => compileManifestJoin({...compile,sources:{
  ...compile.sources,harnessTreeSHA:digest('3'),coreTreeSHA:digest('5'),
}},tuple,actualBuild));
assert.throws(() => compileManifestJoin({...compile,sources:{...compile.sources,coreRefInput:oid('4').slice(1)}},tuple,actualBuild));
assert.throws(() => compileManifestJoin({...compile,sources:{...compile.sources,coreIsHarnessAncestor:false}},tuple,actualBuild));

// The build facts must come from actual buildinfo/race-symbol bytes; a literal
// manifest command or flag cannot replace missing binary provenance.
assert.throws(() => compileManifestJoin(compile,tuple,actualBuildProvenance(
  Buffer.from(testBuildInfo.replace('-tags=windows_callback_asset','')),
  Buffer.from(shippingBuildInfo),Buffer.from(raceSymbols),
)));
assert.throws(() => compileManifestJoin(compile,tuple,actualBuildProvenance(
  Buffer.from(testBuildInfo),Buffer.from(shippingBuildInfo),Buffer.from('runtime.raceinit\n'),
)));
assert.throws(() => compileManifestJoin({...compile,buildProvenance:{
  ...compile.buildProvenance,testBuildInfoSHA256:digest('8'),
}},tuple,actualBuild));
assert.throws(() => compileManifestJoin({...compile,buildProvenance:{
  ...compile.buildProvenance,testCommand:['test','-c','./internal/installruntime'],
}},tuple,actualBuild));
assert.throws(() => compileManifestJoin({...compile,binaries:{...compile.binaries,testMachine:0xaa64}},tuple,actualBuild));
assert.throws(() => compileManifestJoin({...compile,testEmbeddedProof:{
  ...compile.testEmbeddedProof,embeddedOffset:-1,
}},tuple,actualBuild));

selectedTestEvidenceJoin(selectedTests);
const parentSummary = '--- PASS: TestWindowsNativeRecoverFixedMutationBoundaries (7.47s)\n';
const oldChildFirstOrder = terminal().replace(parentSummary,'').replace(
  '=== RUN   TestWindowsNativeForeignDecisionRecoveryRefuses',
  parentSummary+'=== RUN   TestWindowsNativeForeignDecisionRecoveryRefuses',
);
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:oldChildFirstOrder}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal().replace(
  '    --- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:binding_published (1.32s)\n','',
)}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal()+parentSummary}));

assert.throws(() => selectedTestEvidenceJoin({...selectedTests,deadlineMs:360001}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,args:selectedTests.args.slice(0,-2)}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal().replace('--- PASS: TestWindowsNativeRecoverFixedMutationBoundaries/windows:commit_decided ','--- SKIP: TestWindowsNativeRecoverFixedMutationBoundaries/windows:commit_decided ')}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal().replace('=== RUN   TestWindowsNativeRecoverFixedMutationBoundaries/windows:binding_published\n','')}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal()+'=== RUN   TestUnselected\n--- PASS: TestUnselected (0.1s)\n'}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,process:{
  ...processResult,jobObjectAssignedBeforeResume:false,processTreeContained:false,
}}));
const unknownProcess: ProcessSupervisionResult = {
  ...processResult,timedOut:true,unknown:true,effectState:'unknown',
  activeProcessesAfter:1,streamsCollected:false,stdoutEOF:false,stderrEOF:false,
  error:'deadline; helper process tree effect state unknown',
};
processSupervisionJoin(unknownProcess);
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,timedOut:true,unknown:true,
  streamsCollected:false,error:unknownProcess.error,process:unknownProcess}));
assert.throws(() => processSupervisionJoin({...processResult,retryAdmissible:true as false}));

// Every separately observed signature/intake fact joins to both intake and
// deployment; one summary row cannot stand in for the missing strict facts.
assert.throws(() => packageEvidenceJoin({...baseJoin,archiveEvidence:{
  ...archiveEvidence,deploymentIntake:{...intake,architecture:12},
}}));
assert.throws(() => packageEvidenceJoin({...baseJoin,archiveEvidence:{
  ...archiveEvidence,intake:{...intake,dependencies_present:true},
}}));
assert.throws(() => packageEvidenceJoin({...baseJoin,archiveEvidence:{
  ...archiveEvidence,deploymentSignature:{...signature,signer_cert_sha:digest('9')},
}}));
assert.throws(() => packageEvidenceJoin({...baseJoin,archiveEvidence:{
  ...archiveEvidence,signature:{...signature,trust_state_close_result:1},
}}));
assert.throws(() => packageEvidenceJoin({...baseJoin,archiveEvidence:{
  ...archiveEvidence,intake:{...intake,manifest_first4_hex:'0000000'},
}}));
assert.throws(() => packageEvidenceJoin({
  ...baseJoin,expectedArchiveSHA:digest('d'),
}));

console.log('TEST package NoShow evidence joins passed; no native effects executed');

// Reject missing race and foreign/dirty VCS facts from actual Go buildinfo.
for (const bad of [testBuildInfo.replace('-race=true','-race=false'), testBuildInfo.replace('vcs.revision='+oid('4'),'vcs.revision='+oid('9')), testBuildInfo.replace('vcs.modified=false','vcs.modified=true')]) {
  const fact=actualBuildProvenance(Buffer.from(bad),Buffer.from(shippingBuildInfo),Buffer.from(raceSymbols));
  assert.throws(() => compileManifestJoin({...compile,buildProvenance:{...compile.buildProvenance,testBuildInfoSHA256:hashText(bad)}},tuple,fact));
}

// A nonce-free compile manifest binds source only; runtime adds a fresh digest receipt.
assert.deepEqual(compileManifestJoin(compile,tuple,actualBuild),compile.sources);
assert.throws(() => compileManifestJoin({...compile,source:oid('9')},tuple,actualBuild));
