# Read-only binary proof: the linked Go executable contains the exact compiled
# helper bytes, rather than merely a generated source/digest declaration.
param([Parameter(Mandatory=$true)][string]$Executable,
      [Parameter(Mandatory=$true)][string]$Helper)
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System;
public static class EmbeddedCallbackProof {
  public static int Locate(byte[] haystack, byte[] needle) {
    if (needle.Length == 0) return -1;
    // KMP: bounded O(binary+helper) custody verification, no helper execution.
    var prefix = new int[needle.Length];
    for (int i=1, k=0; i<needle.Length; i++) {
      while (k>0 && needle[k]!=needle[i]) k=prefix[k-1];
      if (needle[k]==needle[i]) k++;
      prefix[i]=k;
    }
    for (int i=0, k=0; i<haystack.Length; i++) {
      while (k>0 && needle[k]!=haystack[i]) k=prefix[k-1];
      if (needle[k]==haystack[i]) k++;
      if (k==needle.Length) return i-k+1;
    }
    return -1;
  }
}
'@
$binary = [IO.File]::ReadAllBytes([IO.Path]::GetFullPath($Executable))
$asset = [IO.File]::ReadAllBytes([IO.Path]::GetFullPath($Helper))
$offset = [EmbeddedCallbackProof]::Locate($binary, $asset)
if ($offset -lt 0) { throw 'exact trusted helper bytes absent from linked Go executable' }
@{helperSHA256=(Get-FileHash -LiteralPath $Helper -Algorithm SHA256).Hash.ToLowerInvariant();
  binarySHA256=(Get-FileHash -LiteralPath $Executable -Algorithm SHA256).Hash.ToLowerInvariant();
  embeddedOffset=$offset; helperExecuted=$false; installedQualified=$false} | ConvertTo-Json
