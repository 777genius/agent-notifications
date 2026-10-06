// Disposable Windows CI: official package inspection only, no install or URI launch.
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
    options: { cwd?: string; expectedRejection?: boolean } = {}) {
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
      || (options.expectedRejection ? step.status === 0 : step.status !== 0)) {
    throw new Error(`${phase} failed; inspect bounded step evidence`);
  }
  return step;
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
  const nonce = randomUUID();
  root = join(realpathSync(process.env.RUNNER_TEMP ?? ''), `navigation-windows-test-${nonce}`);
  mkdirSync(root, { recursive: false });
  writeFileSync(join(root, '.owned-test-root'), `TEST navigation Windows ${nonce}\n`, { flag: 'wx' });
  evidence.nonce = nonce;
  const powershell = join(process.env.SystemRoot ?? '', 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
  if (process.argv[2] === '--manifest-fixture-only') { await manifestFixture(powershell); return; }
  const binary = join(root, 'navigation-native-probe.exe'); copyFileSync(realpathSync(process.argv[2] ?? ''), binary);
  const signtool = realpathSync(process.argv[3] ?? '');
  evidence.nonce = nonce; evidence.binarySHA256 = await hash(binary);
  evidence.signtoolSHA256 = await hash(signtool);
  const encoded = Buffer.from(inspectManifest, 'utf8').toString('base64');
  execute('manifest-syntax', powershell, ['-NoProfile', '-NonInteractive', '-Command',
    `$s=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('${encoded}'));$tokens=$null;$errors=$null;`
    + `[System.Management.Automation.Language.Parser]::ParseInput($s,[ref]$tokens,[ref]$errors)|Out-Null;`
    + `if($errors.Count){throw 'manifest operator syntax rejected'}`], 15000);
  if (process.argv[4] === '--syntax-only') { evidence.status = 'syntax_checked'; exitCode = 0; return; }
  if (process.argv[4] !== undefined) throw new Error('unknown metadata mode');
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
