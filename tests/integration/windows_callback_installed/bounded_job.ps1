param(
  [Parameter(Mandatory=$true)][string]$Executable,
  [Parameter(Mandatory=$true)][string]$ArgumentsJson,
  [Parameter(Mandatory=$true)][string]$StdoutPath,
  [Parameter(Mandatory=$true)][string]$StderrPath,
  [Parameter(Mandatory=$true)][int]$TimeoutMilliseconds,
  [Parameter(Mandatory=$true)][string]$ResultPath
)
$ErrorActionPreference = 'Stop'
if ($TimeoutMilliseconds -lt 1000 -or $TimeoutMilliseconds -gt 360000) { throw 'unsupported bounded timeout' }
if (!(Test-Path -LiteralPath $Executable -PathType Leaf)) { throw 'bounded executable missing' }
$arguments = [string[]](ConvertFrom-Json -InputObject (Get-Content -LiteralPath $ArgumentsJson -Raw))
if ($arguments.Count -gt 32) { throw 'bounded argument count' }
foreach ($argument in $arguments) { if ([string]$argument -match '[\u0000-\u001f]') { throw 'bounded control character' } }
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;

public static class BoundedJobRunner {
  [StructLayout(LayoutKind.Sequential)]
  private struct SECURITY_ATTRIBUTES {
    public int nLength; public IntPtr lpSecurityDescriptor; public int bInheritHandle;
  }
  [StructLayout(LayoutKind.Sequential)]
  private struct STARTUPINFO {
    public int cb; public string lpReserved, lpDesktop, lpTitle;
    public int dwX, dwY, dwXSize, dwYSize, dwXCountChars, dwYCountChars, dwFillAttribute, dwFlags;
    public short wShowWindow, cbReserved2; public IntPtr lpReserved2, hStdInput, hStdOutput, hStdError;
  }
  [StructLayout(LayoutKind.Sequential)]
  private struct PROCESS_INFORMATION {
    public IntPtr hProcess, hThread; public int dwProcessId, dwThreadId;
  }
  [StructLayout(LayoutKind.Sequential)]
  private struct IO_COUNTERS {
    public ulong ReadOperationCount, WriteOperationCount, OtherOperationCount;
    public ulong ReadTransferCount, WriteTransferCount, OtherTransferCount;
  }
  [StructLayout(LayoutKind.Sequential)]
  private struct LIMIT_INFORMATION {
    public long PerProcessUserTimeLimit, PerJobUserTimeLimit;
    public uint LimitFlags;
    public UIntPtr MinimumWorkingSetSize, MaximumWorkingSetSize;
    public uint ActiveProcessLimit;
    public UIntPtr Affinity;
    public uint PriorityClass, SchedulingClass;
  }
  [StructLayout(LayoutKind.Sequential)]
  private struct EXTENDED_LIMIT_INFORMATION {
    public LIMIT_INFORMATION BasicLimitInformation; public IO_COUNTERS IoInfo;
    public UIntPtr ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed;
  }
  [StructLayout(LayoutKind.Sequential)]
  private struct BASIC_ACCOUNTING {
    public long TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime;
    public uint TotalPageFaultCount, TotalProcesses, ActiveProcesses, TerminatedProcesses;
  }
  private const int CREATE_SUSPENDED = 0x00000004, CREATE_NO_WINDOW = 0x08000000;
  private const int STARTF_USESTDHANDLES = 0x00000100, HANDLE_FLAG_INHERIT = 1;
  private const int JobObjectExtendedLimitInformation = 9, JobObjectBasicAccountingInformation = 1;
  private const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000, JOB_OBJECT_LIMIT_ACTIVE_PROCESS = 0x8;
  private const uint INFINITE = 0xffffffff, WAIT_OBJECT_0 = 0, WAIT_TIMEOUT = 0x102;

