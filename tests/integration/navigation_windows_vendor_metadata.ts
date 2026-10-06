// Disposable Windows CI: metadata by default; vendor URI effects require a separate explicit mode.
import { spawnSync } from 'node:child_process';
import { randomUUID, createHash } from 'node:crypto';
import { mkdirSync, copyFileSync, realpathSync, writeFileSync, readFileSync, statSync, createReadStream } from 'node:fs';
import { open } from 'node:fs/promises';
import { join } from 'node:path';

type Json = Record<string, unknown>;
const sourceURL = 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-arm64.msix';
const maxPackage = 1024 * 1024 * 1024;
const evidence: Json = { status: 'failed', scope: 'official Windows ARM64 package metadata/signature only',
  installed: false, launchAttempted: false, showAttempts: 0, inputAttempted: false,
  nativeCallbackQualified: false, navigationQualified: false, sourceSHA: process.env.NAVIGATION_SOURCE_SHA ?? null,
  imageVersion: process.env.ImageVersion ?? null, sourceURL, steps: [] };
let root: string | undefined, exitCode = 1;
function execute(phase: string, executable: string, args: string[], timeout: number,
    options: { cwd?: string; expectedRejection?: boolean; allowCollectedFailure?: boolean } = {}) {
  if (!root) throw new Error('owned root absent');
  const env: NodeJS.ProcessEnv = { ...process.env, AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E: '1',
    NAVIGATION_MANIFEST_NONCE: typeof evidence.nonce === 'string' ? evidence.nonce : '' };
  if (!phase.startsWith('fixture-artifact')) { delete env.GH_TOKEN; delete env.GITHUB_TOKEN; }
  if (phase.startsWith('manifest')) for (const key of Object.keys(env)) {
    if (key.toLowerCase() === 'psmodulepath') delete env[key];
  }
  const started = performance.now();
  const result = spawnSync(executable, args, { cwd: options.cwd ?? root, encoding: 'utf8', timeout, maxBuffer: 65536,
    env, windowsHide: true });
  const step = { phase, pid: result.pid, status: result.status, signal: result.signal,
    stdout: result.stdout ?? '', stderr: result.stderr ?? '', error: result.error?.message ?? null,
    elapsedMs: performance.now() - started, expectedRejection: options.expectedRejection === true };
  (evidence.steps as unknown[]).push(step);
  if (step.error || step.signal || step.status === null
      || (!options.allowCollectedFailure && (options.expectedRejection ? step.status === 0 : step.status !== 0))) {
    throw new Error(`${phase} failed; inspect bounded step evidence`);
  }
  return step;
}
const vendorIdentity = { name: 'OpenAI.Codex', publisher: 'CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B',
  familyName: 'OpenAI.Codex_2p2nqsd0c76g0', fullName: 'OpenAI.Codex_26.930.7945.0_arm64__2p2nqsd0c76g0',
  version: '26.930.7945.0', architecture: 12 };
