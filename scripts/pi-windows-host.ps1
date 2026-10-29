param([string]$Root, [string]$Receipt, [switch]$VerifyOnly)
$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$driverHash = '2dc35db3dfad0f25771a3e59af38e8b1268878ebe127476ea75fee109f2927dd'
$infHash = '61c857be74831cc299d9be62f8d49d137f14063454fc54f859d5bc9b4b813daf'
$catalogHash = '0a921ebadbe39cf3fa7c14ff37d1ca22565a9651244bd3ac177dc810bc99072e'
function Hash([string]$Path) { (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
function NativeTool([string]$Pattern) {
    $file = Get-ChildItem -Path $Pattern -File | Sort-Object FullName -Descending | Select-Object -First 1
    if (!$file) { throw 'Installed native Microsoft tool unavailable' }
    $reader = [IO.BinaryReader]::new([IO.File]::OpenRead($file.FullName))
    try {
        if ($reader.ReadUInt16() -ne 0x5a4d) { throw 'Tool DOS header invalid' }
        $reader.BaseStream.Position = 0x3c; $offset = $reader.ReadUInt32(); $reader.BaseStream.Position = $offset
        if ($reader.ReadUInt32() -ne 0x4550 -or $reader.ReadUInt16() -ne 0xaa64) { throw 'Native ARM64 Microsoft tool required' }
    } finally { $reader.Dispose() }
    $signature = Get-AuthenticodeSignature -LiteralPath $file.FullName
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch '(?:^|,\s*)O=Microsoft Corporation(?:,|$)') { throw 'Microsoft tool signature invalid' }
    return $file.FullName
}
function DriverImage {
    $service = @(Get-CimInstance Win32_SystemDriver -Filter "Name='VBAudioVACMME'")
    if ($service.Count -ne 1 -or !$service[0].Started -or $service[0].State -ne 'Running') { throw 'Exact virtual driver is not running' }
    $path = ([string]$service[0].PathName).Trim('"')
    if ($path.StartsWith('\??\', [StringComparison]::OrdinalIgnoreCase)) { $path = $path.Substring(4) }
    if ($path.StartsWith('\SystemRoot\', [StringComparison]::OrdinalIgnoreCase)) { $path = Join-Path $env:WINDIR $path.Substring(12) }
    if ($path.StartsWith('System32\', [StringComparison]::OrdinalIgnoreCase)) { $path = Join-Path $env:WINDIR $path }
    return (Resolve-Path -LiteralPath $path).ProviderPath
}
function VerifyDriver($Proof) {
    $source = git -C $repositoryRoot rev-parse HEAD
    if ($LASTEXITCODE -or $Proof.schema_version -ne 1 -or $Proof.source_sha -cne $source) { throw 'Fixture source changed' }
    $device = @(Get-CimInstance Win32_PnPEntity | Where-Object { $_.PNPDeviceID -eq $Proof.driver.instance_id })
    if ($device.Count -ne 1 -or $device[0].Service -ne 'VBAudioVACMME' -or $device[0].ConfigManagerErrorCode -ne 0 -or
        @($device[0].HardwareID) -notcontains 'VBAudioVACWDM') { throw 'Exact installed driver instance changed' }
    $image = DriverImage
    if ($image -cne $Proof.driver.image_path -or (Hash $image) -cne $driverHash -or
        (Hash $Proof.driver.inf_path) -cne $infHash -or (Hash $Proof.driver.catalog_path) -cne $catalogHash) { throw 'Signed driver input changed' }
    $signtool = NativeTool "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\arm64\signtool.exe"
    & $signtool verify /kp /q /c $Proof.driver.catalog_path $Proof.driver.inf_path $image | Out-Null
    if ($LASTEXITCODE) { throw 'Kernel signing policy or catalog membership verification failed' }
}
if ($VerifyOnly) {
    $proof = Get-Content -LiteralPath $Receipt -Raw | ConvertFrom-Json
    VerifyDriver $proof
    @{ verified=$true; source_sha=$proof.source_sha } | ConvertTo-Json -Compress
    exit 0
}

# This is the explicit owner lane for one disposable hosted image. Existing
# configured physical hosts skip it and keep their normal render loopback.
if ($env:PI_WINDOWS_HOST_RUNNER_ENVIRONMENT -ne 'github-hosted' -or $env:GITHUB_EVENT_NAME -ne 'workflow_dispatch' -or
    $env:GITHUB_RUN_ATTEMPT -ne '1' -or $env:GITHUB_REPOSITORY -ne '777genius/agent-notifications' -or
    $env:GITHUB_ACTOR -ne '777genius' -or $env:GITHUB_TRIGGERING_ACTOR -ne '777genius' -or
    [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -ne 'Arm64') { throw 'Fresh owner-dispatched hosted ARM64 TEST job required' }
$os = Get-CimInstance Win32_OperatingSystem
$admin = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($os.ProductType -ne 1 -or [int]$os.BuildNumber -lt 22000 -or !$admin -or (Test-Path -LiteralPath $Root)) { throw 'Fresh administrative Windows Client fixture root required' }
New-Item -ItemType Directory -Path $Root | Out-Null
$source = git -C $repositoryRoot rev-parse HEAD
if ($LASTEXITCODE -or $source -cne $env:SOURCE_SHA) { throw 'Exact source checkout required' }

# Reuse the already qualified, nonce-bound finite privacy OOBE flow; no process
# killing, registry policy edits, arbitrary key sequence or alternate flow.
npm ci --prefix opencode-plugin --ignore-scripts --no-audit --no-fund
if ($LASTEXITCODE) { throw 'Locked native setup tooling unavailable' }
node --experimental-strip-types --test tests/integration/navigation_windows_bootstrap_contract_test.ts
if ($LASTEXITCODE) { throw 'Guest setup contract failed' }
& cl.exe /nologo /std:c++20 /EHsc /W4 /DUNICODE /D_UNICODE tests/integration/navigation_windows_native_probe.cpp "/Fe:$Root/navigation-native-probe.exe" "/Fo:$Root/navigation-native-probe.obj" /link windowsapp.lib ole32.lib oleaut32.lib shell32.lib propsys.lib advapi32.lib user32.lib uuid.lib uiautomationcore.lib > "$Root/setup-compiler.log" 2>&1
if ($LASTEXITCODE) { throw 'Finite guest setup observer compile failed' }
& cl.exe /nologo /std:c++20 /EHsc /W4 /DUNICODE /D_UNICODE tests/integration/pi_windows_host.cpp "/Fe:$Root/host-observer.exe" "/Fo:$Root/host-observer.obj" /link ole32.lib oleaut32.lib user32.lib uuid.lib propsys.lib advapi32.lib cfgmgr32.lib > "$Root/host-compiler.log" 2>&1
if ($LASTEXITCODE) { throw 'Guest endpoint observer compile failed' }
$env:AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E = '1'
$env:NAVIGATION_WINDOWS_RUNNER = 'windows-11-vs2026-arm'
$env:NAVIGATION_SOURCE_SHA = $source
$env:NAVIGATION_WINDOWS_PREFLIGHT_ONLY = '1'
$env:NAVIGATION_WINDOWS_OOBE_SETUP_TEST = '1'
node --experimental-strip-types tests/integration/navigation_windows_native_e2e.ts "$Root/navigation-native-probe.exe" > "$Root/oobe.log" 2>&1
if ($LASTEXITCODE) { throw 'Finite OOBE setup failed or unknown; no driver or product effects' }
$env:PI_WINDOWS_HOST_SETUP = '1'
& "$Root/host-observer.exe" shell > "$Root/shell-overlay.json"
if ($LASTEXITCODE) { throw 'Single bound Search overlay dismissal failed or unknown' }

# The only driver setup is the measured, authenticated one-shot route. Refuse
# any pre-existing render device or VB instance, preserving configured hosts.
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class PiRenderInventory {
  [ComImport, Guid("A95664D2-9614-4F35-A746-DE8DB63617E6"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  private interface Enumerator { [PreserveSig] int EnumAudioEndpoints(int flow, uint states, out Collection devices); }
  [ComImport, Guid("0BD7A1BE-7A1A-44DB-8397-CC5392387B5E"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  private interface Collection { [PreserveSig] int GetCount(out uint count); }
  public static uint Count() {
    var enumerator=(Enumerator)Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("BCDE0395-E52F-467C-8E3D-C4579291692E")));
    Collection devices=null;
    try { uint count; Marshal.ThrowExceptionForHR(enumerator.EnumAudioEndpoints(0,15,out devices)); Marshal.ThrowExceptionForHR(devices.GetCount(out count)); return count; }
    finally { if(devices!=null) Marshal.FinalReleaseComObject(devices); Marshal.FinalReleaseComObject(enumerator); }
  }
}
'@
if ([PiRenderInventory]::Count() -ne 0 -or @(Get-CimInstance Win32_PnPEntity | Where-Object { $_.Service -eq 'VBAudioVACMME' }).Count) { throw 'Existing audio route preserved; fresh empty guest required' }
$zip = Join-Path $Root 'vendor.zip'
Invoke-WebRequest -Uri 'https://download.vb-audio.com/Download_CABLE/VBCABLE_Driver_Pack45.zip' -OutFile $zip -TimeoutSec 60 -MaximumRedirection 3
if ((Hash $zip) -cne 'b950e39f01af1d04ea623c8f6d8eb9b6ea5c477c637295fabf20631c85116bfb') { throw 'Vendor archive SHA256 mismatch' }
Expand-Archive -LiteralPath $zip -DestinationPath "$Root/vendor"
$inf = "$Root/vendor/vbMmeCable64_win10.inf"; $catalog = "$Root/vendor/vbaudio_cable64_win10.cat"; $binary = "$Root/vendor/vbaudio_cable64arm_win10.sys"
if ((Hash $inf) -cne $infHash -or (Hash $catalog) -cne $catalogHash -or (Hash $binary) -cne $driverHash) { throw 'Exact driver leaves changed' }
$kits = "${env:ProgramFiles(x86)}\Windows Kits\10"
$signtool = NativeTool "$kits\bin\*\arm64\signtool.exe"
& $signtool verify /kp /v /c $catalog $inf $binary > "$Root/catalog-verification.log" 2>&1
if ($LASTEXITCODE) { throw 'Normal kernel signing policy verification failed' }
$devcon = NativeTool "$kits\Tools\*\arm64\devcon.exe"
$install = Start-Process -FilePath $devcon -ArgumentList @('install', ('"' + $inf + '"'), 'VBAudioVACWDM') -WorkingDirectory "$Root/vendor" -NoNewWindow -PassThru -RedirectStandardOutput "$Root/install.stdout.log" -RedirectStandardError "$Root/install.stderr.log"
if (!$install.WaitForExit(90000)) { $install.Kill($true); [void]$install.WaitForExit(5000); throw 'Driver setup timed out; unknown, no retry or reboot' }
if ($install.ExitCode -ne 0) { throw 'Driver setup failed or needs reboot; no retry or reboot' }
$device = @(Get-CimInstance Win32_PnPEntity | Where-Object { $_.Service -eq 'VBAudioVACMME' })
$device | Select-Object PNPDeviceID,Service,ConfigManagerErrorCode,HardwareID | ConvertTo-Json -Depth 3 | Set-Content "$Root/driver-instance.json"
Get-CimInstance Win32_SystemDriver -Filter "Name='VBAudioVACMME'" | Select-Object Name,State,Started,PathName | ConvertTo-Json | Set-Content "$Root/driver-service.json"
if ($device.Count -ne 1 -or $device[0].ConfigManagerErrorCode -ne 0 -or @($device[0].HardwareID) -notcontains 'VBAudioVACWDM') { throw 'Unique installed virtual driver instance unavailable' }
$route = & "$Root/host-observer.exe" discover $device[0].PNPDeviceID
if ($LASTEXITCODE) { throw 'Exact driver-bound endpoint discovery failed; no microphone/default-capture fallback' }
$proof = [ordered]@{ schema_version=1; source_sha=$source; driver=@{ instance_id=$device[0].PNPDeviceID; image_path=(DriverImage); image_sha256=$driverHash; inf_path=$inf; catalog_path=$catalog }; route=($route | ConvertFrom-Json) }
VerifyDriver $proof
$proof | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath "$Root/audio-fixture.json" -Encoding utf8NoBOM
"audio_fixture=$Root/audio-fixture.json" >> $env:GITHUB_OUTPUT