  [DllImport("kernel32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  private static extern IntPtr CreateJobObjectW(IntPtr attributes, string name);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool SetInformationJobObject(IntPtr job, int type, IntPtr data, int size);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool QueryInformationJobObject(IntPtr job, int type, IntPtr data, int size, IntPtr returned);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool TerminateJobObject(IntPtr job, uint exitCode);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool GetExitCodeProcess(IntPtr process, out uint exitCode);
  [DllImport("kernel32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  private static extern IntPtr CreateFileW(string name, uint access, uint share, ref SECURITY_ATTRIBUTES security,
    int creation, int flags, IntPtr template);
  [DllImport("kernel32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
  private static extern bool CreateProcessW(string application, StringBuilder command, IntPtr processAttributes,
    IntPtr threadAttributes, bool inherit, int flags, IntPtr environment, string directory,
    ref STARTUPINFO startup, out PROCESS_INFORMATION process);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern uint ResumeThread(IntPtr thread);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool TerminateProcess(IntPtr process, uint exitCode);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool FlushFileBuffers(IntPtr file);
  [DllImport("kernel32.dll", SetLastError=true)]
  private static extern bool CloseHandle(IntPtr handle);

  private sealed class Safe {
    public IntPtr Value;
    public Safe(IntPtr value) { Value = value; }
    public bool Close() {
      if (Value == IntPtr.Zero || Value == new IntPtr(-1)) { Value = IntPtr.Zero; return true; }
      if (!CloseHandle(Value)) return false;
      Value = IntPtr.Zero; return true;
    }
    ~Safe() { Close(); }
  }

  private static string JsonString(string value) {
    var outValue = new StringBuilder("\"");
    foreach (char ch in value) {
      if (ch == '\\') outValue.Append("\\\\");
      else if (ch == '"') outValue.Append("\\\"");
      else if (ch == '\n') outValue.Append("\\n");
      else if (ch == '\r') outValue.Append("\\r");
      else if (ch == '\t') outValue.Append("\\t");
      else if (ch < 0x20) outValue.Append("\\u").Append(((int)ch).ToString("x4"));
      else outValue.Append(ch);
    }
    return outValue.Append('"').ToString();
  }

  private static void Property(StringBuilder result, string name, string value) {
    if (result.Length > 1) result.Append(',');
    result.Append('"').Append(name).Append("\":").Append(value);
  }

  private static string Quote(string value) {
    if (value.Length > 0 && value.IndexOfAny(new[]{' ','\t','"'}) < 0) return value;
    var outValue = new StringBuilder("\"");
    int slashes = 0;
    foreach (char ch in value) {
      if (ch == '\\') { slashes++; continue; }
      if (ch == '"') { outValue.Append('\\',slashes*2+1).Append('"'); slashes=0; continue; }
      outValue.Append('\\',slashes).Append(ch); slashes=0;
    }
    outValue.Append('\\',slashes*2).Append('"');
    return outValue.ToString();
  }

  private static bool WaitEmpty(IntPtr job, int milliseconds) {
    var clock = Stopwatch.StartNew();
    while (QueryActive(job) != 0) {
      if (clock.ElapsedMilliseconds >= milliseconds) return false;
      Thread.Sleep(20);
    }
    return true;
  }

  private static uint QueryActive(IntPtr job) {
    int size = Marshal.SizeOf<BASIC_ACCOUNTING>();
    IntPtr data = Marshal.AllocHGlobal(size);
    try {
      if (!QueryInformationJobObject(job, JobObjectBasicAccountingInformation, data, size, IntPtr.Zero))
        throw new Win32Exception(Marshal.GetLastWin32Error());
      return (uint)Marshal.PtrToStructure<BASIC_ACCOUNTING>(data).ActiveProcesses;
    } finally { Marshal.FreeHGlobal(data); }
  }

  private static bool ObserveEOF(string path) {
    using (var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read)) {
      var bytes = new byte[4096];
      int total = 0, count;
      while ((count = stream.Read(bytes, 0, bytes.Length)) != 0) {
        total += count;
        if (total > 65536) throw new IOException("bounded stream exceeds cap");
      }
      return stream.Position == stream.Length;
    }
  }

  public static string Run(string executable, string[] arguments, string stdoutPath, string stderrPath,
      int timeoutMilliseconds, out bool infrastructureComplete) {
    infrastructureComplete = false;
    var safe = new Safe[5];
    SECURITY_ATTRIBUTES security = new SECURITY_ATTRIBUTES { nLength = Marshal.SizeOf<SECURITY_ATTRIBUTES>(), bInheritHandle = 1 };
    IntPtr outputHandle = CreateFileW(stdoutPath, 0x40000000, 3, ref security, 1, 0x00200000, IntPtr.Zero);
    safe[0] = new Safe(outputHandle);
    IntPtr errorHandle = CreateFileW(stderrPath, 0x40000000, 3, ref security, 1, 0x00200000, IntPtr.Zero);
    safe[1] = new Safe(errorHandle);
    IntPtr inputHandle = CreateFileW("NUL", 0x80000000, 3, ref security, 3, 0, IntPtr.Zero);
    safe[2] = new Safe(inputHandle);
    IntPtr job = CreateJobObjectW(IntPtr.Zero, null);
    safe[3] = new Safe(job);
    PROCESS_INFORMATION process = new PROCESS_INFORMATION();
    Safe processSafe = null, threadSafe = null;
    bool assigned = false, overcap = false, timedOut = false, unknown = false, handlesClosed = true;
    string error = null;
    uint rootExit = 0; uint active = 0;
    IntPtr jobValue = job;
    var monitorStop = new ManualResetEventSlim(false);
    Thread monitor = new Thread(() => {
      while (!monitorStop.Wait(20)) {
        try {
          if (new FileInfo(stdoutPath).Length + new FileInfo(stderrPath).Length > 65536) {
            overcap = true; TerminateJobObject(jobValue, 160); return;
          }
        } catch { }
      }
    });
    monitor.IsBackground = true;
    try {
      if (outputHandle == IntPtr.Zero || outputHandle == new IntPtr(-1) ||
          errorHandle == IntPtr.Zero || errorHandle == new IntPtr(-1) ||
          inputHandle == IntPtr.Zero || inputHandle == new IntPtr(-1) || job == IntPtr.Zero)
        throw new Win32Exception(Marshal.GetLastWin32Error());
      if (IntPtr.Size != 8 || Marshal.SizeOf<LIMIT_INFORMATION>() != 64 || Marshal.SizeOf<EXTENDED_LIMIT_INFORMATION>() != 144)
        throw new InvalidOperationException("unexpected 64-bit Job Object ABI");
      int limitSize = Marshal.SizeOf<EXTENDED_LIMIT_INFORMATION>();
      IntPtr limitData = Marshal.AllocHGlobal(limitSize);
      try {
        EXTENDED_LIMIT_INFORMATION limit = new EXTENDED_LIMIT_INFORMATION();
        limit.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_ACTIVE_PROCESS;
        limit.BasicLimitInformation.ActiveProcessLimit = 32;
        Marshal.StructureToPtr(limit,limitData,false);
        if (!SetInformationJobObject(job,JobObjectExtendedLimitInformation,limitData,limitSize))
          throw new Win32Exception(Marshal.GetLastWin32Error());
      } finally { Marshal.FreeHGlobal(limitData); }

      var command = new StringBuilder(Quote(executable));
      foreach (string argument in arguments) command.Append(' ').Append(Quote(argument));
      STARTUPINFO startup = new STARTUPINFO { cb = Marshal.SizeOf<STARTUPINFO>(), dwFlags = STARTF_USESTDHANDLES,
        hStdInput = inputHandle, hStdOutput = outputHandle, hStdError = errorHandle };
      if (!CreateProcessW(executable,command,IntPtr.Zero,IntPtr.Zero,true,
          CREATE_SUSPENDED|CREATE_NO_WINDOW,IntPtr.Zero,Path.GetDirectoryName(Path.GetFullPath(executable)),
          ref startup,out process))
        throw new Win32Exception(Marshal.GetLastWin32Error());
      processSafe = new Safe(process.hProcess); threadSafe = new Safe(process.hThread);
      if (!AssignProcessToJobObject(job,process.hProcess)) throw new Win32Exception(Marshal.GetLastWin32Error());
      assigned = true;
      if (ResumeThread(process.hThread) == uint.MaxValue) throw new Win32Exception(Marshal.GetLastWin32Error());
      monitor.Start();
      uint waited = WaitForSingleObject(process.hProcess,(uint)timeoutMilliseconds);
      if (waited == WAIT_TIMEOUT) { timedOut = true; unknown = true; error = "bounded root deadline; process tree effect state unknown"; }
      else if (waited != WAIT_OBJECT_0) throw new Win32Exception(Marshal.GetLastWin32Error());
      if (timedOut || overcap) {
        if (!TerminateJobObject(job,overcap ? 161u : 159u)) throw new Win32Exception(Marshal.GetLastWin32Error());
      }
      if (WaitForSingleObject(process.hProcess,30000) != WAIT_OBJECT_0) { unknown = true; error = error ?? "bounded root process did not collect"; TerminateJobObject(job,162); }
      if (!WaitEmpty(job,30000)) {
        unknown = true; error = error ?? "bounded process tree did not collect";
        if (!TerminateJobObject(job,163)) throw new Win32Exception(Marshal.GetLastWin32Error());
        if (!WaitEmpty(job,30000)) error = "terminated process tree remains unknown";
      }
      if (!GetExitCodeProcess(process.hProcess,out rootExit)) throw new Win32Exception(Marshal.GetLastWin32Error());
      active = QueryActive(job);
      if (active != 0) { unknown = true; error = error ?? "bounded process tree remained active"; }
      if (overcap) { unknown = true; error = "bounded output cap; stream/effect state unknown"; }
      if (!timedOut && !overcap && active == 0 && rootExit == 0) error = null;
      infrastructureComplete = assigned && active == 0;
    } catch (Exception exception) {
      unknown = true; error = exception.GetType().Name+": "+exception.Message;
      try {
        if (process.hProcess != IntPtr.Zero) {
          if (assigned) TerminateJobObject(job,164);
          else TerminateProcess(process.hProcess,164);
          WaitForSingleObject(process.hProcess,30000);
        }
        if (assigned) { WaitEmpty(job,30000); active = QueryActive(job); }
      } catch { }
    } finally {
      monitorStop.Set();
      if (monitor.IsAlive && !monitor.Join(1000)) handlesClosed = false;
      if (assigned && active == 0) {
        if (!FlushFileBuffers(outputHandle) || !FlushFileBuffers(errorHandle)) handlesClosed = false;
      }
      for (int i = 0; i < safe.Length; ++i) if (safe[i] != null && !safe[i].Close()) handlesClosed = false;
      if (processSafe != null && !processSafe.Close()) handlesClosed = false;
      if (threadSafe != null && !threadSafe.Close()) handlesClosed = false;
    }
    if (overcap || !handlesClosed) {
      unknown = true; infrastructureComplete = false;
      error = overcap ? "bounded output cap; effects unknown" : "bounded handles did not close";
    }
    bool stdoutEOF = false, stderrEOF = false;
    if (assigned && active == 0 && handlesClosed && !overcap) {
      try { stdoutEOF = ObserveEOF(stdoutPath); stderrEOF = ObserveEOF(stderrPath); }
      catch (Exception exception) { unknown = true; infrastructureComplete = false; error = exception.Message; }
    }
    bool streamsCollected = stdoutEOF && stderrEOF;
    var result = new StringBuilder("{");
    Property(result,"executable",JsonString(executable));
    Property(result,"command","["+string.Join(",",Array.ConvertAll<string,string>(arguments,JsonString))+"]");
    Property(result,"rootPid",process.dwProcessId.ToString());
    Property(result,"rootExitCode",process.hProcess == IntPtr.Zero ? "null" : rootExit.ToString());
    Property(result,"jobObjectAssignedBeforeResume",assigned ? "true" : "false");
    Property(result,"processTreeContained",assigned ? "true" : "false");
    Property(result,"timedOut",timedOut ? "true" : "false");
    Property(result,"unknown",unknown ? "true" : "false");
    Property(result,"effectState",unknown ? "\"unknown\"" : "\"known_collected\"");
    Property(result,"retryAdmissible","false");
    Property(result,"activeProcessesAfter",active.ToString());
    Property(result,"streamsCollected",(streamsCollected ? "true" : "false"));
    Property(result,"stdoutEOF",(stdoutEOF ? "true" : "false"));
    Property(result,"stderrEOF",(stderrEOF ? "true" : "false"));
    Property(result,"error",error == null ? "null" : JsonString(error));
    result.Append('}');
    return result.ToString();
  }
}
'@
$absoluteExecutable = [IO.Path]::GetFullPath($Executable)
$argumentsJsonAbsolute = [IO.Path]::GetFullPath($ArgumentsJson)
$stdoutAbsolute = [IO.Path]::GetFullPath($StdoutPath)
$stderrAbsolute = [IO.Path]::GetFullPath($StderrPath)
$resultAbsolute = [IO.Path]::GetFullPath($ResultPath)
if (!(Test-Path -LiteralPath $absoluteExecutable -PathType Leaf)) { throw 'bounded executable disappeared' }
if ((Get-Item -LiteralPath $absoluteExecutable).Length -le 0) { throw 'bounded executable empty' }
$complete = $false
$json = [BoundedJobRunner]::Run($absoluteExecutable,[string[]]$arguments,$stdoutAbsolute,$stderrAbsolute,$TimeoutMilliseconds,[ref]$complete)
if (!(Test-Path -LiteralPath $stdoutAbsolute -PathType Leaf) -or !(Test-Path -LiteralPath $stderrAbsolute -PathType Leaf)) { throw 'bounded streams not materialized' }
[IO.File]::WriteAllText($resultAbsolute,$json,[Text.UTF8Encoding]::new($false))
if (!$complete) { exit 2 }
exit 0
