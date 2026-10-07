// One disposable Windows CI package. No real client route or private-key export.
import { spawnSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { copyFileSync, existsSync, lstatSync, mkdirSync, readFileSync, realpathSync, statSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

type Json = Record<string, unknown>;
type Step = { mode: string; pid: number; status: number | null; signal: string | null; error?: string;
  stdout: string; stderr: string; collectedAt: number };
// CI-only ACL/read-only setup cap; native callback and click budgets stay separate.
const PREPARE_BUDGET_MS = 90_000;
const evidence: Json = { status: 'failed', scope: 'packaged TEST cold toast COM callback only',
  nativeCallbackQualified: false, navigationQualified: false, clientRouteTested: false,
  vendorCompositionQualified: false, processQuiescenceQualified: false,
  showAttempts: null, showCallOutcome: 'not_started', nativeEffectUncertain: false,
  prepareBudgetMS: PREPARE_BUDGET_MS,
  sourceSHA: process.env.NAVIGATION_SOURCE_SHA, runnerLabel: process.env.NAVIGATION_WINDOWS_RUNNER,
  imageVersion: process.env.ImageVersion, nodeVersion: process.version, steps: [] };
let root: string | undefined;
let nonce: string | undefined;
let observer: string | undefined;
let binary: string | undefined;
let exitCode = 1;
let packageIntent = false;
let activationIntent = false;
let prepareOnly = false;
let bootstrap: { handoff: Json; paths: Map<string, string>; hashes: Map<string, string> } | undefined;

// Fixed source, saved verbatim in the owned root. Data arrives only in JSON.
// Mutation intents survive failures; cleanup reconciles exact owned identities.
const operator = String.raw`
param([string]$Mode, [string]$ContextFile)
$ErrorActionPreference = 'Stop'
if ($Mode -eq 'prepare') { $prepareClock = [Diagnostics.Stopwatch]::StartNew() }
function PreparationPhase([string]$Phase) {
  if ($Mode -ne 'prepare') { return }
  [Console]::Error.WriteLine('{"preparePhase":"' + $Phase + '","pid":' + $PID + ',"elapsedMs":' + $prepareClock.ElapsedMilliseconds + '}')
  [Console]::Error.Flush()
}
PreparationPhase 'entry'
if ($PSVersionTable.PSVersion.Major -ne 5 -or $PSVersionTable.PSVersion.Minor -ne 1) { throw 'target Windows PowerShell 5.1 required' }
PreparationPhase 'security_before'
$securityModule = Import-Module Microsoft.PowerShell.Security -PassThru -ErrorAction Stop
PreparationPhase 'security_after'
PreparationPhase 'utility_before'
# One actual dependency, imported from this fixed Windows PowerShell distribution.
# All import time remains inside prepare's unchanged budget; no speed is presumed.
$expectedPSHome = [IO.Path]::Combine([Environment]::GetEnvironmentVariable('SystemRoot'), 'System32\WindowsPowerShell\v1.0')
if (![StringComparer]::OrdinalIgnoreCase.Equals([IO.Path]::GetFullPath($PSHOME), [IO.Path]::GetFullPath($expectedPSHome))) { throw 'unexpected PowerShell distribution path' }
$utilityManifest = [IO.Path]::Combine($PSHOME, 'Modules\Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1')
$utilityFile = [IO.FileInfo]::new($utilityManifest)
if (!$utilityFile.Exists -or $utilityFile.Length -le 0 -or ($utilityFile.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'fixed Utility manifest absent or reparsed' }
Import-Module -Name $utilityManifest -ErrorAction Stop
PreparationPhase 'utility_after'
PreparationPhase 'context_before'
PreparationPhase 'context_read_before'
# Owned context IO bypasses provider-based reads, not later certificate/SDK providers.
# This is a bounded input contract; any cold-module cost in later phases remains measured.
function ReadOwnedUtf8([string]$Path, [int]$Limit) {
  $file = [IO.FileInfo]::new($Path)
  if (!$file.Exists -or ($file.Attributes -band ([IO.FileAttributes]::ReparsePoint -bor [IO.FileAttributes]::Directory)) -or
      $file.Length -le 0 -or $file.Length -gt $Limit) { throw 'invalid bounded owned input' }
  $stream = [IO.FileStream]::new($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
  try {
    $length = $stream.Length
    if ($length -ne $file.Length -or $length -le 0 -or $length -gt $Limit) { throw 'owned input length changed' }
    $bytes = [byte[]]::new([int]$length)
    $offset = 0
    while ($offset -lt $bytes.Length) {
      $read = $stream.Read($bytes, $offset, $bytes.Length - $offset)
      if ($read -le 0) { throw 'truncated owned input' }
      $offset += $read
    }
    if ($stream.ReadByte() -ne -1 -or $stream.Length -ne $length) { throw 'owned input grew' }
    $file.Refresh()
    if (!$file.Exists -or $file.Length -ne $length -or ($file.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'owned input changed' }
    return [Text.UTF8Encoding]::new($false, $true).GetString($bytes)
  } finally { $stream.Dispose() }
}
$contextPath = [IO.Path]::GetFullPath($ContextFile)
$contextRoot = [IO.Path]::GetDirectoryName($contextPath)
$contextDirectory = [IO.DirectoryInfo]::new($contextRoot)
if ([IO.Path]::GetFileName($contextPath) -cne 'operator-context.json' -or !$contextDirectory.Exists -or
    ($contextDirectory.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'invalid owned context path' }
$contextText = ReadOwnedUtf8 $contextPath 65536
PreparationPhase 'context_read_after'
PreparationPhase 'context_decode_before'
$c = $contextText | ConvertFrom-Json
PreparationPhase 'context_decode_after'
PreparationPhase 'context_path_before'
$r = [IO.Path]::GetFullPath($c.root)
if ($c.nonce -notmatch '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' -or
    [IO.Path]::GetFileName($r) -cne ('navigation-windows-test-' + $c.nonce) -or
    $r -cne $contextRoot) { throw 'invalid TEST context' }
$rootDirectory = [IO.DirectoryInfo]::new($r)
if (!$rootDirectory.Exists -or ($rootDirectory.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'reparsed TEST root' }
PreparationPhase 'context_path_after'
PreparationPhase 'context_marker_before'
if ((ReadOwnedUtf8 ([IO.Path]::Combine($r, '.owned-test-root')) 256).TrimEnd() -cne ('TEST navigation Windows ' + $c.nonce)) { throw 'TEST marker mismatch' }
PreparationPhase 'context_marker_after'
PreparationPhase 'context_identity_before'
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$session = [Diagnostics.Process]::GetCurrentProcess().SessionId
PreparationPhase 'context_identity_after'
PreparationPhase 'context_state_path_before'
$subject = 'CN=NavigationTest-' + $c.nonce
$name = 'NavigationTest.' + $c.nonce.Replace('-', '')
$stateFile = [IO.Path]::Combine($r, 'package-operator-state.json')
PreparationPhase 'context_state_path_after'
PreparationPhase 'context_after'
function HashFile($p) { return (Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash.ToLowerInvariant() }
function CertHash($cert) {
  $h = [Security.Cryptography.SHA256]::Create()
  try { return ([BitConverter]::ToString($h.ComputeHash($cert.RawData))).Replace('-', '').ToLowerInvariant() }
  finally { $h.Dispose() }
}
function Save($s) {
  $temp = $stateFile + '.tmp'
  [IO.File]::WriteAllText($temp, ($s | ConvertTo-Json -Depth 12), [Text.UTF8Encoding]::new($false))
  Move-Item -LiteralPath $temp -Destination $stateFile -Force -ErrorAction Stop
}
function CheckPackage($p, $s) {
  if ($p.Name -cne $name -or $p.Publisher -cne $subject -or $p.Version.ToString() -ne '1.0.0.0' -or
      $p.Architecture.ToString() -ne 'Arm64') { throw 'deployed TEST identity mismatch' }
  $exe = Join-Path $p.InstallLocation 'navigation-msix-probe.exe'
  if ((HashFile $exe) -cne $s.executableSHA256) { throw 'installed executable mismatch' }
  [xml]$manifest = Get-Content -LiteralPath (Join-Path $p.InstallLocation 'AppxManifest.xml') -Raw
  if ($manifest.Package.Identity.Name -cne $name -or $manifest.Package.Identity.Publisher -cne $subject -or
      $manifest.Package.Applications.Application.Id -cne 'TestSender') { throw 'installed manifest mismatch' }
  return $exe
}
if ($Mode -eq 'prepare') {
  PreparationPhase 'acl_before'
  $principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  if (!$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'ephemeral CI admin prerequisite absent' }
  $acl = [Security.AccessControl.DirectorySecurity]::new()
  $acl.SetAccessRuleProtection($true, $false)
  $acl.SetOwner([Security.Principal.SecurityIdentifier]::new($sid))
  foreach ($id in @($sid, 'S-1-5-18', 'S-1-5-32-544')) {
    $rule = [Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($id),
      'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($rule)
  }
  Set-Acl -LiteralPath $r -AclObject $acl -ErrorAction Stop
  $actual = Get-Acl -LiteralPath $r
  if (!$actual.AreAccessRulesProtected -or $actual.Owner -ne ([Security.Principal.SecurityIdentifier]::new($sid)).Translate([Security.Principal.NTAccount]).Value) { throw 'private root ACL mismatch' }
  foreach ($rule in $actual.Access) {
    $id = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
    if ($id -notin @($sid, 'S-1-5-18', 'S-1-5-32-544') -or $rule.IsInherited -or $rule.AccessControlType -ne 'Allow') { throw 'unexpected root ACL grant' }
  }
  PreparationPhase 'acl_after'
  PreparationPhase 'commands_before'
  foreach ($cmd in @('New-SelfSignedCertificate', 'Export-Certificate', 'Import-Certificate', 'Add-AppxPackage', 'Remove-AppxPackage', 'Get-AppxPackage')) { Get-Command $cmd -ErrorAction Stop | Out-Null }
  PreparationPhase 'commands_after'
  PreparationPhase 'package_absence_before'
  if (@(Get-AppxPackage -Name $name -ErrorAction Stop).Count -ne 0) { throw 'TEST package already exists' }
  PreparationPhase 'package_absence_after'
  PreparationPhase 'certificate_absence_before'
  if (@(Get-ChildItem Cert:\CurrentUser\My | Where-Object Subject -CEQ $subject).Count -ne 0) { throw 'TEST signer already exists' }
  if (@(Get-ChildItem Cert:\LocalMachine\TrustedPeople | Where-Object Subject -CEQ $subject).Count -ne 0) { throw 'TEST trust already exists' }
  PreparationPhase 'certificate_absence_after'
  PreparationPhase 'sdk_before'
  $sdk = Get-ChildItem -LiteralPath (Join-Path ([Environment]::GetEnvironmentVariable('ProgramFiles(x86)')) 'Windows Kits\10\bin') -Directory |
    Where-Object { $_.Name -match '^10\.0\.\d+\.\d+$' -and
      (Test-Path -LiteralPath (Join-Path $_.FullName 'arm64\makeappx.exe')) -and
      (Test-Path -LiteralPath (Join-Path $_.FullName 'arm64\signtool.exe')) } |
    Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
  if (!$sdk) { throw 'architecture-matched SDK tools absent' }
  PreparationPhase 'sdk_after'
  PreparationPhase 'hash_before'
  $s = [ordered]@{ nonce=$c.nonce; userSid=$sid; session=$session; name=$name; publisher=$subject;
    executableSHA256=(HashFile $c.binary); makeappx=(Join-Path $sdk.FullName 'arm64\makeappx.exe');
    signtool=(Join-Path $sdk.FullName 'arm64\signtool.exe'); sdkVersion=$sdk.Name;
    uniquePackageAbsent=$true; uniqueSignerAbsent=$true; uniqueTrustAbsent=$true;
    powershellVersion=$PSVersionTable.PSVersion.ToString(); securityModulePath=$securityModule.Path }
  $s.makeappxSHA256 = HashFile $s.makeappx; $s.signtoolSHA256 = HashFile $s.signtool
  PreparationPhase 'hash_after'
  PreparationPhase 'state_before'
  Save $s
  PreparationPhase 'state_after'
  exit 0
}
if (!(Test-Path -LiteralPath $stateFile)) { throw 'owned operator state absent' }
$loaded = Get-Content -LiteralPath $stateFile -Raw -Encoding UTF8 | ConvertFrom-Json
$s = [ordered]@{}
foreach ($property in $loaded.PSObject.Properties) { $s[$property.Name] = $property.Value }
if ($s.nonce -cne $c.nonce -or $s.userSid -cne $sid -or $s.session -ne $session -or
    $s.name -cne $name -or $s.publisher -cne $subject) { throw 'operator ownership mismatch' }
function Tool($label, $exe, $toolArguments) {
  $s.toolIntent = $label; Save $s
  & $exe @toolArguments *> (Join-Path $r ($label + '.log'))
  $rc = $LASTEXITCODE; $s[$label + 'ExitCode'] = $rc; Save $s
  if ($rc -ne 0) { throw ($label + ' failed: ' + $rc) }
}
if ($Mode -eq 'package') {
  if ((HashFile $c.binary) -cne $s.executableSHA256 -or (HashFile $s.makeappx) -cne $s.makeappxSHA256 -or
      (HashFile $s.signtool) -cne $s.signtoolSHA256) { throw 'captured tooling changed' }
  $layout = Join-Path $r 'layout'; New-Item -ItemType Directory -Path $layout -ErrorAction Stop | Out-Null
  Copy-Item -LiteralPath $c.binary -Destination (Join-Path $layout 'navigation-msix-probe.exe') -ErrorAction Stop
  Add-Type -AssemblyName System.Drawing
  foreach ($asset in @(@('store.png',50), @('square44.png',44), @('square150.png',150))) {
    $bitmap = [Drawing.Bitmap]::new([int]$asset[1], [int]$asset[1])
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try { $graphics.Clear([Drawing.Color]::DarkBlue); $bitmap.Save((Join-Path $layout $asset[0]), [Drawing.Imaging.ImageFormat]::Png) }
    finally { $graphics.Dispose(); $bitmap.Dispose() }
  }
  $callbackArgs = 'callback "' + $r + '" ' + $c.nonce
  $escaped = [Security.SecurityElement]::Escape($callbackArgs)
  $manifest = @"
<?xml version="1.0" encoding="utf-8"?>
<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10" xmlns:uap="http://schemas.microsoft.com/appx/manifest/uap/windows10" xmlns:uap10="http://schemas.microsoft.com/appx/manifest/uap/windows10/10" xmlns:desktop="http://schemas.microsoft.com/appx/manifest/desktop/windows10" xmlns:com="http://schemas.microsoft.com/appx/manifest/com/windows10" xmlns:rescap="http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities" IgnorableNamespaces="uap uap10 desktop com rescap">
<Identity Name="$name" Publisher="$subject" Version="1.0.0.0" ProcessorArchitecture="arm64"/>
<Properties><DisplayName>Navigation TEST</DisplayName><PublisherDisplayName>Navigation TEST</PublisherDisplayName><Logo>store.png</Logo></Properties>
<Dependencies><TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.19041.0" MaxVersionTested="10.0.26200.0"/></Dependencies>
<Applications><Application Id="TestSender" Executable="navigation-msix-probe.exe" EntryPoint="Windows.FullTrustApplication" uap10:RuntimeBehavior="packagedClassicApp" uap10:TrustLevel="mediumIL">
<uap:VisualElements DisplayName="Navigation TEST" Description="Disposable cold callback TEST" BackgroundColor="transparent" Square44x44Logo="square44.png" Square150x150Logo="square150.png"/>
<Extensions><desktop:Extension Category="windows.toastNotificationActivation"><desktop:ToastNotificationActivation ToastActivatorCLSID="$($c.nonce)"/></desktop:Extension>
<com:Extension Category="windows.comServer"><com:ComServer><com:ExeServer Executable="navigation-msix-probe.exe" Arguments="$escaped" DisplayName="Navigation TEST"><com:Class Id="$($c.nonce)" DisplayName="Navigation TEST"/></com:ExeServer></com:ComServer></com:Extension></Extensions>
</Application></Applications><Capabilities><rescap:Capability Name="runFullTrust"/></Capabilities></Package>
"@
  [IO.File]::WriteAllText((Join-Path $layout 'AppxManifest.xml'), $manifest, [Text.UTF8Encoding]::new($false))
  $s.manifestSHA256 = HashFile (Join-Path $layout 'AppxManifest.xml'); Save $s
  $msix = Join-Path $r 'navigation-test.msix'
  Tool 'makeappx' $s.makeappx @('pack','/h','SHA256','/d',$layout,'/p',$msix)
  $s.certCreateIntent=$true; Save $s
  $cert = New-SelfSignedCertificate -Type Custom -KeyUsage DigitalSignature -CertStoreLocation 'Cert:\CurrentUser\My' -Subject $subject -FriendlyName $subject -Provider 'Microsoft Software Key Storage Provider' -KeyAlgorithm RSA -KeyLength 2048 -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3','2.5.29.19={text}') -NotAfter (Get-Date).AddDays(1) -ErrorAction Stop
  $s.thumbprint=$cert.Thumbprint; $s.signerDERSHA256=CertHash $cert; Save $s
  if ($cert.Subject -cne $subject -or !$cert.HasPrivateKey) { throw 'created signer mismatch' }
  $rsa = [Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($cert)
  try {
    if ($rsa -isnot [Security.Cryptography.RSACng] -or $rsa.Key.Provider.Provider -cne 'Microsoft Software Key Storage Provider') { throw 'owned signer key provider mismatch' }
    $s.signerKeyName=$rsa.Key.KeyName; $s.signerKeyProvider=$rsa.Key.Provider.Provider; Save $s
  } finally { if ($rsa) { $rsa.Dispose() } }
  $cer = Join-Path $r 'public-signer.cer'
  Export-Certificate -Cert $cert -FilePath $cer -Type CERT -ErrorAction Stop | Out-Null
  if ((HashFile $cer) -cne $s.signerDERSHA256) { throw 'public certificate mismatch' }
  $trustPath = 'Cert:\LocalMachine\TrustedPeople\' + $cert.Thumbprint
  if (Test-Path -LiteralPath $trustPath) { throw 'exact trust identity was not absent' }
  $s.exactThumbprintAbsentBeforeImport=$true; $s.trustImportIntent=$true; Save $s
  Import-Certificate -FilePath $cer -CertStoreLocation 'Cert:\LocalMachine\TrustedPeople' -ErrorAction Stop | Out-Null
  $trusted = Get-Item -LiteralPath $trustPath -ErrorAction Stop
  if ($trusted.Subject -cne $subject -or (CertHash $trusted) -cne $s.signerDERSHA256) { throw 'machine trust DER mismatch' }
  $s.trustImported=$true; Save $s
  Tool 'signtool_sign' $s.signtool @('sign','/fd','SHA256','/sha1',$cert.Thumbprint,'/s','My',$msix)
  Tool 'signtool_verify' $s.signtool @('verify','/pa','/v',$msix)
  $signature = Get-AuthenticodeSignature -LiteralPath $msix
  if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -cne $subject -or
      (CertHash $signature.SignerCertificate) -cne $s.signerDERSHA256) { throw 'signed package signer mismatch' }
  $s.packageSHA256=HashFile $msix; $s.deployIntent=$true; Save $s
  Add-AppxPackage -Path $msix -ErrorAction Stop
  $packages = @(Get-AppxPackage -Name $name -ErrorAction Stop)
  if ($packages.Count -ne 1) { throw 'unique deployed package absent' }
  $installedExe = CheckPackage $packages[0] $s
  $s.packageFullName=$packages[0].PackageFullName; $s.packageFamilyName=$packages[0].PackageFamilyName
  $s.installedExecutable=$installedExe; $s.deployed=$true; Save $s; exit 0
}
if ($Mode -ne 'cleanup') { throw 'unknown operator mode' }
$errors = [Collections.Generic.List[string]]::new()
try {
  $packages = @(Get-AppxPackage -Name $name -ErrorAction Stop)
  if ($packages.Count -gt 1) { throw 'ambiguous cleanup package' }
  if ($packages.Count -eq 1) {
    if (!$s.deployIntent -or !$s.uniquePackageAbsent) { throw 'package ownership unknown' }
    $null = CheckPackage $packages[0] $s
    if ($s.packageFullName -and $packages[0].PackageFullName -cne $s.packageFullName) { throw 'captured package changed' }
    $s.cleanupPackageFullName=$packages[0].PackageFullName; Save $s
    Remove-AppxPackage -Package $packages[0].PackageFullName -ErrorAction Stop
  }
  $s.packageAbsent=@(Get-AppxPackage -Name $name -ErrorAction Stop).Count -eq 0; Save $s
} catch { $errors.Add('package: ' + $_.Exception.Message) }
foreach ($store in @('LocalMachine\TrustedPeople','CurrentUser\My')) {
  try {
    if (!$s.thumbprint) {
      if ($s.certCreateIntent) { throw 'certificate creation outcome unknown' }
      $s[$store + 'Absent']=$true; continue
    }
    if ($s.thumbprint -notmatch '^[A-F0-9]{40}$' -or !$s.signerDERSHA256) { throw 'invalid owned certificate identity' }
    $path = 'Cert:\' + $store + '\' + $s.thumbprint
    if (Test-Path -LiteralPath $path) {
      $cert = Get-Item -LiteralPath $path -ErrorAction Stop
      if ($cert.Subject -cne $subject -or (CertHash $cert) -cne $s.signerDERSHA256 -or !$s.uniqueSignerAbsent -or
          ($store -eq 'LocalMachine\TrustedPeople' -and (!$s.trustImportIntent -or !$s.exactThumbprintAbsentBeforeImport))) { throw 'certificate ownership mismatch' }
      if ($store -eq 'CurrentUser\My') {
        if (!$s.signerKeyName -or $s.signerKeyProvider -cne 'Microsoft Software Key Storage Provider') { throw 'owned signer key identity unknown' }
        $rsa = [Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($cert)
        try {
          if ($rsa -isnot [Security.Cryptography.RSACng] -or $rsa.Key.KeyName -cne $s.signerKeyName -or
              $rsa.Key.Provider.Provider -cne $s.signerKeyProvider) { throw 'owned signer key changed' }
        } finally { if ($rsa) { $rsa.Dispose() } }
        $s.signerKeyDeleteIntent=$true; Save $s
        Remove-Item -LiteralPath $path -DeleteKey -ErrorAction Stop
      } else { Remove-Item -LiteralPath $path -ErrorAction Stop }
    }
    $s[$store + 'Absent']=!(Test-Path -LiteralPath $path)
    if ($store -eq 'CurrentUser\My') {
      if (!$s.signerKeyName -or $s.signerKeyProvider -cne 'Microsoft Software Key Storage Provider') { throw 'owned signer key absence unknown' }
      $s.signerKeyAbsent=![Security.Cryptography.CngKey]::Exists($s.signerKeyName,
        [Security.Cryptography.CngProvider]::new($s.signerKeyProvider), [Security.Cryptography.CngKeyOpenOptions]::None)
      if (!$s.signerKeyAbsent) { throw 'owned signer private key persists' }
    }
    Save $s
  } catch { $errors.Add($store + ': ' + $_.Exception.Message) }
}
$s.cleanupErrors=@($errors); $s.cleanupPassed=$errors.Count -eq 0 -and $s.packageAbsent -and $s['LocalMachine\TrustedPeopleAbsent'] -and $s['CurrentUser\MyAbsent'] -and (!$s.certCreateIntent -or $s.signerKeyAbsent); Save $s
if (!$s.cleanupPassed) { throw 'exact owned package/certificate cleanup failed' }
`;

function hash(path: string): string { return createHash('sha256').update(readFileSync(path)).digest('hex'); }
function bindBootstrap(): void {
  if (process.env.NAVIGATION_WINDOWS_MSIX_MODE !== 'oobe_then_msix_callback') return;
  const supplied = resolve(process.env.NAVIGATION_WINDOWS_OOBE_BOOTSTRAP_ROOT ?? '');
  const path = realpathSync(supplied);
  const st = lstatSync(supplied), name = basename(path), id = name.slice('navigation-windows-test-'.length);
  if (!st.isDirectory() || st.isSymbolicLink() || dirname(path) !== realpathSync(process.env.RUNNER_TEMP!)
      || !/^navigation-windows-test-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(name)
      || path === root || id === nonce) {
    throw new Error('fresh sibling bootstrap TEST root required');
  }
  const paths = new Map<string, string>(), hashes = new Map<string, string>();
  function bytes(name: string, limit: number): Buffer {
    const file = join(path, name), fs = lstatSync(file);
    if (!fs.isFile() || fs.isSymbolicLink() || fs.nlink !== 1 || realpathSync(file) !== file
        || fs.size < 1 || fs.size > limit) throw new Error('bounded immutable bootstrap member required');
    const data = readFileSync(file); if (data.length !== fs.size) throw new Error('bootstrap member changed');
    paths.set(name, file); hashes.set(name, createHash('sha256').update(data).digest('hex')); return data;
  }
  const marker = bytes('.owned-test-root', 256).toString('utf8');
  const handoff = JSON.parse(bytes('same-job-bootstrap.json', 65_536).toString('utf8')) as Json;
  const result = JSON.parse(bytes('evidence.json', 131_072).toString('utf8')) as Json;
  const packet = bytes('oobe-setup-records.json', 6_291_456);
  if (marker !== 'TEST navigation Windows ' + id + '\n' || handoff.root !== path || handoff.nonce !== id
      || handoff.runID !== process.env.GITHUB_RUN_ID || handoff.job !== process.env.GITHUB_JOB
      || handoff.attempt !== '1' || handoff.sourceSHA !== evidence.sourceSHA
      || handoff.observerSHA256 !== evidence.observerSHA256 || handoff.evidenceSHA256 !== hashes.get('evidence.json')
      || handoff.recordsSHA256 !== hashes.get('oobe-setup-records.json')
      || typeof handoff.userSid !== 'string' || !/^S-1-5-[0-9-]{1,160}$/.test(handoff.userSid)
      || !Number.isInteger(handoff.session) || Number(handoff.session) <= 0
      || result.status !== 'guest_setup_complete' || result.root !== path || result.nonce !== id
      || result.sourceSHA !== evidence.sourceSHA || result.binarySHA256 !== evidence.observerSHA256
      || result.imageVersion !== evidence.imageVersion || result.runnerLabel !== evidence.runnerLabel
      || result.oobeSetupQualified !== true || result.captureQualified !== true
      || result.oobeSetupActorCollected !== true || result.captureActorCollected !== true
      || result.invokeEffectUncertain !== false || result.nativeEffectUncertain !== false
      || result.showAttempts !== 0 || result.noKeyboardOrPointerInputProved !== true
      || result.setupRecordErrors || result.setupRecordPublicationError
      || result.setupRecordsSHA256 !== hashes.get('oobe-setup-records.json')) throw new Error('bootstrap custody/terminal proof absent');
  const terminal = result.oobeSetupResult as Json, capture = result['desktop-capture.json'] as Json;
  if (!terminal || terminal.setupQualified !== true || terminal.ownedOOBEWindowGone !== true
      || terminal.invokeEffectUncertain !== false || terminal.acceptCallsEntered !== 1
      || terminal.invokeCallsEntered !== terminal.invokeCallsReturned
      || terminal.progressionsObserved !== terminal.invokeCallsReturned
      || !capture || capture.session !== handoff.session || capture.sourceSHA !== evidence.sourceSHA
      || capture.binarySHA256 !== evidence.observerSHA256 || !Array.isArray(result.steps)
      || result.steps.length !== 4 || result.steps.some((s: Json, index: number) =>
        s.mode !== ['center-policy', 'preflight', 'oobe-setup', 'desktop-capture'][index]
        || s.status !== 0 || s.signal !== null || s.error || !Number.isInteger(s.pid) || Number(s.pid) <= 0)) {
    throw new Error('known collected bootstrap actors required');
  }
  const records = JSON.parse(packet.toString('utf8')) as Json;
  if (records.nonce !== id || records.actorPID !== (result.steps[2] as Json).pid
      || !records.records || typeof records.records !== 'object' || Array.isArray(records.records)
      || Object.keys(records.records).length !== result.setupRecordCount || Number(result.setupRecordCount) > 198
      || Object.entries(records.records).some(([key, value]: [string, Json]) =>
        !/^oobe-setup-(intent|result|after-preflight|[0-4]-(armed|returned|progress|(before|after-[0-7])-(preflight|after-preflight|intent|uia)))\.json$/.test(key)
        || !value || value.pid !== records.actorPID || value.nonce !== id
        || Buffer.byteLength(JSON.stringify(value)) > (key.endsWith('-uia.json') ? 65_536 : 16_384))) {
    throw new Error('bounded bootstrap packet binding absent');
  }
  bootstrap = { handoff, paths, hashes };
  evidence.bootstrap = { nonce: id, session: handoff.session, sourceSHA: handoff.sourceSHA,
    handoffSHA256: hashes.get('same-job-bootstrap.json'), evidenceSHA256: hashes.get('evidence.json'),
    recordsSHA256: hashes.get('oobe-setup-records.json'), observerSHA256: handoff.observerSHA256 };
}
function revalidateBootstrap(): void {
  if (!bootstrap) return;
  for (const [name, path] of bootstrap.paths) {
    const st = lstatSync(path);
    const limit = name === 'oobe-setup-records.json' ? 6_291_456 : name === 'evidence.json' ? 131_072 : 65_536;
    if (!st.isFile() || st.isSymbolicLink() || st.nlink !== 1 || realpathSync(path) !== path
        || st.size < 1 || st.size > limit
        || hash(path) !== bootstrap.hashes.get(name)) throw new Error('bootstrap custody changed before package effects');
  }
  if (hash(observer!) !== evidence.observerSHA256) throw new Error('observer changed before package effects');
}
function read(name: string): Json {
  if (!root) throw new Error('owned root absent');
  const path = join(root, name);
  if (statSync(path).size > 65_536) throw new Error('oversized record: ' + name);
  const result: unknown = JSON.parse(readFileSync(path, 'utf8'));
  if (!result || typeof result !== 'object' || Array.isArray(result)) throw new Error('invalid record: ' + name);
  return result as Json;
}
function execute(mode: string, exe: string, args: string[], timeout: number, env = process.env): Step {
  if (!root) throw new Error('owned root absent');
  const result = spawnSync(exe, args, { cwd: root, env, encoding: 'utf8', timeout,
    maxBuffer: 65_536, windowsHide: false });
  const step: Step = { mode, pid: result.pid, status: result.status, signal: result.signal,
    stdout: result.stdout ?? '', stderr: result.stderr ?? '', collectedAt: Date.now(),
    ...(result.error ? { error: result.error.message } : {}) };
  (evidence.steps as Step[]).push(step);
  writeFileSync(join(root, 'controller-progress.json'), JSON.stringify(evidence, null, 2));
  return step;
}
function success(step: Step): void {
  if (step.error || step.signal || step.status !== 0) throw new Error(step.mode + ' failed: ' + (step.error ?? step.stderr));
}
function preparationTrace(step: Step): void {
  evidence.prepareActorCollected = !step.error && !step.signal && step.status !== null;
  evidence.prepareCollectionUnknown = evidence.prepareActorCollected !== true;
  const contextPhases = ['read', 'decode', 'path', 'marker', 'identity', 'state_path']
    .flatMap(phase => [`context_${phase}_before`, `context_${phase}_after`]);
  const phases = ['entry', ...['security', 'utility', 'context', 'acl', 'commands', 'package_absence',
    'certificate_absence', 'sdk', 'hash', 'state'].flatMap(phase => [phase + '_before', phase + '_after'])];
  phases.splice(phases.indexOf('context_after'), 0, ...contextPhases);
  const lines = step.stderr.split(/\r?\n/).filter(line => line.startsWith('{"preparePhase":'));
  if (lines.length > phases.length || Buffer.byteLength(lines.join('\n')) > 16_384) throw new Error('bounded preparation trace required');
  const trace = lines.map(line => JSON.parse(line) as Json);
  for (const [index, row] of trace.entries()) {
    if (Object.keys(row).sort().join(',') !== 'elapsedMs,pid,preparePhase' || row.pid !== step.pid
        || row.preparePhase !== phases[index] || !Number.isInteger(row.elapsedMs) || Number(row.elapsedMs) < 0
        || Number(row.elapsedMs) >= PREPARE_BUDGET_MS || index > 0 && Number(row.elapsedMs) < Number(trace[index - 1].elapsedMs)) {
      throw new Error('preparation checkpoint identity/order/budget invalid');
    }
  }
  evidence.prepareTrace = trace; evidence.prepareLastPhase = trace.at(-1)?.preparePhase ?? 'no_entry_observed';
  writeFileSync(join(root!, 'prepare-trace.json'), JSON.stringify({ pid: step.pid, trace,
    collected: evidence.prepareActorCollected, collectionUnknown: evidence.prepareCollectionUnknown }), { flag: 'wx' });
  if (step.status === 0 && trace.length !== phases.length) throw new Error('complete preparation trace absent');
}
function native(mode: string, timeout: number, useObserver = false): Step {
  if (!root || !nonce || !binary || !observer) throw new Error('native fixture absent');
  return execute(mode, useObserver ? observer : binary, [mode, root, nonce], timeout);
}
function powershell(mode: string, timeout: number): Step {
  if (!root || !process.env.SystemRoot) throw new Error('PowerShell prerequisite absent');
  return execute('package-' + mode,
    join(process.env.SystemRoot, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe'),
    ['-NoLogo', '-NoProfile', '-NonInteractive', '-File', join(root, 'package-operator.ps1'), mode, join(root, 'operator-context.json')], timeout,
    Object.fromEntries(Object.entries(process.env).filter(([key]) => key.toUpperCase() !== 'PSMODULEPATH')));
}
function observeShow(): void {
  if (!root || !nonce) return;
  for (const name of ['show-outcome.json', 'sender-failure.json']) {
    if (!existsSync(join(root, name))) continue;
    const result = read(name); const ready = read('identity-ready.json');
    if (result.nonce !== nonce || result.pid !== ready.pid || typeof result.showCallEntered !== 'boolean'
        || typeof result.showCallReturned !== 'boolean' || (result.showCallReturned && !result.showCallEntered)) {
      throw new Error('native Show state mismatch');
    }
    evidence.showAttempts = result.showCallEntered ? 1 : 0;
    evidence.showCallOutcome = result.showCallEntered ? (result.showCallReturned ? 'returned' : 'entered_error') : 'not_called';
    evidence.nativeEffectUncertain = result.showCallEntered && !result.showCallReturned;
  }
}
async function main(): Promise<void> {
  if (process.platform !== 'win32' || process.env.CI !== 'true' || process.env.GITHUB_ACTIONS !== 'true'
      || process.env.AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E !== '1'
      || process.env.NAVIGATION_WINDOWS_RUNNER !== 'windows-11-vs2026-arm'
      || Number(process.versions.node.split('.')[0]) !== 24) throw new Error('explicit disposable Windows client CI required');
  const mode = process.env.NAVIGATION_WINDOWS_MSIX_MODE;
  if (!['native_callback', 'oobe_then_msix_callback', 'prepare_only'].includes(mode ?? '')
      || process.env.GITHUB_REPOSITORY !== '777genius/agent-notifications'
      || process.env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || process.env.GITHUB_RUN_ATTEMPT !== '1'
      || process.arch !== 'arm64' || !/^[0-9a-f]{40}$/.test(process.env.NAVIGATION_SOURCE_SHA ?? '')) {
    throw new Error('first explicit manual packaged TEST mode and exact source required');
  }
  if (mode !== 'oobe_then_msix_callback' && process.env.NAVIGATION_WINDOWS_OOBE_BOOTSTRAP_ROOT) throw new Error('unexpected bootstrap in standalone mode');
  prepareOnly = mode === 'prepare_only';
  if (prepareOnly) { evidence.scope = 'TEST new-root ACL and read-only package/SDK prerequisites only'; evidence.prepareComplete = false; }
  evidence.mode = mode;
  if (!process.env.RUNNER_TEMP) throw new Error('owned CI temp absent');
  nonce = randomUUID(); root = join(realpathSync(process.env.RUNNER_TEMP), 'navigation-windows-test-' + nonce);
  mkdirSync(root); writeFileSync(join(root, '.owned-test-root'), 'TEST navigation Windows ' + nonce + '\n', { flag: 'wx' });
  binary = join(root, 'navigation-msix-probe.exe'); observer = join(root, 'navigation-native-probe.exe');
  copyFileSync(realpathSync(resolve(process.argv[2] ?? '')), binary);
  copyFileSync(realpathSync(resolve(process.argv[3] ?? '')), observer);
  writeFileSync(join(root, 'package-operator.ps1'), operator, { flag: 'wx' });
  writeFileSync(join(root, 'operator-context.json'), JSON.stringify({ nonce, root, binary }), { flag: 'wx' });
  evidence.root = root; evidence.binarySHA256 = hash(binary); evidence.observerSHA256 = hash(observer);
  evidence.operatorSHA256 = hash(join(root, 'package-operator.ps1'));
  for (const kind of ['native', 'msix']) {
    const imports = join(process.env.RUNNER_TEMP, 'navigation-' + kind + '-imports.log');
    if (!statSync(imports).isFile() || statSync(imports).size > 65_536) throw new Error('compiled import evidence absent');
    copyFileSync(imports, join(root, 'navigation-' + kind + '-imports.log'));
    evidence[kind + 'ImportEvidenceSHA256'] = hash(imports);
  }
  bindBootstrap();
  const preparation = powershell('prepare', PREPARE_BUDGET_MS); // New-root ACL + read-only queries, no certificate/package mutation.
  preparationTrace(preparation); success(preparation);
  if (prepareOnly) {
    const state = read('package-operator-state.json');
    if (state.nonce !== nonce || state.executableSHA256 !== evidence.binarySHA256 || state.uniquePackageAbsent !== true
        || state.uniqueSignerAbsent !== true || state.uniqueTrustAbsent !== true) throw new Error('preparation state binding absent');
    evidence.prepareComplete = true; exitCode = 0; return;
  }
  const fresh = native('preflight', 15_000, true); success(fresh);
  const desktop = read('preflight.json'), prepared = read('package-operator-state.json');
  if (desktop.ready !== true || desktop.pid !== fresh.pid || desktop.nonce !== nonce
      || bootstrap && (desktop.session !== bootstrap.handoff.session
        || prepared.session !== bootstrap.handoff.session || prepared.userSid !== bootstrap.handoff.userSid)) {
    throw new Error('fresh interactive user/session bootstrap binding unavailable');
  }
  revalidateBootstrap();
  evidence.sameJobBootstrapQualified = bootstrap !== undefined;
  packageIntent = true; evidence.packageMutationOutcomeUnknown = true;
  const packaged = powershell('package', 150_000);
  evidence.packageMutationOutcomeUnknown = !!packaged.error || !!packaged.signal || packaged.status === null;
  success(packaged);
  const state = read('package-operator-state.json');
  for (const key of ['packageFullName', 'packageFamilyName', 'installedExecutable', 'userSid', 'executableSHA256']) {
    if (typeof state[key] !== 'string' || !(state[key] as string).length) throw new Error('missing installed identity: ' + key);
  }
  if (state.deployed !== true || state.executableSHA256 !== evidence.binarySHA256 || typeof state.session !== 'number') throw new Error('deployed identity mismatch');
  writeFileSync(join(root, 'msix-spec.json'), JSON.stringify({ nonce, packageFullName: state.packageFullName,
    aumid: state.packageFamilyName + '!TestSender', installedExecutable: state.installedExecutable,
    userSid: state.userSid, session: state.session, executableSHA256: state.executableSHA256 }), { flag: 'wx' });
  activationIntent = true; evidence.nativeEffectUncertain = true; evidence.showCallOutcome = 'unknown';
  const sender = native('activate', 45_000); observeShow(); success(sender);
  const exited = read('sender-exit.json');
  if (exited.collected !== true || exited.exitCode !== 0 || evidence.showAttempts !== 1 || evidence.showCallOutcome !== 'returned') throw new Error('collected sender/Show proof absent');
  // Read-only diagnostics do not establish visible UI or a native callback.
  native('history', 15_000);
  const invoke = native('invoke', 30_000, true); success(invoke);
  if (read('ui-invoke.json').invokeHRESULT !== 0) throw new Error('real Shell Invoke failed');
  for (let i = 0; i < 100 && !existsSync(join(root, 'effect.json')); i++) await delay(100);
  success(native('collect', 12_000));
  if (read('callback-exit.json').collected !== true || read('callback-exit.json').exitCode !== 0
      || read('callback-terminal.json').valid !== true) throw new Error('cold callback exit proof absent');
  evidence.coldNativeCallbackObserved = true; evidence.nativeEffectUncertain = false; exitCode = 0;
}
try { await main(); }
catch (error: unknown) { evidence.error = error instanceof Error ? error.message : String(error); }
finally {
  if (root) {
    let cleaned = true;
    if (activationIntent && existsSync(join(root, 'msix-spec.json'))) {
      try { observeShow(); }
      catch (error: unknown) { evidence.showObservationError = String(error); cleaned = false; }
      try {
        success(native('cleanup', 20_000));
        const sender = read('sender-cleanup.json'); const callback = read('callback-cleanup.json');
        evidence.nativeProcessCleanupQualified = (sender.collected === true || sender.originalAbsent === true)
          && (callback.collected === true || callback.originalAbsent === true);
        // Missing records are unknown process scope. Package removal is a separate boundary.
        evidence.nativeProcessCleanupUnknown = evidence.nativeProcessCleanupQualified !== true;
        if (evidence.nativeProcessCleanupUnknown) cleaned = false;
      }
      catch (error: unknown) { evidence.nativeCleanupError = String(error); cleaned = false; }
    }
    if (packageIntent) {
      try { success(powershell('cleanup', 90_000)); if (read('package-operator-state.json').cleanupPassed !== true) throw new Error('cleanup proof absent'); }
      catch (error: unknown) { evidence.packageCleanupError = String(error); cleaned = false; }
    }
    if (evidence.packageMutationOutcomeUnknown === true || evidence.prepareCollectionUnknown === true) cleaned = false;
    evidence.cleanupPassed = cleaned;
    evidence.nativeCallbackQualified = !prepareOnly && exitCode === 0 && cleaned;
    const preparationPassed = prepareOnly && evidence.prepareComplete === true && exitCode === 0 && cleaned;
    evidence.status = preparationPassed ? 'prepare_complete' : evidence.nativeCallbackQualified ? 'qualified' : 'failed';
    if (!evidence.nativeCallbackQualified && !preparationPassed) exitCode = 1;
    writeFileSync(join(root, 'evidence.json'), JSON.stringify(evidence, null, 2) + '\n', { flag: 'wx' });
    if (process.env.GITHUB_OUTPUT) writeFileSync(process.env.GITHUB_OUTPUT, 'evidence_root=' + root + '\n', { flag: 'a' });
  }
  console.log(JSON.stringify({ status: evidence.status, nativeCallbackQualified: evidence.nativeCallbackQualified,
    navigationQualified: false, showAttempts: evidence.showAttempts, nativeEffectUncertain: evidence.nativeEffectUncertain }));
}
process.exitCode = exitCode;
