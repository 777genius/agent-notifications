param([Parameter(Mandatory=$true)][string]$Supervisor)
$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ('TEST-bounded-job-' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
function Invoke-Case([string]$Name,[string]$Executable,[string[]]$ChildArguments,[int]$Deadline) {
  $arguments = Join-Path $root ($Name + '.args.json')
  $result = Join-Path $root ($Name + '.result.json')
  [IO.File]::WriteAllText($arguments,(ConvertTo-Json -InputObject $ChildArguments -Compress),[Text.UTF8Encoding]::new($false))
  & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $Supervisor -Executable $Executable -ArgumentsJson $arguments -StdoutPath (Join-Path $root ($Name+'.stdout')) -StderrPath (Join-Path $root ($Name+'.stderr')) -ResultPath $result -TimeoutMilliseconds $Deadline
  if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 2) { throw "$Name supervisor failed" }
  $facts = ConvertFrom-Json -InputObject (Get-Content -LiteralPath $result -Raw)
  if (!$facts.jobObjectAssignedBeforeResume -or !$facts.processTreeContained -or $facts.rootPid -le 0 -or $facts.activeProcessesAfter -ne 0 -or !$facts.streamsCollected -or !$facts.stdoutEOF -or !$facts.stderrEOF -or $facts.retryAdmissible) { throw "$Name did not collect its own tree" }
  return $facts
}
$passed = $false
try {
  $cmd = Join-Path $env:SystemRoot 'System32\cmd.exe'
  $powerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
  $success = Invoke-Case 'success' $cmd @('/d','/c','exit /b 0') 30000
  if ($success.unknown -or $success.timedOut -or $success.rootExitCode -ne 0 -or $success.error) { throw 'successful actor was not collected' }
  # A root that exits before its child must not grant collection prematurely.
  $descendant = Invoke-Case 'descendant' $powerShell @('-NoProfile','-NonInteractive','-Command',"Start-Process -FilePath '$cmd' -ArgumentList '/d /c ping -n 3 127.0.0.1 > nul' -WindowStyle Hidden | Out-Null") 30000
  if ($descendant.unknown -or $descendant.timedOut -or $descendant.rootExitCode -ne 0 -or $descendant.error) { throw 'descendant collection failed' }
  $timeout = Invoke-Case 'timeout' $cmd @('/d','/c','ping -n 60 127.0.0.1 > nul') 1000
  if (!$timeout.unknown -or !$timeout.timedOut -or $timeout.effectState -ne 'unknown' -or !$timeout.error) { throw 'deadline granted a known effect or replay' }
  $passed = $true
  Write-Output 'TEST Job Object ABI, root, descendant and deadline collection passed; no SDK/package/notification effects'
} finally {
  if ($passed) { Remove-Item -LiteralPath $root -Recurse -Force }
  else { Write-Output "Retained own TEST evidence: $root" }
}
