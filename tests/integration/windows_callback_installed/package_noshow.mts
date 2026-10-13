import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomBytes, createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, open, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { sha, type Tuple } from './evidence.mts';
import { acquireOfficialArchive } from './strict_archive.mts';
import {
  compileManifestJoin,
  packageEvidenceJoin,
  packageTestPattern,
  type ArchiveFacts,
  type BinaryFacts,
  type CompileManifest,
  type PackageScopeFacts,
  type SelectedTestResult,
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

const env = process.env;
assert.equal(env.GITHUB_ACTIONS,'true');
assert.equal(env.GITHUB_EVENT_NAME,'workflow_dispatch');
assert.equal(env.GITHUB_RUN_ATTEMPT,'1');
assert.equal(env.GITHUB_REPOSITORY,'777genius/agent-notifications');
assert.equal(process.platform,'win32');
assert.equal(env.TEST_INSTALLED_ADMISSION,'one-fresh-disposable-package-noshow');
assert.equal(env.TEST_INSTALLED_PHASE,'package_noshow');
assert.equal(env.TEST_INSTALLED_SCENARIO,'package_noshow');

const harnessSHA = env.GITHUB_SHA!;
const coreSHA = env.TEST_CORE_SHA!;
assert(harnessSHA.match(/^[a-f0-9]{40}$/));
assert(coreSHA.match(/^[a-f0-9]{40}$/));
const tuple: Tuple = {source:harnessSHA,nonce:randomBytes(16).toString('hex')};
const root = path.join(env.RUNNER_TEMP!,'TEST-installed-'+tuple.nonce);
const artifact = path.resolve(env.TEST_INSTALLED_ARTIFACT!);
const observer = path.resolve(env.TEST_INSTALLED_OBSERVER!);
const coreSource = path.resolve(env.TEST_CORE_SOURCE!);
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
    const stat = await h.stat();
    assert(stat.isFile() && stat.nlink === 1 && stat.size > 0 && stat.size <= limit);
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

function machine(bytes: Buffer): number {
  assert(bytes.length >= 256 && bytes.subarray(0,2).toString() === 'MZ');
  const p = bytes.readUInt32LE(60);
  assert(p+6 <= bytes.length && bytes.subarray(p,p+4).equals(Buffer.from([80,69,0,0])));
  return bytes.readUInt16LE(p+4);
}

async function readJson<T>(file: string): Promise<T> {
  return JSON.parse((await boundedRead(file)).toString()) as T;
}

async function runObserver(mode: 'archive'|'deploy'|'package-census'): Promise<void> {
  const id = (++count).toString().padStart(2,'0');
  await persist(id+'-observer-'+mode+'.intent.json',{...tuple,observer,mode,timeout:140000});
  await new Promise<void>((resolve,reject) => {
    const child = spawn(observer,[mode,root,tuple.nonce,harnessSHA],{
      env:childEnv,stdio:['ignore','pipe','pipe'],windowsHide:true,
    });
    let bytes = 0, out = '', error = '', fault: Error | undefined;
    const stop = (reason: Error): void => {
      fault ??= reason;
      child.kill();
    };
    const timer = setTimeout(() => stop(new Error('observer deadline; deployment effect/collection unknown')),140000);
    child.stdout.on('data',(chunk: Buffer) => {
      bytes += chunk.length;
      if (bytes > 65536) stop(new Error('observer output bound'));
      else out += chunk.toString('utf8');
    });
    child.stderr.on('data',(chunk: Buffer) => {
      bytes += chunk.length;
      if (bytes > 65536) stop(new Error('observer output bound'));
      else error += chunk.toString('utf8');
    });
    child.on('error',(reason: Error) => {
      fault ??= reason;
    });
    child.once('close',(code,signal) => {
      clearTimeout(timer);
      const unknown = !!fault || code === null || child.pid === undefined;
      void persist(id+'-observer-'+mode+'.collected.json',{
        ...tuple,mode,collected:child.pid !== undefined,pid:child.pid,code,signal,out,error,
        error_message:fault?.message ?? null,unknown,
      }).then(() => {
        if (fault || code !== 0 || signal) reject(fault ?? new Error(`observer ${mode} failed code=${code} signal=${signal}`));
        else resolve();
      },reject);
    });
  });
}

async function collectSelectedTests(): Promise<SelectedTestResult> {
  const id = (++count).toString().padStart(2,'0');
  await persist(id+'-selected-tests.intent.json',{...tuple,testBinary,args:selectedArgs,deadlineMs:360000});
  return await new Promise<SelectedTestResult>((resolve,reject) => {
    const child = spawn(testBinary,[...selectedArgs],{
      env:childEnv,stdio:['ignore','pipe','pipe'],windowsHide:true,
    });
    let bytes = 0, stdout = '', stderr = '', fault: Error | undefined, timedOut = false;
    const stop = (reason: Error, timeout=false): void => {
      fault ??= reason;
      timedOut ||= timeout;
      child.kill();
    };
    const timer = setTimeout(() => stop(new Error('selected production test deadline'),true),360000);
    const observe = (chunk: Buffer, isStderr: boolean): void => {
      bytes += chunk.length;
      if (bytes > 60000) stop(new Error('selected production test output bound'));
      else if (isStderr) stderr += chunk.toString('utf8');
      else stdout += chunk.toString('utf8');
    };
    child.stdout.on('data',(chunk: Buffer) => observe(chunk,false));
    child.stderr.on('data',(chunk: Buffer) => observe(chunk,true));
    child.on('error',(reason: Error) => {
      fault ??= reason;
    });
    child.once('close',(code,signal) => {
      clearTimeout(timer);
      const result: SelectedTestResult = {
        collected:child.pid !== undefined,
        streamsCollected:true,
        timedOut,
        unknown:!!fault || code === null || child.pid === undefined,
        error:fault?.message ?? null,
        signal,
        exitCode:code,
        deadlineMs:360000,
        args:selectedArgs,
        stdout,
        stderr,
      };
      void (async () => {
        await writeFile(path.join(root,'selected-tests.stdout.txt'),stdout,{flag:'wx'});
        await writeFile(path.join(root,'selected-tests.stderr.txt'),stderr,{flag:'wx'});
        await persist('selected-tests.collected.json',{
          ...result,
          stdout:undefined,
          stderr:undefined,
          stdoutSHA256:sha(Buffer.from(stdout)),
          stderrSHA256:sha(Buffer.from(stderr)),
          stdoutBytes:Buffer.byteLength(stdout),
          stderrBytes:Buffer.byteLength(stderr),
        });
        resolve(result);
      })().catch(reject);
    });
  });
}

async function actualBinaryFacts(manifest: BinaryFacts): Promise<void> {
  const helperRaw = await boundedRead(helper,16*1024*1024);
  const shippingRaw = await boundedRead(shipping,32*1024*1024);
  const testRaw = await boundedRead(testBinary,32*1024*1024);
  assert.equal(sha(helperRaw),manifest.helperSHA256);
  assert.equal(sha(shippingRaw),manifest.shippingBinarySHA256);
  assert.equal(sha(testRaw),manifest.testBinarySHA256);
  assert.equal(machine(helperRaw),manifest.helperMachine);
  assert.equal(machine(shippingRaw),manifest.shippingMachine);
  assert.equal(machine(testRaw),manifest.testMachine);
}

async function main(): Promise<void> {
  await mkdir(root,{recursive:false,mode:0o700});
  const compile = await readJson<CompileManifest>(compilePath);
  const source = compileManifestJoin(compile,tuple);
  assert.equal(source.coreSHA,coreSHA);
  const testSource = path.join(coreSource,'internal','installruntime','windows_generation_native_test.go');
  assert.equal(await hashFile(testSource),source.testSourceSHA256);
  assert.equal(await hashFile(observer),source.observerSHA256);
  assert.equal(await hashFile(process.argv[1]!),source.controllerSHA256);
  await actualBinaryFacts(compile.binaries);

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
  await runObserver('package-census');
  const census = await readJson<Census>(path.join(root,'package-census.json'));
  const selectedTests = await collectSelectedTests();
  const observerMachine = machine(await boundedRead(observer,32*1024*1024));
  packageEvidenceJoin({
    tuple,
    expectedArchiveSHA,
    expectedFullName,
    compile,
    acquisition,
    archive,
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
    observerSHA256:source.observerSHA256,
    controllerSHA256:source.controllerSHA256,
    testSourceSHA256:source.testSourceSHA256,
    selectedTestPattern:packageTestPattern,
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
    ...scope,
  }).catch(() => undefined);
  throw error;
}
