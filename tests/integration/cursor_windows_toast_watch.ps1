param([switch]$RestoreOnly)
$ErrorActionPreference = 'Stop'
$root = $env:CURSOR_TEST_ROOT
if ($env:GITHUB_ACTIONS -ne 'true' -or !(Split-Path $root -Leaf).StartsWith('TEST-cursor-windows-')) { throw 'Disposable Windows CI TEST root required' }
$primary = [IO.Path]::GetFullPath((Join-Path $root 'home/.local/bin/claude-notifications-windows-amd64.exe'))
$backup = Join-Path $root 'TEST-audit-policy.csv'
$originalPath = Join-Path $root 'TEST-audit-original.json'
$registry = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\System\Audit'
$valueName = 'ProcessCreationIncludeCmdLine_Enabled'
function Restore-TestAudit {
    if (!(Test-Path $originalPath)) { return }
    $original = Get-Content $originalPath -Raw | ConvertFrom-Json
    & auditpol.exe /restore "/file:$backup" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'TEST audit policy restore failed' }
    if ($original.hadValue) {
        New-ItemProperty $registry -Name $valueName -PropertyType DWord -Value $original.value -Force | Out-Null
    } else {
        Remove-ItemProperty $registry -Name $valueName -ErrorAction SilentlyContinue
    }
    if (!$original.hadKey -and (Test-Path $registry)) {
        $key = Get-Item $registry
        if ($key.Property.Count -eq 0 -and @(Get-ChildItem $registry).Count -eq 0) { Remove-Item $registry }
    }
    'restored' | Set-Content (Join-Path $root 'TEST-audit-restored')
}
if ($RestoreOnly) { Restore-TestAudit; exit 0 }
# Windows durable audit events retain command and exit status after child exit.
# This replaces timing-sensitive WMI queries; only owned toast rows are exported.
'starting' | Set-Content (Join-Path $root 'toast-watch-startup')
$hadKey = Test-Path $registry
$prior = Get-ItemProperty $registry -Name $valueName -ErrorAction SilentlyContinue
$hadValue = $null -ne $prior
if ($hadValue -and (Get-Item $registry).GetValueKind($valueName) -ne 'DWord') { throw 'Unexpected original audit value type' }
& auditpol.exe /backup "/file:$backup" | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'TEST audit policy backup failed' }
@{hadKey=$hadKey;hadValue=$hadValue;value=$(if($hadValue){$prior.$valueName}else{0})} | ConvertTo-Json | Set-Content $originalPath
try {
    New-Item $registry -Force | Out-Null
    New-ItemProperty $registry -Name $valueName -PropertyType DWord -Value 1 -Force | Out-Null
    foreach ($subcategory in @('Process Creation','Process Termination')) {
        & auditpol.exe /set "/subcategory:$subcategory" /success:enable | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'TEST process audit enable failed' }
    }
    $fence = (Get-WinEvent -LogName Security -MaxEvents 1).RecordId
    'ready' | Set-Content (Join-Path $root 'toast-watch-ready')
    $deadline = [DateTime]::UtcNow.AddSeconds(180)
    while ([DateTime]::UtcNow -lt $deadline -and !(Test-Path (Join-Path $root 'toast-watch-done'))) { Start-Sleep -Milliseconds 25 }
    if (!(Test-Path (Join-Path $root 'toast-watch-done'))) { throw 'TEST watcher deadline' }
    $filter = "*[System[(EventID=4688 or EventID=4689) and EventRecordID > $fence]]"
    $events = @(Get-WinEvent -LogName Security -FilterXPath $filter -MaxEvents 2000)
    if ($events.Count -ge 2000) { throw 'TEST process audit overflow' }
    $rows = @(); $owned = @{}
    foreach ($event in @($events | Sort-Object RecordId)) {
        $xml = [xml]$event.ToXml(); $fields = @{}
        foreach ($data in $xml.Event.EventData.Data) { $fields[$data.Name] = $data.InnerText }
        if ($event.Id -eq 4688 -and $fields['ParentProcessName'] -eq $primary -and (Split-Path $fields['NewProcessName'] -Leaf) -eq 'powershell.exe') {
            $command = $fields['CommandLine']
            if (!$command -or !$command.Contains('ToastNotificationManager') -or !$command.Contains('.Show($toast)')) { throw 'Owned toast command unavailable or changed' }
            $id = [Convert]::ToUInt32($fields['NewProcessId'].Substring(2),16)
            if ($owned.ContainsKey($id)) { throw 'Ambiguous owned process identity' }
            $owned[$id] = $true
            $rows += @{kind='start';pid=$id;parent=[Convert]::ToUInt32($fields['ProcessId'].Substring(2),16);command=$command;record=$event.RecordId}
        } elseif ($event.Id -eq 4689) {
            $id = [Convert]::ToUInt32($fields['ProcessId'].Substring(2),16)
            if ($owned.ContainsKey($id)) {
                $rows += @{kind='stop';pid=$id;exit=[Convert]::ToUInt32($fields['Status'].Substring(2),16);record=$event.RecordId}
            }
        }
    }
    ConvertTo-Json -InputObject @($rows) -Depth 5 | Set-Content (Join-Path $root 'toast-processes.json') -Encoding UTF8
} finally { Restore-TestAudit }
