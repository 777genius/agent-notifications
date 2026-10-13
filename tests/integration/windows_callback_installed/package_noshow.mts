import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { randomBytes, createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, open, readFile, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { promisify } from 'node:util';
import { sha, type Tuple } from './evidence.mts';
import { acquireOfficialArchive } from './strict_archive.mts';
import {
  actualBuildProvenance,
  compileManifestJoin,
  dispatchAdmissionJoin,
  packageEvidenceJoin,
  packageTestPattern,
  processSupervisionJoin,
  type ActualBuildProvenance,
  type ArchiveEvidence,
  type ArchiveFacts,
  type BinaryFacts,
  type CompileManifest,
  type FileIdentity,
  type PackageScopeFacts,
  type IntakeFacts,
  type ProcessSupervisionResult,
  type SignatureFacts,
  type SelectedTestResult,
  type SourceFacts,
} from './package_evidence.mts';

type Acquisition = Tuple & {official_url: string; actual_url: string; bytes: number; sha256: string};
type Census = Tuple & {
  selected_full_name: string;
  family: string;
  selection_scope: string;
  architecture: string;
  selected_current_user_package_count: number;
  official_identity: boolean;
  read_only: boolean;
};
type BoundedCollection = {
  process: ProcessSupervisionResult;
  stdout: string;
  stderr: string;
  supervisorExitCode: number;
  supervisorSignal: NodeJS.Signals | null;
};

const execFileAsync = promisify(execFile);
const env = process.env;
assert.equal(env.GITHUB_ACTIONS,'true');
assert.equal(process.platform,'win32');
dispatchAdmissionJoin({
  eventName:env.GITHUB_EVENT_NAME!,
  repository:env.GITHUB_REPOSITORY!,
  runAttempt:Number(env.GITHUB_RUN_ATTEMPT),
  admission:env.TEST_INSTALLED_ADMISSION!,
  phase:env.TEST_INSTALLED_PHASE!,
  scenario:env.TEST_INSTALLED_SCENARIO!,
  coreRef:env.TEST_CORE_REF!,
});

const harnessSHA = env.GITHUB_SHA!;
const coreRef = env.TEST_CORE_REF!;
const coreSHA = env.TEST_CORE_SHA!;
assert(coreRef.match(/^[a-f0-9]{40}$/));
assert(coreSHA.match(/^[a-f0-9]{40}$/));
assert.equal(coreRef,coreSHA);
assert(harnessSHA.match(/^[a-f0-9]{40}$/));
const tuple: Tuple = {source:harnessSHA,nonce:randomBytes(16).toString('hex')};
const root = path.join(env.RUNNER_TEMP!,'TEST-installed-'+tuple.nonce);
const artifact = path.resolve(env.TEST_INSTALLED_ARTIFACT!);
const observer = path.resolve(env.TEST_INSTALLED_OBSERVER!);
const harnessSource = path.resolve(env.GITHUB_WORKSPACE!);
const coreSource = path.resolve(env.TEST_CORE_SOURCE!);
const supervisor = path.join(harnessSource,'tests','integration','windows_callback_installed','bounded_job.ps1');
const helper = path.join(artifact,'core-helper.exe');
const shipping = path.join(artifact,'core-claude-notifications-windows-amd64.exe');
const testBinary = path.join(artifact,'installruntime.test.exe');
const compilePath = path.join(artifact,'compile-manifest.json');
const expectedArchiveSHA = env.TEST_EXPECTED_ARCHIVE_SHA!;
const expectedFullName = env.TEST_EXPECTED_FULL_NAME!;
const selectedArgs = ['-test.v','-test.count=1','-test.run',packageTestPattern,'-test.timeout','6m'] as const;
const scope: PackageScopeFacts = {
  coreSourceSHA:coreSHA,
  harnessSourceSHA:harnessSHA,
  nativeAmd64Qualified:false,
  installedCallbackQualified:false,
  notificationQualified:false,
  callbackQualified:false,
  rebootQualified:false,
  noShowScope:true,
};
const childEnv = {...env,TEST_WINDOWS_INSTALLED_RECOVERY:'fresh-owned-account-no-show'};
let count = 0;

async function boundedRead(file: string, limit=65536): Promise<Buffer> {
  const h = await open(file,'r');
  try {
    const info = await h.stat();
    assert(info.isFile() && info.nlink === 1 && info.size > 0 && info.size <= limit);
    return await h.readFile();
  } finally {
    await h.close();
  }
}

async function persist(leaf: string, value: unknown): Promise<void> {
  const data = Buffer.from(JSON.stringify(value));
  assert(data.length <= 65536);
  const h = await open(path.join(root,leaf),'wx');
  try {
    await h.writeFile(data);
    await h.sync();
  } finally {
    await h.close();
  }
}

async function hashFile(file: string): Promise<string> {
  const h = createHash('sha256');
  for await (const chunk of createReadStream(file)) h.update(chunk as Buffer);
  return h.digest('hex');
}

async function fileIdentity(file: string, repository: string): Promise<FileIdentity> {
  const info = await stat(file);
  assert(info.isFile() && info.nlink === 1 && info.size > 0 && info.size <= 67108864);
  const bytes = await readFile(file);
  assert.equal(bytes.length,info.size);
  // Git applies the checked-in text/EOL attributes (PowerShell is CRLF).
  // SHA256 and size retain the exact bytes actually compiled/executed.
  return {bytes:info.size,sha256:sha(bytes),gitBlob:await git(repository,['hash-object','--',file])};
}

function machine(bytes: Buffer): number {
  assert(bytes.length >= 256 && bytes.subarray(0,2).toString() === 'MZ');
  const p = bytes.readUInt32LE(60);
  assert(p+6 <= bytes.length && bytes.subarray(p,p+4).equals(Buffer.from([80,69,0,0])));
  return bytes.readUInt16LE(p+4);
}

async function readJson<T>(file: string): Promise<T> {
  return JSON.parse((await boundedRead(file)).toString()) as T;
}

async function git(rootPath: string, args: readonly string[]): Promise<string> {
  const result = await execFileAsync('git',args,{cwd:rootPath,windowsHide:true,maxBuffer:65536,timeout:30000});
  return result.stdout.trim();
}

async function verifyGitIdentity(
  source: SourceFacts,
): Promise<void> {
  const harnessTree = await git(harnessSource,['rev-parse',`${harnessSHA}^{tree}`]);
  const coreTreeOuter = await git(harnessSource,['rev-parse',`${coreSHA}^{tree}`]);
  const coreTreeCheckout = await git(coreSource,['rev-parse',`${coreSHA}^{tree}`]);
  assert.equal(harnessTree,source.harnessTreeSHA);
  assert.equal(coreTreeOuter,source.coreTreeSHA);
  assert.equal(coreTreeCheckout,source.coreTreeSHA);
  await git(harnessSource,['cat-file','-e',`${coreSHA}^{commit}`]);
  await git(harnessSource,['merge-base','--is-ancestor',coreSHA,harnessSHA]);
  const identities = {
    'tests/integration/windows_callback_installed/observer.cpp':source.harnessFiles.observer,
    'tests/integration/windows_callback_installed/Observer.vcxproj':source.harnessFiles.observerProject,
    'tests/integration/windows_callback_installed/package_noshow.mts':source.harnessFiles.controller,
    'tests/integration/windows_callback_installed/package_evidence.mts':source.harnessFiles.packageEvidence,
    'tests/integration/windows_callback_installed/evidence.mts':source.harnessFiles.evidence,
    'tests/integration/windows_callback_installed/strict_archive.mts':source.harnessFiles.strictArchive,
    'tests/integration/windows_callback_installed/bounded_job.ps1':source.harnessFiles.supervisor,
  } as const;
  for (const [relative,expected] of Object.entries(identities)) {
    const actual = await fileIdentity(path.join(harnessSource,relative),harnessSource);
    assert.deepEqual(actual,expected);
    assert.equal(await git(harnessSource,['rev-parse',`${harnessSHA}:${relative}`]),actual.gitBlob);
  }
  const coreRelative = 'internal/installruntime/windows_generation_native_test.go';
  const coreActual = await fileIdentity(path.join(coreSource,coreRelative),coreSource);
  assert.deepEqual(coreActual,source.coreFiles.testSource);
  assert.equal(await git(coreSource,['rev-parse',`${coreSHA}:${coreRelative}`]),coreActual.gitBlob);
  assert.equal(await hashFile(path.resolve(process.argv[1]!)),source.harnessFiles.controller.sha256);
  assert.equal(await hashFile(supervisor),source.harnessFiles.supervisor.sha256);
}

async function runBounded(
  label: string,
  executable: string,
  args: readonly string[],
  deadlineMs: number,
): Promise<BoundedCollection> {
  const id = (++count).toString().padStart(2,'0');
  const stem = `${id}-${label}`;
  const argsPath = path.join(root,stem+'.args.json');
  const stdoutPath = path.join(root,stem+'.stdout.txt');
  const stderrPath = path.join(root,stem+'.stderr.txt');
  const resultPath = path.join(root,stem+'.supervision.json');
  await persist(stem+'.intent.json',{...tuple,executable,args,deadlineMs,jobObjectContainment:true,noRetry:true});
  await writeFile(argsPath,JSON.stringify([...args]),{flag:'wx'});
  const supervisorArgs = [
    '-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',supervisor,
    '-Executable',executable,'-ArgumentsJson',argsPath,'-StdoutPath',stdoutPath,
    '-StderrPath',stderrPath,'-TimeoutMilliseconds',String(deadlineMs),'-ResultPath',resultPath,
  ];
  return await new Promise<BoundedCollection>((resolve,reject) => {
    const child = spawn('powershell.exe',supervisorArgs,{
      env:childEnv,stdio:['ignore','pipe','pipe'],windowsHide:true,
    });
    let supervisorOut = '', supervisorErr = '', supervisorBytes = 0, fault: Error | undefined;
    const observe = (chunk: Buffer, stderr: boolean): void => {
      supervisorBytes += chunk.length;
      if (supervisorBytes > 65536) {
        fault ??= new Error('bounded supervisor output cap');
        child.kill();
      } else if (stderr) supervisorErr += chunk.toString('utf8');
      else supervisorOut += chunk.toString('utf8');
    };
    child.stdout.on('data',(chunk: Buffer) => observe(chunk,false));
    child.stderr.on('data',(chunk: Buffer) => observe(chunk,true));
    child.on('error',(reason: Error) => { fault ??= reason; });
    const timer = setTimeout(() => {
      fault ??= new Error('bounded supervisor outer deadline');
      child.kill();
    },deadlineMs+95000);
    child.once('close',async (code,signal) => {
      clearTimeout(timer);
      try {
        if (fault) throw fault;
        if (code !== 0 && code !== 2) throw new Error(`bounded supervisor failed code=${code} signal=${signal}`);
        const process = await readJson<ProcessSupervisionResult>(resultPath);
        const stdout = (await boundedRead(stdoutPath)).toString('utf8');
        const stderr = (await boundedRead(stderrPath)).toString('utf8');
        const collection: BoundedCollection = {
          process,stdout,stderr,supervisorExitCode:code,supervisorSignal:signal,
        };
        await persist(stem+'.collected.json',{
          ...tuple,
          supervisorExitCode:code,
          supervisorSignal:signal,
          supervisorStdout:supervisorOut.slice(0,4096),
          supervisorStderr:supervisorErr.slice(0,4096),
          ...process,
          stdout:undefined,
          stderr:undefined,
          stdoutSHA256:sha(Buffer.from(stdout)),
          stderrSHA256:sha(Buffer.from(stderr)),
          stdoutBytes:Buffer.byteLength(stdout),
          stderrBytes:Buffer.byteLength(stderr),
        });
        resolve(collection);
      } catch (reason) {
        reject(reason);
      }
    });
  });
}

async function runObserver(mode: 'archive'|'deploy'|'package-census'): Promise<void> {
  const result = await runBounded(`observer-${mode}`,observer,[mode,root,tuple.nonce,harnessSHA],140000);
  processSupervisionJoin(result.process);
  if (result.process.rootExitCode !== 0 || result.process.unknown || result.process.timedOut)
    throw new Error(`observer ${mode} failed or remained unknown`);
}

async function collectSelectedTests(): Promise<SelectedTestResult> {
  const result = await runBounded('selected-tests',testBinary,selectedArgs,360000);
  await writeFile(path.join(root,'selected-tests.stdout.txt'),result.stdout,{flag:'wx'});
  await writeFile(path.join(root,'selected-tests.stderr.txt'),result.stderr,{flag:'wx'});
  return {
    collected:result.process.rootPid > 0,
    streamsCollected:result.process.streamsCollected,
    timedOut:result.process.timedOut,
    unknown:result.process.unknown,
    error:result.process.error,
    signal:result.supervisorSignal,
    exitCode:result.process.rootExitCode,
    deadlineMs:360000,
    args:selectedArgs,
    stdout:result.stdout,
    stderr:result.stderr,
    process:result.process,
  };
}

async function actualBinaryFacts(manifest: BinaryFacts, testProof: {embeddedOffset:number}, shippingProof: {embeddedOffset:number}): Promise<void> {
  const helperRaw = await boundedRead(helper,16*1024*1024);
  const shippingRaw = await boundedRead(shipping,32*1024*1024);
  const testRaw = await boundedRead(testBinary,32*1024*1024);
  assert.equal(sha(helperRaw),manifest.helperSHA256);
  assert.equal(sha(shippingRaw),manifest.shippingBinarySHA256);
  assert.equal(sha(testRaw),manifest.testBinarySHA256);
  assert.equal(machine(helperRaw),manifest.helperMachine);
  assert.equal(machine(shippingRaw),manifest.shippingMachine);
  assert.equal(machine(testRaw),manifest.testMachine);
  for (const [binary,proof] of [[shippingRaw,shippingProof],[testRaw,testProof]] as const) {
    assert(Number.isSafeInteger(proof.embeddedOffset));
    assert(proof.embeddedOffset >= 0 && proof.embeddedOffset+helperRaw.length <= binary.length);
    assert(binary.subarray(proof.embeddedOffset,proof.embeddedOffset+helperRaw.length).equals(helperRaw));
  }
}

async function actualBuild(): Promise<ActualBuildProvenance> {
  return actualBuildProvenance(
    await boundedRead(path.join(artifact,'core-test-buildinfo.txt')),
    await boundedRead(path.join(artifact,'core-shipping-buildinfo.txt')),
    await boundedRead(path.join(artifact,'core-test-race-symbols.txt')),
  );
}

async function main(): Promise<void> {
  await mkdir(root,{recursive:false,mode:0o700});
  const compile = await readJson<CompileManifest>(compilePath);
  const build = await actualBuild();
  const source = compileManifestJoin(compile,tuple,build);
  assert.equal(source.coreSHA,coreSHA);
  await verifyGitIdentity(source);
  await actualBinaryFacts(compile.binaries,compile.testEmbeddedProof,compile.shippingEmbeddedProof);

  await persist('scope.json',{
    ...tuple,
    phase:'package_noshow',
    admission:'one-fresh-disposable-package-noshow',
    scenario:'package_noshow',
    coreRefInput:source.coreRefInput,
    coreRefExplicit:source.coreRefExplicit,
    firstAttemptOnly:true,
    wholeJobFreshProfile:true,
    packageRemovalClaim:false,
    globalDrainClaim:false,
    retainedUntilJobTeardown:true,
    productionTestAdmissionCompatibility:'fresh-owned-account-no-show',
    processTreeContainment:'job_object_before_resume',
    timeoutRetryAdmissible:false,
    ...scope,
  });

  const acquisition: Acquisition = {...tuple,...await acquireOfficialArchive(root,tuple)};
  await persist('acquisition.json',acquisition);
  await runObserver('archive');
  const archive = await readJson<ArchiveFacts>(path.join(root,'archive.json'));
  assert.equal(archive.archive_sha,expectedArchiveSHA);
  assert.equal(archive.full_name,expectedFullName);
  await persist('accepted-pins.json',{
    ...tuple,expectedArchiveSHA,expectedFullName,
    acceptedSeparately:true,archiveIntakeReadOnly:true,phasePromotion:false,
  });

  await runObserver('deploy');
  const deployment = await readJson<ArchiveFacts & {
    outcome: string;
    add_package_operations: number;
    immutable_archive_custody: boolean;
  }>(path.join(root,'deployment.json'));
  const archiveEvidence: ArchiveEvidence = {
    signature:await readJson<SignatureFacts>(path.join(root,'signature.json')),
    intake:await readJson<IntakeFacts>(path.join(root,'intake.json')),
    deploymentSignature:await readJson<SignatureFacts>(path.join(root,'deployment-signature.json')),
    deploymentIntake:await readJson<IntakeFacts>(path.join(root,'deployment-intake.json')),
  };
  await runObserver('package-census');
  const census = await readJson<Census>(path.join(root,'package-census.json'));
  const selectedTests = await collectSelectedTests();
  const observerMachine = machine(await boundedRead(observer,32*1024*1024));
  packageEvidenceJoin({
    tuple,
    expectedArchiveSHA,
    expectedFullName,
    compile,
    actualBuild:build,
    acquisition,
    archive,
    archiveEvidence,
    deployment,
    census,
    selectedTests,
    observerMachine,
    scope,
  });
  await persist('qualification.json',{
    ...tuple,
    outcome:'scoped_package_noshow_recovery',
    archiveSHA256:expectedArchiveSHA,
    fullName:expectedFullName,
    helperSHA256:compile.binaries.helperSHA256,
    shippingBinarySHA256:compile.binaries.shippingBinarySHA256,
    testBinarySHA256:compile.binaries.testBinarySHA256,
    shippingEmbeddedOffset:compile.shippingEmbeddedProof.embeddedOffset,
    testEmbeddedOffset:compile.testEmbeddedProof.embeddedOffset,
    harnessTreeSHA:source.harnessTreeSHA,
    coreTreeSHA:source.coreTreeSHA,
    coreIsHarnessAncestor:source.coreIsHarnessAncestor,
    sourceFiles:{...source.harnessFiles,testSource:source.coreFiles.testSource},
    buildProvenance:compile.buildProvenance,
    selectedTestPattern:packageTestPattern,
    expectedTestRuns:[...selectedTests.process.command],
    selectedTestOutput:['selected-tests.stdout.txt','selected-tests.stderr.txt'],
    packageOrProfileRetainedUntilJobTeardown:true,
    packageRemovalClaim:false,
    globalDrainClaim:false,
    ...scope,
  });
}

try {
  await main();
} catch (error) {
  await persist('failure.json',{
    ...tuple,
    error:String(error),
    retainedUntilJobTeardown:true,
    packageRemovalClaim:false,
    globalDrainClaim:false,
    noRetryAfterPossibleNativeEffects:true,
    timeoutOrUnknownEffectsAreNotReplayable:true,
    ...scope,
  }).catch(() => undefined);
  throw error;
}
