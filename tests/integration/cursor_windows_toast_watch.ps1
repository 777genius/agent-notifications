$ErrorActionPreference = 'Stop'
$root = $env:CURSOR_TEST_ROOT
$primary = [IO.Path]::GetFullPath((Join-Path $root 'home/.local/bin/claude-notifications-windows-amd64.exe'))
$rows = @()
$owned = @{}
$registrations = @()
'starting' | Set-Content (Join-Path $root 'toast-watch-startup')
try {
    $registrations += Register-CimIndicationEvent -Query "SELECT * FROM Win32_ProcessStartTrace WHERE ProcessName='powershell.exe'" -SourceIdentifier 'TEST-Cursor-Start'
    'start subscription registered' | Add-Content (Join-Path $root 'toast-watch-startup')
    $registrations += Register-CimIndicationEvent -Query "SELECT * FROM Win32_ProcessStopTrace WHERE ProcessName='powershell.exe'" -SourceIdentifier 'TEST-Cursor-Stop'
    'ready' | Set-Content (Join-Path $root 'toast-watch-ready')
    $deadline = [DateTime]::UtcNow.AddSeconds(180)
    while ([DateTime]::UtcNow -lt $deadline -and !(Test-Path (Join-Path $root 'toast-watch-done'))) {
        foreach ($event in @(Get-Event)) {
            $value = $event.SourceEventArgs.NewEvent
            $id = [string]$value.ProcessID
            if ($event.SourceIdentifier -eq 'TEST-Cursor-Start') {
                # Query command first: the child can exit while querying its parent.
                $child = Get-CimInstance Win32_Process -Filter "ProcessId=$id" -ErrorAction SilentlyContinue
                $parent = Get-CimInstance Win32_Process -Filter "ProcessId=$($value.ParentProcessID)" -ErrorAction SilentlyContinue
                if ($parent.ExecutablePath -eq $primary) {
                    if (!$child) { throw 'Owned PowerShell command unavailable' }
                    # Shortcut readiness also uses owned PowerShell children.
                    if (!$child.CommandLine.Contains('ToastNotificationManager') -or !$child.CommandLine.Contains('.Show($toast)')) {
                        Remove-Event -EventIdentifier $event.EventIdentifier
                        continue
                    }
                    $owned[$id] = $true
                    $rows += @{ kind='start'; pid=$value.ProcessID; parent=$value.ParentProcessID; command=$child.CommandLine }
                }
            } elseif ($event.SourceIdentifier -eq 'TEST-Cursor-Stop' -and $owned.ContainsKey($id)) {
                $rows += @{ kind='stop'; pid=$value.ProcessID; exit=$value.ExitStatus }
            }
            Remove-Event -EventIdentifier $event.EventIdentifier
        }
        Start-Sleep -Milliseconds 25
    }
    if (!(Test-Path (Join-Path $root 'toast-watch-done'))) { throw 'TEST watcher deadline' }
    ConvertTo-Json -InputObject @($rows) -Depth 5 | Set-Content (Join-Path $root 'toast-processes.json') -Encoding UTF8
} finally {
    foreach ($registration in $registrations) {
        Unregister-Event -SourceIdentifier $registration.Name -ErrorAction SilentlyContinue
        Remove-Job -Job $registration -Force -ErrorAction SilentlyContinue
    }
}
