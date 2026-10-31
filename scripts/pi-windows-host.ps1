param([string]$Root, [string]$Receipt, [switch]$VerifyOnly, [switch]$InspectNotificationCenterOnly)
$ErrorActionPreference = 'Stop'
if ($VerifyOnly -and $InspectNotificationCenterOnly) { throw 'Inspection cannot be combined with driver verification' }
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
if ($env:PI_WINDOWS_HOST_RUNNER_LABEL -cne 'windows-11-vs2026-arm') { throw 'Public notification preparation requires the official hosted Client image' }
New-Item -ItemType Directory -Path $Root | Out-Null
$source = git -C $repositoryRoot rev-parse HEAD
if ($LASTEXITCODE -or $source -cne $env:SOURCE_SHA) { throw 'Exact source checkout required' }

# This disposable owner-only Client image has a broken inherited Windows
# Terminal startup (missing settings path and an unrelated WSL update tab).
# Prepare only Terminal's documented user settings before the first TEST tab;
# the actual Pi TUI, Windows notification delivery and foreground clicks stay
# native and unmodified. Never execute this on configured/non-hosted machines.
$terminalPackage = @(Get-AppxPackage -Name Microsoft.WindowsTerminal | Where-Object {
    $_.PackageFamilyName -ceq 'Microsoft.WindowsTerminal_8wekyb3d8bbwe'
})
if ($terminalPackage.Count -ne 1) { throw 'Official stable Windows Terminal package required for TEST Client' }
$terminalRoot = Join-Path $env:LOCALAPPDATA 'Packages\\Microsoft.WindowsTerminal_8wekyb3d8bbwe\\LocalState'
if (!(Test-Path -LiteralPath $terminalRoot -PathType Container)) {
    New-Item -ItemType Directory -Path $terminalRoot -Force | Out-Null
}
$terminalDir = Get-Item -LiteralPath $terminalRoot
if (!$terminalDir.PSIsContainer -or ($terminalDir.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Physical Windows Terminal settings directory required'
}
$terminalSettings = Join-Path $terminalRoot 'settings.json'
$priorHash = $null
if (Test-Path -LiteralPath $terminalSettings) {
    $original = Get-Item -LiteralPath $terminalSettings
    if ($original.PSIsContainer -or ($original.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
        $original.Length -gt 65536) { throw 'Bounded regular Windows Terminal user settings required' }
    $priorHash = Hash $terminalSettings
}
# Microsoft-supported settings: explicitly select PowerShell and never restore
# a WSL startup tab from a persisted window on this fresh TEST host.
$terminalConfig = [ordered]@{
    defaultProfile = 'Windows PowerShell'
    firstWindowPreference = 'defaultProfile'
    startupActions = ''
    disabledProfileSources = @('Windows.Terminal.Wsl')
}
$terminalBody = $terminalConfig | ConvertTo-Json -Depth 3
[IO.File]::WriteAllText($terminalSettings, $terminalBody + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
$readback = Get-Content -LiteralPath $terminalSettings -Raw | ConvertFrom-Json
if ($readback.defaultProfile -cne 'Windows PowerShell' -or
    $readback.firstWindowPreference -cne 'defaultProfile' -or
    $readback.startupActions -cne '' -or
    @($readback.disabledProfileSources).Count -ne 1 -or
    $readback.disabledProfileSources[0] -cne 'Windows.Terminal.Wsl') {
    throw 'Exact disposable Terminal startup settings readback failed'
}
@{ scope='fresh_owner_host_windows_terminal_settings'; source_sha=$source;
   previous_hash=$priorHash; prepared_hash=(Hash $terminalSettings);
   wsl_dynamic_profiles_disabled=$true; default_powershell_profile=$true;
   qualification_input=$false; product_executed=$false } |
    ConvertTo-Json -Compress | Set-Content -LiteralPath "$Root/windows-terminal-preparation.json" -Encoding utf8NoBOM

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

# Public notification preparation precedes driver/product/WinRT sender work.
# The separate inspection mode remains read-only and deliberately unqualified.
    $shellPaths = [ordered]@{
        explorer='explorer.exe'
        system32_shell_host='System32\ShellHost.exe'
        shell_experience_host='SystemApps\ShellExperienceHost_cw5n1h2txyewy\ShellExperienceHost.exe'
        cbs_shell_host='SystemApps\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\ShellHost.exe'
        cbs_search_host='SystemApps\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\SearchHost.exe'
    }
    $verifiedShells = @()
    foreach ($kind in $shellPaths.Keys) {
        $image = Join-Path $env:WINDIR $shellPaths[$kind]
        if (!(Test-Path -LiteralPath $image -PathType Leaf)) { continue }
        $signature = Get-AuthenticodeSignature -LiteralPath $image
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch '(?:^|,\s*)O=Microsoft Corporation(?:,|$)') { throw 'Shell image Microsoft signature unavailable' }
        $verifiedShells += @{ image_kind=$kind; image_sha256=(Hash $image); signature='valid_microsoft' }
    }
    if (@($verifiedShells | Where-Object { $_.image_kind -ceq 'explorer' }).Count -ne 1) { throw 'Signed Explorer admission unavailable' }
    @{ schema_version=1; scope=$(if ($InspectNotificationCenterOnly) { 'public_notification_ui_inspection_only' } else { 'fresh_owned_public_DND_preparation' }); source_sha=$source; product_executed=$false; qualification_input=$false;
       runner=$env:PI_WINDOWS_HOST_RUNNER_LABEL; runner_environment=$env:PI_WINDOWS_HOST_RUNNER_ENVIRONMENT; attempt=1;
       actor=$env:GITHUB_ACTOR; triggering_actor=$env:GITHUB_TRIGGERING_ACTOR; windows_build=[int]$os.BuildNumber;
       native_observer_sha256=(Hash "$Root/host-observer.exe"); signed_shell_images=$verifiedShells } |
        ConvertTo-Json -Depth 5 | Set-Content -LiteralPath "$Root/notification-ui-admission.json" -Encoding utf8NoBOM
    $notificationMode = if ($InspectNotificationCenterOnly) { 'notifications-inspect' } else { 'notifications-prepare' }
    $notificationScope = if ($InspectNotificationCenterOnly) { 'public_notification_ui_inspection_only' } else { 'fresh_owned_public_DND_preparation' }
    if ($InspectNotificationCenterOnly) { $env:PI_WINDOWS_NOTIFICATION_UI_INSPECTION = '1' }
    else { $env:PI_WINDOWS_NOTIFICATION_UI_PREPARATION = '1' }
    $preparationProof = $null
    $probe = $null; $primaryInspectionFailure = $null; $probeCleanupFailure = $null; $probeCollected = $false
    try {
        $probe = Start-Process -FilePath "$Root/host-observer.exe" -ArgumentList @($notificationMode, ('"' + $Root + '"')) -NoNewWindow -PassThru -RedirectStandardOutput "$Root/notification-ui.json" -RedirectStandardError "$Root/notification-ui.stderr.log"
        if (!$probe.WaitForExit(25000)) { throw 'Bounded public UI inspection timed out; outcome unknown, no retry' }
        if ($probe.ExitCode -ne 0) { throw 'Public UI inspection unavailable or incomplete; no product attempt' }
        foreach ($verified in $verifiedShells) {
            if ((Hash (Join-Path $env:WINDIR $shellPaths[$verified.image_kind])) -cne $verified.image_sha256) { throw 'Admitted Shell image changed during inspection' }
        }
        # Independent post-observation image evidence only. This does not admit
        # an actor, retry a census, authorize settings changes, or run a sender.
        $inventoryFile = Get-Item -LiteralPath "$Root/notification-ui.json"
        if ($inventoryFile.Length -gt 65536) { throw 'Observed UI inventory exceeds its bound' }
        $inventory = Get-Content -LiteralPath $inventoryFile.FullName -Raw | ConvertFrom-Json
        if (@($inventory.observed_providers).Count -gt 16) { throw 'Observed UI actor evidence exceeds its bound' }
        $observedImages = @()
        foreach ($actor in @($inventory.observed_providers)) {
            $imageEvidence = @{ provider_pid=$actor.provider_pid; provider_birth=$actor.provider_birth;
                diagnostic_only=$true; mutation_authorized=$false; image_root=$actor.canonical_image_root;
                canonical_windows_image=$null; image_sha256=$null; native_hash_matches=$false;
                authenticode_status='unavailable'; verified_microsoft=$false; query_error_class=$null }
            try {
                if (!$actor.canonical_windows_image) { throw 'Observed image was redacted or unavailable' }
                $image = [IO.Path]::GetFullPath([string]$actor.canonical_windows_image)
                if ($image.Length -gt 512 -or !($image.StartsWith((Join-Path $env:WINDIR 'System32\'), [StringComparison]::OrdinalIgnoreCase) -or
                    $image.StartsWith((Join-Path $env:WINDIR 'SystemApps\'), [StringComparison]::OrdinalIgnoreCase) -or
                    $image.Equals((Join-Path $env:WINDIR 'explorer.exe'), [StringComparison]::OrdinalIgnoreCase))) { throw 'Observed image outside protected roots' }
                $file = Get-Item -LiteralPath $image
                if (!$file.PSIsContainer -and $file.Length -gt 0 -and $file.Length -le 67108864) {
                    $imageEvidence.canonical_windows_image = $image
                    $beforeHash = Hash $image
                    $signature = Get-AuthenticodeSignature -LiteralPath $image
                    $afterHash = Hash $image
                    $imageEvidence.image_sha256 = $afterHash
                    $imageEvidence.native_hash_matches = $beforeHash -ceq $afterHash -and $afterHash -ceq $actor.image_sha256
                    $imageEvidence.authenticode_status = $signature.Status.ToString()
                    $imageEvidence.verified_microsoft = $imageEvidence.native_hash_matches -and $signature.Status -eq 'Valid' -and
                        $signature.SignerCertificate.Subject -match '(?:^|,\s*)O=Microsoft Corporation(?:,|$)'
                } else { throw 'Observed image file bound unavailable' }
            } catch { $imageEvidence.query_error_class = $_.Exception.GetType().Name }
            $observedImages += $imageEvidence
        }
        @{ scope='readonly_observed_windows_image_evidence'; diagnostic_only=$true; mutation_authorized=$false;
           product_executed=$false; qualification_input=$false; observed_images=$observedImages } |
            ConvertTo-Json -Depth 5 | Set-Content -LiteralPath "$Root/notification-ui-observed-images.json" -Encoding utf8NoBOM
        if (!$InspectNotificationCenterOnly) {
            $preparationFile = Get-Item -LiteralPath "$Root/notification-preparation.json"
            if ($preparationFile.Length -gt 65536) { throw 'Notification preparation receipt exceeds its bound' }
            $preparationProof = Get-Content -LiteralPath $preparationFile.FullName -Raw | ConvertFrom-Json
            $actorProof = @($observedImages | Where-Object { $_.provider_pid -eq $preparationProof.provider_pid -and
                $_.provider_birth -ceq $preparationProof.provider_birth -and $_.verified_microsoft -and
                $_.image_sha256 -ceq $preparationProof.image_sha256 })
            if ($preparationProof.scope -cne 'fresh_owned_public_DND_preparation' -or $preparationProof.product_executed -ne $false -or
                $preparationProof.native_show_attempts -ne 0 -or !$preparationProof.known_shell_experience_host -or
                $preparationProof.before_toggle_state -notin @(0,1) -or $preparationProof.after_toggle_state -ne 0 -or
                !$preparationProof.off_verified -or !$preparationProof.panel_closed_verified -or $preparationProof.owned_mouse_release_unknown -or
                !$preparationProof.before_screenshot -or !$preparationProof.after_screenshot -or !$preparationProof.closed_screenshot -or
                $preparationProof.close_key_events -ne 4 -or $preparationProof.primary_failure -or $preparationProof.cleanup_failure -or $actorProof.Count -ne 1 -or
                ($preparationProof.before_toggle_state -eq 1 -and (!$preparationProof.physical_click_attempted -or $preparationProof.accepted_mouse_events -ne 2)) -or
                ($preparationProof.before_toggle_state -eq 0 -and ($preparationProof.physical_click_attempted -or $preparationProof.accepted_mouse_events -ne 0))) {
                throw 'Exact owned public DND OFF preparation or panel closure is not proved'
            }
        }
    } catch { $primaryInspectionFailure = $_ }
    finally {
        if ($probe) {
            try {
                if (!$probe.HasExited) { $probe.Kill() } # This owned observer only.
                $probeCollected = $probe.WaitForExit(3000)
                if (!$probeCollected) { throw 'Owned public UI observer not collected' }
            } catch { $probeCleanupFailure = $_.Exception.GetType().Name }
            finally { try { $probe.Dispose() } catch { if (!$probeCleanupFailure) { $probeCleanupFailure = $_.Exception.GetType().Name } } }
        }
        try {
            @{ scope=$notificationScope; product_executed=$false; qualification_input=$false;
               owned_observer_collected=$probeCollected; cleanup_error_class=$probeCleanupFailure;
               settings_writes_observed=$(if ($InspectNotificationCenterOnly) { 0 } elseif ($preparationProof -and $preparationProof.off_verified) { $preparationProof.before_toggle_state } else { $null }) } |
                ConvertTo-Json -Compress | Set-Content -LiteralPath "$Root/notification-ui-collection.json" -Encoding utf8NoBOM
        } catch { if (!$probeCleanupFailure) { $probeCleanupFailure = $_.Exception.GetType().Name } }
    }
    if ($primaryInspectionFailure) { throw $primaryInspectionFailure }
    if (!$probeCollected -or $probeCleanupFailure) { throw 'Public UI observer cleanup unavailable' }
    if ($InspectNotificationCenterOnly) { throw 'Read-only notification UI inventory captured; reviewed actual selector required before product work' }

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
$route = & "$Root/host-observer.exe" discover $device[0].PNPDeviceID 2> "$Root/endpoint-discovery.stderr.log"
if ($LASTEXITCODE) { throw 'Exact driver-bound endpoint discovery failed; no microphone/default-capture fallback' }
$proof = [ordered]@{ schema_version=1; source_sha=$source; driver=@{ instance_id=$device[0].PNPDeviceID; image_path=(DriverImage); image_sha256=$driverHash; inf_path=$inf; catalog_path=$catalog }; route=($route | ConvertFrom-Json) }
$proof | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath "$Root/frozen-audio-fixture.json" -Encoding utf8NoBOM
VerifyDriver $proof
# Read-only negative admission: a changed opaque topology must refuse, then the
# exact frozen pair must reopen. Neither command renders, captures or sends input.
$atoms = @('--virtual-cable', $proof.route.driver_instance, $proof.route.render_endpoint_id, $proof.route.capture_endpoint_id, $proof.route.render_topology_id, $proof.route.capture_topology_id)
$invalid = $atoms.Clone(); $invalid[5] += '-TEST-invalid-topology'
& "$Root/host-observer.exe" verify @invalid 2> "$Root/binding-refusal.stderr.log" | Out-Null
$refusal = $LASTEXITCODE
if ($refusal -eq 0) { throw 'Changed capture topology unexpectedly admitted' }
$validated = & "$Root/host-observer.exe" verify @atoms 2> "$Root/binding-valid.stderr.log"
if ($LASTEXITCODE) { throw 'Exact frozen endpoint pair failed to reopen after refused admission' }
$validated = $validated | ConvertFrom-Json
foreach ($key in @('kind', 'driver_instance', 'render_endpoint_id', 'capture_endpoint_id', 'render_topology_id', 'capture_topology_id')) {
    if ($validated.$key -cne $proof.route.$key) { throw 'Reopened route does not match the frozen pair' }
}
@{ changed_topology_exit_code=$refusal; valid_route=$validated; product_executed=$false } | ConvertTo-Json -Depth 4 | Set-Content "$Root/binding-admission.json" -Encoding utf8NoBOM
$proof | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath "$Root/audio-fixture.json" -Encoding utf8NoBOM
"audio_fixture=$Root/audio-fixture.json" >> $env:GITHUB_OUTPUT