async function vendorURI(binary: string, metadata: Json): Promise<void> {
  if (!root || process.env.NAVIGATION_WINDOWS_VENDOR_NATIVE_TEST !== '1'
      || process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications'
      || evidence.signatureVerified !== true
      || Object.entries(vendorIdentity).some(([key, value]) => metadata[key] !== value)) {
    throw new Error('explicit pinned vendor URI TEST authority required');
  }
  const ownedRoot = root, nonce = evidence.nonce as string;
  evidence.scope = 'current-user pinned package and targeted synthetic URI handoff only';
  evidence.targetConfirmed = false; evidence.toastCallbackQualified = false;
  evidence.installed = null; evidence.installModeAttempted = false; evidence.installAttempted = false;
  evidence.handoffAccepted = false; evidence.removed = false; evidence.cleanupVerified = false;
  evidence.installOutcomeUnknown = false; evidence.launchOutcomeUnknown = false;
  evidence.allEffectActorsCollected = true; evidence.processQuiescenceQualified = false;
  let installKnown = false, effectsKnown = true, actorsCollected = true, primaryFailure: unknown;
  const raw = (name: string): string => {
    const path = join(ownedRoot, name);
    if (realpathSync(path) !== path || statSync(path).size > 16384) throw new Error(`invalid bounded vendor report ${name}`);
    const text = readFileSync(path, 'utf8');
    (evidence.vendorReportSHA256 as Json)[name] = createHash('sha256').update(text).digest('hex');
    return text;
  };
  evidence.vendorReportSHA256 = {};
  const report = (name: string, step: ReturnType<typeof execute>): Json => {
    const value = JSON.parse(raw(name)) as Json;
    if (value.pid !== step.pid || value.nonce !== nonce || value.familyName !== vendorIdentity.familyName
        || value.fullName !== vendorIdentity.fullName || value.showAttempts !== 0
        || value.toastCallbackQualified !== false || value.targetConfirmed !== false) throw new Error(`uncorrelated vendor report ${name}`);
    (evidence.vendorReports as Json)[name] = value; return value;
  };
  evidence.vendorReports = {};
  const run = (mode: string): ReturnType<typeof execute> => {
    try { return execute(mode, binary, [mode, ownedRoot, nonce], mode.startsWith('vendor-state-') ? 15000 : 165000,
      { allowCollectedFailure: true }); }
    catch (error: unknown) { actorsCollected = false; evidence.allEffectActorsCollected = false; throw error; }
  };
  const failure = (mode: string, step: ReturnType<typeof execute>): Json | null => {
    try {
      const value = report(`vendor-failure-${mode}.json`, step);
      return typeof value.effectAPIEntered === 'boolean' && typeof value.outcomeUnknown === 'boolean'
        && value.retryAllowed === false ? value : null;
    } catch { return null; } // Missing terminal evidence is unknown, even if the process returned nonzero.
  };
  const custody = `TEST vendor custody ${nonce}\nsigntool-pa-all-success\n${evidence.packageSHA256}\n${evidence.binarySHA256}\n`;
  const packagePath = join(ownedRoot, 'client.msix');
  let intent = '';
  try {
    if (await hash(packagePath) !== evidence.packageSHA256 || await hash(binary) !== evidence.binarySHA256) {
      throw new Error('vendor inputs changed before custody');
    }
    evidence.packageSHA256BeforeEffects = evidence.packageSHA256; evidence.binarySHA256BeforeEffects = evidence.binarySHA256;
    writeFileSync(join(ownedRoot, 'vendor-custody.proof'), custody, { flag: 'wx', flush: true });
    const before = run('vendor-state-before');
    const beforeReport = report('vendor-before.json', before);
    evidence.vendorFamilyCountBeforeEffects = beforeReport.installedCount;
    if (before.status === 0 && beforeReport.readOnly === true && Number.isInteger(beforeReport.installedCount)
        && (beforeReport.installedCount as number) >= 0) evidence.installed = beforeReport.installedCount !== 0;
    if (before.status !== 0 || beforeReport.readOnly !== true || beforeReport.installedCount !== 0 || beforeReport.exactFullName !== false) {
      throw new Error('preexisting or unproved vendor family; no installation');
    }
    const absent = raw('vendor-absent.proof');
    const parts = absent.split('\n');
    if (parts.length !== 3 || parts[0] !== nonce || !/^(?:[0-9a-f]{2}){8,68}$/.test(parts[1] ?? '') || parts[2] !== '') {
      throw new Error('invalid current-user absence proof');
    }
    intent = `${custody}absent-before-install\n${parts[1]}\n`;
    evidence.installModeAttempted = true; evidence.installAttempted = null;
    evidence.installed = null; evidence.installOutcomeUnknown = true; effectsKnown = false;
    const add = run('vendor-install');
    if (add.status !== 0) {
      const rejected = failure('vendor-install', add);
      if (rejected) evidence.installAttempted = rejected.effectAPIEntered;
      if (rejected?.effectAPIEntered === false && rejected.outcomeUnknown === false) {
        effectsKnown = true; evidence.installOutcomeUnknown = false;
      }
      throw new Error('vendor installation failed');
    }
    const added = report('vendor-install-result.json', add);
    if (added.operationCompleted === true) evidence.installAttempted = true;
    if (added.operationCompleted !== true || added.extendedError !== 0
        || raw('vendor-install-intent.proof') !== intent || raw('vendor-install-completed.proof') !== intent) {
      throw new Error('vendor installation success unproved');
    }
    installKnown = true; effectsKnown = true; evidence.installCompleted = true; evidence.installOutcomeUnknown = false;
    const after = run('vendor-state-after');
    const afterReport = report('vendor-after.json', after);
    if (after.status !== 0 || afterReport.readOnly !== true || afterReport.installedCount !== 1 || afterReport.exactFullName !== true) {
      throw new Error('fresh installed vendor identity unproved');
    }
    evidence.installed = true;
    const query = run('vendor-query-thread');
    const supported = report('vendor-query-thread.json', query);
    if (query.status !== 0 || supported.readOnly !== true || supported.uriSupport !== 0) throw new Error('targeted synthetic URI unavailable');
    evidence.launchModeAttempted = true; evidence.launchAttempted = null; evidence.handoffAccepted = null;
    evidence.installed = null; evidence.launchOutcomeUnknown = true; effectsKnown = false;
    const launch = run('vendor-launch-thread');
    if (launch.status !== 0 && launch.status !== 3) {
      const rejected = failure('vendor-launch-thread', launch);
      if (rejected) evidence.launchAttempted = rejected.effectAPIEntered;
      if (rejected?.effectAPIEntered === false && rejected.outcomeUnknown === false) {
        effectsKnown = true; evidence.handoffAccepted = false; evidence.launchOutcomeUnknown = false;
      }
      throw new Error('vendor launch failed');
    }
    const prelaunch = report('vendor-query-thread-prelaunch.json', launch);
    if (prelaunch.readOnly !== true || prelaunch.uriSupport !== 0) throw new Error('collected launch pre-query unproved');
    const launched = report('vendor-launch.json', launch);
    if (launched.launchReturned !== true || launched.launchAttempts !== 1 || launched.retryAllowed !== false
        || typeof launched.handoffAccepted !== 'boolean' || raw('vendor-launch-intent.proof') !== `${intent}thread\n`
        || (launch.status === 0) !== launched.handoffAccepted) throw new Error('vendor handoff terminal result unproved');
    effectsKnown = true; evidence.launchAttempted = true; evidence.launchAttempts = 1;
    evidence.launchOutcomeUnknown = false; evidence.handoffAccepted = launched.handoffAccepted;
    if (!launched.handoffAccepted) throw new Error('targeted synthetic URI handoff declined');
  } catch (error: unknown) { primaryFailure = error; }
  finally {
    if (installKnown && effectsKnown && actorsCollected) {
      evidence.cleanupAttempted = true; evidence.installed = null; evidence.removed = null; evidence.removeOutcomeUnknown = true;
      try {
        const remove = run('vendor-remove');
        const removed = report('vendor-remove-result.json', remove);
        const absent = report('vendor-removed.json', remove);
        if (remove.status !== 0 || removed.operationCompleted !== true || removed.extendedError !== 0
            || absent.currentUserFamilyAbsent !== true || raw('vendor-remove-intent.proof') !== intent) {
          throw new Error('vendor removal terminal absence unproved');
        }
        evidence.installed = false; evidence.removed = true; evidence.cleanupVerified = true; evidence.removeOutcomeUnknown = false;
      } catch (error: unknown) { evidence.cleanupError = error instanceof Error ? error.message : String(error); primaryFailure ??= error; }
    } else if (installKnown || !effectsKnown || !actorsCollected) {
      evidence.cleanupSkippedOutcomeUnknown = true; primaryFailure ??= new Error('vendor effects or actor collection unknown; no guessed removal');
    }
    for (const [key, path] of [['packageSHA256AfterEffects', packagePath], ['binarySHA256AfterEffects', binary]] as const) {
      try { evidence[key] = await hash(path); if (evidence[key] !== (key.startsWith('package') ? evidence.packageSHA256 : evidence.binarySHA256)) throw new Error('vendor custody input changed'); }
      catch (error: unknown) {
        evidence.inputIntegrityErrors ??= [];
        (evidence.inputIntegrityErrors as unknown[]).push({ field: key, error: error instanceof Error ? error.message : String(error) });
        primaryFailure ??= error;
      }
    }
  }
  if (primaryFailure) throw primaryFailure;
  if (!evidence.cleanupVerified || !evidence.handoffAccepted) throw new Error('vendor URI checkpoint incomplete');
  evidence.status = 'vendor_uri_handoff_accepted_and_package_removed'; exitCode = 0;
}
async function hash(path: string): Promise<string> {
  const digest = createHash('sha256'); let bytes = 0;
  for await (const chunk of createReadStream(path, { highWaterMark: 65536 })) {
    if (!Buffer.isBuffer(chunk) || (bytes += chunk.length) > maxPackage) throw new Error('hash input outside bound');
    digest.update(chunk);
  }
  return digest.digest('hex');
}
async function download(path: string): Promise<string> {
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 120000);
  let current = new URL(sourceURL);
  const redirects: string[] = [];
  const file = await open(path, 'wx', 0o600);
  try {
    for (let attempt = 0; attempt < 6; attempt++) {
      if (current.protocol !== 'https:' || current.username || current.password) throw new Error('invalid source redirect');
      const response = await fetch(current, { redirect: 'manual', signal: controller.signal });
      redirects.push(current.origin + current.pathname); evidence.redirects = redirects;
      if ([301, 302, 303, 307, 308].includes(response.status)) {
        const location = response.headers.get('location'); await response.body?.cancel();
        if (!location) throw new Error('redirect location absent'); current = new URL(location, current); continue;
      }
      if (response.status !== 200 || !response.body) { await response.body?.cancel(); throw new Error('official download unavailable'); }
      const declared = response.headers.get('content-length');
      if (declared && (!/^\d+$/.test(declared) || Number(declared) > maxPackage)) {
        await response.body.cancel(); throw new Error('declared package size outside bound');
      }
      const digest = createHash('sha256'), reader = response.body.getReader(); let bytes = 0;
      try {
        while (true) {
          const chunk = await reader.read(); if (chunk.done) break;
          bytes += chunk.value.length;
          if (bytes > maxPackage) throw new Error('package download exceeded bound');
          digest.update(chunk.value); await file.writeFile(chunk.value);
        }
      } finally { await reader.cancel(); }
      if (!bytes || declared && bytes !== Number(declared)) throw new Error('package download incomplete');
      await file.sync(); evidence.packageBytes = bytes; evidence.etag = response.headers.get('etag');
      return digest.digest('hex');
    }
    throw new Error('redirect budget exhausted');
  } finally { controller.abort(); clearTimeout(timer); await file.close(); }
}
const inspectManifest = String.raw`
$ErrorActionPreference='Stop'
$clock=[System.Diagnostics.Stopwatch]::StartNew()
function Checkpoint([string]$phase) {
  [Console]::Error.WriteLine('manifest-phase='+$phase+' elapsedMs='+$clock.ElapsedMilliseconds)
  [Console]::Error.Flush()
}
Checkpoint 'entry'
[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false)
$settings=[System.Xml.XmlReaderSettings]::new()
$settings.DtdProcessing=[System.Xml.DtdProcessing]::Prohibit
$settings.XmlResolver=$null
$settings.MaxCharactersInDocument=2097152
Checkpoint 'path_start'
$path=Join-Path (Get-Location) 'vendor-manifest.xml'
if ((Get-Item -LiteralPath $path).Length -gt 2097152) { throw 'manifest too large' }
$reader=[System.Xml.XmlReader]::Create($path,$settings)
Checkpoint 'xml_load_start'
try { $doc=[System.Xml.XmlDocument]::new(); $doc.XmlResolver=$null; $doc.Load($reader) } finally { $reader.Dispose() }
Checkpoint 'xml_loaded'
$apps=@($doc.SelectNodes("/*[local-name()='Package']/*[local-name()='Applications']/*[local-name()='Application']"))
$dependencies=@($doc.SelectNodes("/*[local-name()='Package']/*[local-name()='Dependencies']/*"))
if ($apps.Count -gt 64 -or $dependencies.Count -gt 64) { throw 'manifest record bound exceeded' }
function Bounded([string]$value) { if ($value.Length -gt 512 -or $value -match '[\x00-\x1f\x7f]') { throw 'invalid manifest string' }; return $value }
$projection=@($apps | ForEach-Object {
  $protocols=@($_.SelectNodes("./*[local-name()='Extensions']/*[local-name()='Extension'][@Category='windows.protocol']/*[local-name()='Protocol']"))
  if ($protocols.Count -gt 16) { throw 'protocol count exceeded' }
  [ordered]@{id=(Bounded ($_.GetAttribute('Id'))); executable=(Bounded ($_.GetAttribute('Executable')));
    entryPoint=(Bounded ($_.GetAttribute('EntryPoint'))); protocols=@($protocols | ForEach-Object { Bounded ($_.GetAttribute('Name')) })}
})
$deps=@($dependencies | ForEach-Object { [ordered]@{kind=$_.LocalName; name=(Bounded ($_.GetAttribute('Name')));
  publisher=(Bounded ($_.GetAttribute('Publisher'))); minVersion=(Bounded ($_.GetAttribute('MinVersion')))} })
$result=[ordered]@{pid=$PID; nonce=$env:NAVIGATION_MANIFEST_NONCE; name=$doc.DocumentElement.Identity.Name; publisher=$doc.DocumentElement.Identity.Publisher;
  version=$doc.DocumentElement.Identity.Version; architecture=$doc.DocumentElement.Identity.ProcessorArchitecture;
  applications=$projection; dependencies=$deps; readOnly=$true; installed=$false; launchAttempted=$false}
Checkpoint 'serialize_start'
$json=$result | ConvertTo-Json -Depth 8 -Compress
if ([System.Text.Encoding]::UTF8.GetByteCount($json) -gt 16384) { throw 'manifest projection too large' }
Checkpoint 'json_ready'
$json
`;
function manifestProjection(step: ReturnType<typeof execute>, metadata: Json): Json {
  const manifest = JSON.parse(step.stdout.replace(/^\uFEFF/, '').trim()) as Json;
  if (manifest.pid !== step.pid || manifest.nonce !== evidence.nonce || manifest.readOnly !== true
      || manifest.installed !== false || manifest.launchAttempted !== false
      || manifest.name !== metadata.name || manifest.publisher !== metadata.publisher || manifest.version !== metadata.version
      || manifest.architecture !== 'arm64' || !Array.isArray(manifest.applications) || manifest.applications.length === 0
      || !manifest.applications.some(app => app && Array.isArray(app.protocols) && app.protocols.includes('codex'))) {
    throw new Error('actual vendor manifest does not establish expected identity/protocol');
  }
  return manifest;
}
async function manifestFixture(powershell: string): Promise<void> {
  if (!root || process.argv.length !== 3 || process.env.NAVIGATION_WINDOWS_MANIFEST_DIAGNOSTIC !== '1'
      || process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications') throw new Error('explicit fixed-artifact diagnostic required');
  evidence.scope = 'retained failed-phase manifest fixture only';
  evidence.packageDownloadAttempted = false; evidence.signatureReverified = false; evidence.sdkProbeReplayed = false;
  const artifactID = 11431669982;
  const archiveSHA = 'bd44b0946079032d4855bfd7abbc649066243e120c7303e5039caf11c333a476';
  const sourceSHA = '052b08b52113872466d10f3ad205845a19f76bcf';
  const api = `repos/777genius/agent-notifications/actions/artifacts/${artifactID}`;
  const descriptor = JSON.parse(execute('fixture-artifact-metadata', 'gh', ['api', api], 30000).stdout) as Json;
  const run = descriptor.workflow_run as Json | undefined;
  if (descriptor.id !== artifactID || descriptor.expired !== false || descriptor.size_in_bytes !== 232904
      || descriptor.digest !== `sha256:${archiveSHA}` || descriptor.name !== 'navigation-windows-vendor-metadata-37506435343-1'
      || run?.id !== 37506435343 || run.head_sha !== sourceSHA) throw new Error('fixed artifact provenance mismatch');
  evidence.fixtureArtifact = { id: artifactID, runID: run.id, sourceSHA, archiveSHA256: archiveSHA, archiveBytes: 232904 };
  const downloaded = spawnSync('gh', ['api', `${api}/zip`], { cwd: root, timeout: 30000, maxBuffer: 1048576, windowsHide: true });
  const bytes = downloaded.stdout ?? Buffer.alloc(0);
  writeFileSync(join(root, 'prior-artifact.zip'), bytes, { flag: 'wx' });
  (evidence.steps as unknown[]).push({ phase: 'fixture-artifact-download', pid: downloaded.pid,
    status: downloaded.status, signal: downloaded.signal, error: downloaded.error?.message ?? null,
    stderr: downloaded.stderr?.toString('utf8') ?? '', stdoutBytes: bytes.length,
    stdoutSHA256: createHash('sha256').update(bytes).digest('hex') });
  if (downloaded.error || downloaded.signal || downloaded.status !== 0 || bytes.length !== 232904
      || createHash('sha256').update(bytes).digest('hex') !== archiveSHA) throw new Error('fixed artifact download incomplete');
  const pwsh = realpathSync(process.env.NAVIGATION_DIAGNOSTIC_PWSH ?? '');
  evidence.fixtureExtractorSHA256 = await hash(pwsh);
  // Exact five-member digest-bound ZIP. Extract only three inert inputs, never its executable.
  execute('fixture-extract', pwsh, ['-NoProfile', '-NonInteractive', '-Command', String.raw`
$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip=[System.IO.Compression.ZipFile]::OpenRead((Join-Path (Get-Location) 'prior-artifact.zip'))
try {
  $expected=@{'.owned-test-root'=61;'navigation-native-probe.exe'=591872;'vendor-evidence.json'=4804;'package-metadata.json'=358;'vendor-manifest.xml'=8082}
  if($zip.Entries.Count -ne 5){throw 'unexpected ZIP members'}
  $seen=@{}
  foreach($entry in $zip.Entries){
    if(!$expected.ContainsKey($entry.FullName) -or $seen.ContainsKey($entry.FullName) -or $entry.Length -ne $expected[$entry.FullName]){throw 'ZIP member mismatch'}
    $seen[$entry.FullName]=$true
    if($entry.FullName -notin @('vendor-evidence.json','package-metadata.json','vendor-manifest.xml')){continue}
    $input=$entry.Open();$output=[IO.MemoryStream]::new()
    try{$input.CopyTo($output);if($output.Length -ne $entry.Length){throw 'ZIP output incomplete'}
      $name=if($entry.FullName -eq 'vendor-manifest.xml'){$entry.FullName}else{'prior-'+$entry.FullName}
      $target=Join-Path (Get-Location) $name
      $file=[IO.File]::Open($target,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
      try{$value=$output.ToArray();$file.Write($value,0,$value.Length);$file.Flush($true)}finally{$file.Dispose()}
    }finally{$input.Dispose();$output.Dispose()}
  }
}finally{$zip.Dispose()}
`], 15000);
  const pins = { 'vendor-manifest.xml': '6324900851e14fadc1e1168368d5203cbe0c6b222e472491977899016a045e6c',
    'prior-vendor-evidence.json': '9c69e18987233b831c0af395744ef2dd3148a002d7b40fc3ca1001b8929a0759',
    'prior-package-metadata.json': 'ad74c4d19ce19952578b20b19da54b8bcda99c550e82483be411948c8dd8ca11' };
  for (const [name, expected] of Object.entries(pins)) if (await hash(join(root, name)) !== expected) throw new Error('retained fixture hash mismatch');
  evidence.fixtureInputSHA256 = pins;
  const prior = JSON.parse(readFileSync(join(root, 'prior-vendor-evidence.json'), 'utf8')) as Json;
  const metadata = JSON.parse(readFileSync(join(root, 'prior-package-metadata.json'), 'utf8')) as Json;
  if (prior.sourceSHA !== sourceSHA || prior.status !== 'failed' || prior.signatureVerified !== true
      || metadata.name !== 'OpenAI.Codex' || metadata.familyName !== 'OpenAI.Codex_2p2nqsd0c76g0'
      || metadata.version !== '26.930.7945.0') throw new Error('prior evidence boundary mismatch');
  evidence.retainedPriorSignatureVerified = true; evidence.retainedPriorSDKMetadata = metadata;
  evidence.manifest = manifestProjection(execute('manifest-fixture', powershell,
    ['-NoProfile', '-NonInteractive', '-Command', inspectManifest], 15000), metadata);
  const apps = (evidence.manifest as Json).applications as Json[];
  if (apps.length !== 2 || apps[0]?.id !== 'App' || apps[1]?.id !== 'CodexCoreCommandRunner') throw new Error('known vendor application projection mismatch');
  const negative = join(root, 'dtd-negative-TEST'); mkdirSync(negative);
  const dtd = '<?xml version="1.0"?><!DOCTYPE Package [<!ENTITY protocol "codex">]><Package><Identity Name="OpenAI.Codex" Publisher="CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B" Version="26.930.7945.0" ProcessorArchitecture="arm64"/><Applications><Application Id="App"><Extensions><Extension Category="windows.protocol"><Protocol Name="&protocol;"/></Extension></Extensions></Application></Applications></Package>';
  writeFileSync(join(negative, 'vendor-manifest.xml'), dtd, { flag: 'wx' });
  evidence.dtdNegativeInputSHA256 = createHash('sha256').update(dtd).digest('hex');
  const rejected = execute('manifest-DTD-negative', powershell, ['-NoProfile', '-NonInteractive', '-Command', inspectManifest], 15000,
    { cwd: negative, expectedRejection: true });
  if (!rejected.stderr.includes('manifest-phase=xml_load_start') || rejected.stderr.includes('manifest-phase=xml_loaded')) {
    throw new Error('DTD control did not reach bounded XML load rejection');
  }
  for (const [name, expected] of Object.entries(pins)) if (await hash(join(root, name)) !== expected) throw new Error('retained fixture changed across diagnostic');
  evidence.fixtureInputsUnchanged = true; evidence.dtdNegativeControlPassed = true;
  evidence.status = 'manifest_fixture_projected'; exitCode = 0;
}
async function main(): Promise<void> {
  if (process.platform !== 'win32' || process.env.CI !== 'true' || process.env.GITHUB_ACTIONS !== 'true'
      || process.env.NAVIGATION_WINDOWS_VENDOR_METADATA_E2E !== '1' || Number(process.versions.node.split('.')[0]) !== 24) {
    throw new Error('requires explicit disposable Windows CI metadata opt-in and Node24');
  }
  const args = process.argv.slice(2), fixture = args[0] === '--manifest-fixture-only';
  const native = args[0] === '--vendor-uri-native';
  if (fixture ? args.length !== 1 : native ? args.length !== 3
      || process.env.NAVIGATION_WINDOWS_VENDOR_NATIVE_TEST !== '1'
      : args.length !== 2 && !(args.length === 3 && args[2] === '--syntax-only')) {
    throw new Error('invalid exact metadata/fixture/vendor URI arguments');
  }
  if (!fixture && (args[native ? 1 : 0]?.startsWith('--') || args[native ? 2 : 1]?.startsWith('--'))) {
    throw new Error('binary and signature-tool paths required before any inspection/effect');
  }
  const nonce = randomUUID();
  root = join(realpathSync(process.env.RUNNER_TEMP ?? ''), `navigation-windows-test-${nonce}`);
  mkdirSync(root, { recursive: false });
  writeFileSync(join(root, '.owned-test-root'), `TEST navigation Windows ${nonce}\n`, { flag: 'wx' });
  evidence.nonce = nonce;
  const powershell = join(process.env.SystemRoot ?? '', 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
  if (fixture) { await manifestFixture(powershell); return; }
  const binary = join(root, 'navigation-native-probe.exe'); copyFileSync(realpathSync(args[native ? 1 : 0] ?? ''), binary);
  const signtool = realpathSync(args[native ? 2 : 1] ?? '');
  evidence.nonce = nonce; evidence.binarySHA256 = await hash(binary);
  evidence.signtoolSHA256 = await hash(signtool);
  if (!native) {
    const encoded = Buffer.from(inspectManifest, 'utf8').toString('base64');
    execute('manifest-syntax', powershell, ['-NoProfile', '-NonInteractive', '-Command',
    `$s=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('${encoded}'));$tokens=$null;$errors=$null;`
    + `[System.Management.Automation.Language.Parser]::ParseInput($s,[ref]$tokens,[ref]$errors)|Out-Null;`
    + `if($errors.Count){throw 'manifest operator syntax rejected'}`], 15000);
    if (args[2] === '--syntax-only') { evidence.status = 'syntax_checked'; exitCode = 0; return; }
  }
  const packagePath = join(root, 'client.msix');
  evidence.packageSHA256 = await download(packagePath);
  execute('signature', signtool, ['verify', '/pa', '/all', '/v', packagePath], 60000);
  evidence.signatureVerified = true;
  const metadataStep = execute('package-metadata', binary, ['package-metadata', root, nonce], 15000);
  const metadataPath = join(root, 'package-metadata.json');
  if (statSync(metadataPath).size > 16384) throw new Error('metadata report too large');
  const metadata = JSON.parse(readFileSync(metadataPath, 'utf8')) as Json; evidence.metadata = metadata;
  if (metadata.pid !== metadataStep.pid || metadata.nonce !== nonce || metadata.readOnly !== true
      || metadata.installed !== false || metadata.launchAttempted !== false || metadata.showAttempts !== 0
      || metadata.name !== 'OpenAI.Codex' || metadata.architecture !== 12
      || typeof metadata.familyName !== 'string' || !metadata.familyName.startsWith('OpenAI.Codex_')
      || typeof metadata.fullName !== 'string' || !metadata.fullName.startsWith('OpenAI.Codex_')) throw new Error('package identity evidence invalid');
  if (native) { await vendorURI(binary, metadata); return; }
  const manifestStep = execute('manifest', powershell, ['-NoProfile', '-NonInteractive', '-Command', inspectManifest], 15000);
  evidence.manifest = manifestProjection(manifestStep, metadata);
  evidence.packageSHA256AfterInspection = await hash(packagePath);
  if (evidence.packageSHA256AfterInspection !== evidence.packageSHA256) throw new Error('package changed across inspection');
  evidence.status = 'metadata_inspected'; exitCode = 0;
}
try { await main(); }
catch (error: unknown) { evidence.error = error instanceof Error ? error.message : String(error); }
finally {
  if (root) {
    writeFileSync(join(root, 'vendor-evidence.json'), JSON.stringify(evidence, null, 2) + '\n', { flag: 'wx' });
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, `evidence_root=${root}\n`, { flag: 'a' });
  }
  console.log(JSON.stringify(evidence)); process.exitCode = exitCode;
}
