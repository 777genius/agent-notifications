$ErrorActionPreference = 'Stop'
$root = $env:CURSOR_TEST_ROOT
$primary = [IO.Path]::GetFullPath((Join-Path $root 'home/.local/bin/claude-notifications-windows-amd64.exe'))
'starting' | Set-Content (Join-Path $root 'toast-watch-startup')
# Handle local WMI events directly. PowerShell's queued event jobs can delay the
# command-line query until the short-lived OS submission process has exited.
Add-Type -ReferencedAssemblies @('System.dll','System.Core.dll','System.Management.dll') -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Management;
public sealed class CursorToastWatch : IDisposable {
    readonly object sync = new object();
    readonly string primary;
    readonly Dictionary<uint, Dictionary<string,object>> starts = new Dictionary<uint, Dictionary<string,object>>();
    readonly Dictionary<uint, uint> stops = new Dictionary<uint, uint>();
    readonly List<string> errors = new List<string>();
    readonly ManagementEventWatcher start, stop;
    static ManagementObject Read(uint id) {
        var p = new ManagementObject("\\\\.\\root\\cimv2:Win32_Process.Handle='" + id + "'");
        try { p.Get(); return p; } catch { p.Dispose(); return null; }
    }
    public CursorToastWatch(string expected, uint self) {
        primary = expected;
        using(var p = Read(self)) { if(p == null) throw new Exception("Local WMI unavailable"); }
        start = new ManagementEventWatcher("SELECT * FROM Win32_ProcessStartTrace WHERE ProcessName='powershell.exe'");
        stop = new ManagementEventWatcher("SELECT * FROM Win32_ProcessStopTrace WHERE ProcessName='powershell.exe'");
        start.EventArrived += Started; stop.EventArrived += Stopped;
        start.Start();
        try { stop.Start(); } catch { start.Dispose(); stop.Dispose(); throw; }
    }
    void Started(object sender, EventArrivedEventArgs e) {
        lock(sync) {
            uint pid = Convert.ToUInt32(e.NewEvent["ProcessID"]);
            uint parentID = Convert.ToUInt32(e.NewEvent["ParentProcessID"]);
            using(var child = Read(pid)) using(var parent = Read(parentID)) {
                if(parent == null || !String.Equals(Convert.ToString(parent["ExecutablePath"]),primary,StringComparison.OrdinalIgnoreCase)) return;
                if(child == null) { errors.Add("Owned command unavailable: " + pid); return; }
                string command = Convert.ToString(child["CommandLine"]);
                if(!command.Contains("ToastNotificationManager") || !command.Contains(".Show($toast)")) {
                    errors.Add("Owned toast command changed: " + pid); return;
                }
                starts[pid] = new Dictionary<string,object>{{"kind","start"},{"pid",pid},{"parent",parentID},{"command",command}};
            }
        }
    }
    void Stopped(object sender, EventArrivedEventArgs e) {
        lock(sync) { stops[Convert.ToUInt32(e.NewEvent["ProcessID"])] = Convert.ToUInt32(e.NewEvent["ExitStatus"]); }
    }
    public object[] Rows() {
        lock(sync) {
            if(errors.Count != 0) throw new Exception(String.Join("; ",errors.ToArray()));
            var rows = new List<object>();
            foreach(var pair in starts) {
                rows.Add(pair.Value);
                uint exit;
                if(stops.TryGetValue(pair.Key,out exit)) rows.Add(new Dictionary<string,object>{{"kind","stop"},{"pid",pair.Key},{"exit",exit}});
            }
            return rows.ToArray();
        }
    }
    public void Dispose() { start.Stop(); stop.Stop(); start.Dispose(); stop.Dispose(); }
}
'@
$watch = New-Object CursorToastWatch($primary, [uint32]$PID)
try {
    'ready' | Set-Content (Join-Path $root 'toast-watch-ready')
    $deadline = [DateTime]::UtcNow.AddSeconds(180)
    while ([DateTime]::UtcNow -lt $deadline -and !(Test-Path (Join-Path $root 'toast-watch-done'))) { Start-Sleep -Milliseconds 25 }
    if (!(Test-Path (Join-Path $root 'toast-watch-done'))) { throw 'TEST watcher deadline' }
    ConvertTo-Json -InputObject @($watch.Rows()) -Depth 5 | Set-Content (Join-Path $root 'toast-processes.json') -Encoding UTF8
} finally { $watch.Dispose() }
