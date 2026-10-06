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
function execute(phase: string, executable: string, args: string[], timeout: number) {
  if (!root) throw new Error('owned root absent');
  const env: NodeJS.ProcessEnv = { ...process.env, AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E: '1' };
  if (phase.startsWith('manifest')) for (const key of Object.keys(env)) {
    if (key.toLowerCase() === 'psmodulepath') delete env[key];
  }
  const result = spawnSync(executable, args, { cwd: root, encoding: 'utf8', timeout, maxBuffer: 65536,
    env, windowsHide: true });
  const step = { phase, pid: result.pid, status: result.status, signal: result.signal,
    stdout: result.stdout ?? '', stderr: result.stderr ?? '', error: result.error?.message ?? null };
  (evidence.steps as unknown[]).push(step);
  if (step.error || step.signal || step.status !== 0) throw new Error(`${phase} failed; inspect bounded step evidence`);
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
[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false)
$settings=[System.Xml.XmlReaderSettings]::new()
$settings.DtdProcessing=[System.Xml.DtdProcessing]::Prohibit
$settings.XmlResolver=$null
$settings.MaxCharactersInDocument=2097152
$path=Join-Path (Get-Location) 'vendor-manifest.xml'
if ((Get-Item -LiteralPath $path).Length -gt 2097152) { throw 'manifest too large' }
$reader=[System.Xml.XmlReader]::Create($path,$settings)
try { $doc=[System.Xml.XmlDocument]::new(); $doc.XmlResolver=$null; $doc.Load($reader) } finally { $reader.Dispose() }
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
$result=[ordered]@{name=$doc.DocumentElement.Identity.Name; publisher=$doc.DocumentElement.Identity.Publisher;
  version=$doc.DocumentElement.Identity.Version; architecture=$doc.DocumentElement.Identity.ProcessorArchitecture;
  applications=$projection; dependencies=$deps; readOnly=$true; installed=$false; launchAttempted=$false}
$json=$result | ConvertTo-Json -Depth 8 -Compress
if ([System.Text.Encoding]::UTF8.GetByteCount($json) -gt 16384) { throw 'manifest projection too large' }
$json
`;
async function main(): Promise<void> {
  if (process.platform !== 'win32' || process.env.CI !== 'true' || process.env.GITHUB_ACTIONS !== 'true'
      || process.env.NAVIGATION_WINDOWS_VENDOR_METADATA_E2E !== '1' || Number(process.versions.node.split('.')[0]) !== 24) {
    throw new Error('requires explicit disposable Windows CI metadata opt-in and Node24');
  }
  const nonce = randomUUID();
  root = join(realpathSync(process.env.RUNNER_TEMP ?? ''), `navigation-windows-test-${nonce}`);
  mkdirSync(root, { recursive: false });
  writeFileSync(join(root, '.owned-test-root'), `TEST navigation Windows ${nonce}\n`, { flag: 'wx' });
  const binary = join(root, 'navigation-native-probe.exe'); copyFileSync(realpathSync(process.argv[2] ?? ''), binary);
  const signtool = realpathSync(process.argv[3] ?? '');
  evidence.nonce = nonce; evidence.binarySHA256 = await hash(binary);
  evidence.signtoolSHA256 = await hash(signtool);
  const powershell = join(process.env.SystemRoot ?? '', 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
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
  const manifest = JSON.parse(manifestStep.stdout.replace(/^\uFEFF/, '').trim()) as Json; evidence.manifest = manifest;
  if (manifest.name !== metadata.name || manifest.publisher !== metadata.publisher || manifest.version !== metadata.version
      || manifest.architecture !== 'arm64' || !Array.isArray(manifest.applications) || manifest.applications.length === 0
      || !manifest.applications.some(app => Array.isArray(app.protocols) && app.protocols.includes('codex'))) {
    throw new Error('actual vendor manifest does not establish expected identity/protocol');
  }
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
