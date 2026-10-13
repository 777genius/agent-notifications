import assert from 'node:assert/strict';
import {
  compileManifestJoin,
  packageEvidenceJoin,
  packageSelectedTests,
  packageTestPattern,
  selectedTestEvidenceJoin,
  type CompileManifest,
  type PackageScopeFacts,
  type SelectedTestResult,
} from './package_evidence.mts';

const tuple = {source:'1'.repeat(40),nonce:'2'.repeat(32)};
const digest = (letter: string): string => letter.repeat(64);
const compile: CompileManifest = {
  ...tuple,
  sources:{
    harnessSHA:tuple.source,harnessTreeSHA:digest('3'),
    coreRefInput:'4'.repeat(40),coreRefExplicit:true,coreResolvedSHA:'4'.repeat(40),coreSHA:'4'.repeat(40),
    coreTreeSHA:digest('5'),coreIsHarnessAncestor:true,checkoutHeadSHA:'4'.repeat(40),
    compileSourceSHA:'4'.repeat(40),nativeBuildSourceSHA:'4'.repeat(40),testSourceCoreSHA:'4'.repeat(40),
    testSourceSHA256:digest('6'),observerSHA256:digest('7'),observerHarnessSHA:tuple.source,
    controllerSHA256:digest('8'),controllerHarnessSHA:tuple.source,
  },
  binaries:{
    helperSHA256:digest('9'),shippingBinarySHA256:digest('a'),testBinarySHA256:digest('b'),
    helperMachine:0x8664,shippingMachine:0x8664,testMachine:0x8664,
    helperEmbeddedInShipping:true,helperEmbeddedInTest:true,
  },
  nativeBuildEvidence:{sourceSHA:'4'.repeat(40),helperSHA256:digest('9'),architecture:'amd64',
    helperExecuted:false,installedQualified:false},
  shippingEmbeddedProof:{helperSHA256:digest('9'),binarySHA256:digest('a'),
    helperExecuted:false,installedQualified:false},
  testEmbeddedProof:{helperSHA256:digest('9'),binarySHA256:digest('b'),
    helperExecuted:false,installedQualified:false},
};
const terminal = (names: readonly string[]=packageSelectedTests): string =>
  names.map(name => `=== RUN   ${name}\nowned case output ${name}\n--- PASS: ${name} (0.1s)\n`).join('') + 'PASS\n';
const selectedTests: SelectedTestResult = {
  collected:true,streamsCollected:true,timedOut:false,unknown:false,error:null,signal:null,exitCode:0,
  deadlineMs:360000,args:['-test.v','-test.count=1','-test.run',packageTestPattern,'-test.timeout','6m'],
  stdout:terminal(),stderr:'',
};
const scope: PackageScopeFacts = {
  coreSourceSHA:'4'.repeat(40),harnessSourceSHA:tuple.source,
  nativeAmd64Qualified:false,installedCallbackQualified:false,notificationQualified:false,
  callbackQualified:false,rebootQualified:false,noShowScope:true,
};
const fullName = 'OpenAI.Codex_1.2.3.4_x64__2p2nqsd0c76g0';
const archive = {...tuple,archive_sha:digest('c'),full_name:fullName,family:'OpenAI.Codex_2p2nqsd0c76g0',signature:'system_trust'};
const joined = () => packageEvidenceJoin({
  tuple,expectedArchiveSHA:digest('c'),expectedFullName:fullName,compile,
  acquisition:{...tuple,official_url:'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',
    actual_url:'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',bytes:4096,sha256:digest('c')},
  archive,deployment:{...archive,outcome:'installed_exact',add_package_operations:1,immutable_archive_custody:true},
  census:{...tuple,selected_full_name:fullName,family:'OpenAI.Codex_2p2nqsd0c76g0',selection_scope:'current_user',
    architecture:'x64',selected_current_user_package_count:1,official_identity:true,read_only:true},
  selectedTests,scope,
  observerMachine:0xAA64,
});
assert.deepEqual(joined(),scope);

// Separate identities remain joinable without treating the core commit as the
// harness commit, even when empty coreRef resolves to the same current head.
assert.equal(compileManifestJoin({...compile,sources:{
  ...compile.sources,coreRefInput:'',coreRefExplicit:false,coreResolvedSHA:tuple.source,coreSHA:tuple.source,
  checkoutHeadSHA:tuple.source,compileSourceSHA:tuple.source,nativeBuildSourceSHA:tuple.source,testSourceCoreSHA:tuple.source,
},nativeBuildEvidence:{...compile.nativeBuildEvidence,sourceSHA:tuple.source}},tuple).coreSHA,tuple.source);
assert.throws(() => compileManifestJoin({...compile,sources:{...compile.sources,coreSHA:tuple.source}},tuple));
assert.throws(() => compileManifestJoin({...compile,sources:{...compile.sources,coreIsHarnessAncestor:false}},tuple));
assert.throws(() => compileManifestJoin({...compile,sources:{...compile.sources,coreRefInput:'4'.repeat(39)}},tuple));
assert.throws(() => compileManifestJoin({...compile,binaries:{...compile.binaries,testMachine:0xaa64}},tuple));
assert.throws(() => compileManifestJoin({...compile,binaries:{...compile.binaries,helperEmbeddedInTest:false}},tuple));

selectedTestEvidenceJoin(selectedTests);
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,deadlineMs:360001}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,args:selectedTests.args.slice(0,-2)}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal().replace('--- PASS: TestWindowsNativeRecoverFixedMutationBoundaries ','--- SKIP: TestWindowsNativeRecoverFixedMutationBoundaries ')}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal([packageSelectedTests[0]!])}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,stdout:terminal()+'=== RUN   TestUnselected\n--- PASS: TestUnselected (0.1s)\n'}));
assert.throws(() => selectedTestEvidenceJoin({...selectedTests,timedOut:true,unknown:true}));

assert.throws(() => packageEvidenceJoin({
  ...({} as Parameters<typeof packageEvidenceJoin>[0]),
  tuple,expectedArchiveSHA:digest('d'),expectedFullName:fullName,compile,
  acquisition:{...tuple,official_url:'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',
    actual_url:'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',bytes:4096,sha256:digest('c')},
  archive,deployment:{...archive,outcome:'installed_exact',add_package_operations:1,immutable_archive_custody:true},
  census:{...tuple,selected_full_name:fullName,family:'OpenAI.Codex_2p2nqsd0c76g0',selection_scope:'current_user',
    architecture:'x64',selected_current_user_package_count:1,official_identity:true,read_only:true},
  selectedTests,scope,
  observerMachine:0xAA64,
}));
console.log('TEST package NoShow evidence joins passed; no native effects executed');
