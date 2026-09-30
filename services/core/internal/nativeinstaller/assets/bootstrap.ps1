param(
  [Parameter(Mandatory=$true)][string]$Base,
  [Parameter(Mandatory=$true)][string]$Authorization,
  [Parameter(ValueFromRemainingArguments=$true)][string[]]$InstallArguments
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Net.Http
if (!("OacNativeDownloadSpace" -as [type])) {
  Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
public static class OacNativeDownloadSpace {
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  static extern bool GetDiskFreeSpaceEx(string path, out ulong available, out ulong total, out ulong free);
  public static ulong Available(string path) {
    ulong available, total, free;
    if (!GetDiskFreeSpaceEx(path, out available, out total, out free)) throw new Win32Exception(Marshal.GetLastWin32Error());
    return available;
  }
}
'@
}
$architecture = @{x64='amd64';arm64='arm64'}[[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()]
if (!$architecture) { throw 'Unsupported processor architecture.' }
$root = if ($env:OAC_RUNTIME_HOME) { $env:OAC_RUNTIME_HOME } else { Join-Path $HOME '.oac' }
if (![IO.Path]::IsPathRooted($root)) { throw 'OAC_RUNTIME_HOME must be an absolute directory.' }
$cache = Join-Path ([IO.Path]::GetFullPath($root)) 'native-download'
$work = Join-Path $cache 'staging'
$lock = $null
$client = $null
$ownsStaging = $false
$terminal = ![Console]::IsOutputRedirected
$ProgressPreference = if ($terminal) { 'Continue' } else { 'SilentlyContinue' }
function Assert-OrdinaryPath([string]$Path) {
  if ((Test-Path -LiteralPath $Path) -and ((Get-Item -Force -LiteralPath $Path).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Native download paths must not be symbolic links or junctions.'
  }
}
function Assert-Space([long]$Required) {
  $available = [OacNativeDownloadSpace]::Available($cache)
  if ($available -lt ($Required + 64MB)) { throw 'Not enough disk space for the native installer. Free space in the Runtime home and retry.' }
}
function Show-Bytes([string]$Label, [long]$Bytes, [long]$Total) {
  if (!$terminal) { return }
  $percent = if ($Total -gt 0) { [Math]::Min(100, [int](100.0 * $Bytes / $Total)) } else { -1 }
  Write-Progress -Activity $Label -Status ("{0:N1} MiB" -f ($Bytes / 1MB)) -PercentComplete $percent
}
function Get-NativeFile([string]$Uri, [string]$Destination, [long]$Limit) {
  for ($attempt=1; $attempt -le 3; $attempt++) {
    $response = $null; $inputStream = $null; $outputStream = $null; $retryable = $true
    try {
      $address = [Uri]$Uri
      for ($redirect=0; $redirect -le 5; $redirect++) {
        $response = $client.GetAsync($address, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
        $status = [int]$response.StatusCode
        if ($status -notin @(301,302,303,307,308)) { break }
        if ($redirect -eq 5 -or !$response.Headers.Location) { $retryable=$false; throw 'Installer download has too many or invalid redirects.' }
        $next = [Uri]::new($address, $response.Headers.Location)
        if ($next.Scheme -ne 'https' -or $next.UserInfo) { $retryable=$false; throw 'Installer download redirect must use HTTPS without credentials.' }
        $response.Dispose(); $response=$null; $address=$next
      }
      if ($status -ne 200) {
        $retryable = $status -in @(408,429,500,502,503,504)
        if ($status -eq 404) { throw 'This Core has no qualified installer for this platform.' }
        throw "Installer download failed (HTTP $status). Check Core and the download host."
      }
      $total = $response.Content.Headers.ContentLength
      if ($null -eq $total) { $total = 0 }
      $retryable=$false
      if ($total -gt $Limit) { throw 'Installer download exceeds the available space or metadata size limit.' }
      Assert-Space $total
      $outputStream = [IO.File]::Open($Destination, [IO.FileMode]::Create, [IO.FileAccess]::Write, [IO.FileShare]::None)
      $retryable=$true
      $inputStream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
      $buffer = New-Object byte[] 65536
      [long]$received = 0
      $clock = [Diagnostics.Stopwatch]::StartNew()
      [long]$lastProgress=0
      while ($true) {
        if ($clock.Elapsed.TotalSeconds -gt 1200) { throw 'Installer download timed out; retry with a fresh command if it expired.' }
        $cancel = [Threading.CancellationTokenSource]::new()
        try {
          $cancel.CancelAfter(60000)
          $pending = $inputStream.ReadAsync($buffer, 0, $buffer.Length, $cancel.Token)
          if (!$pending.Wait(60000)) { throw 'Installer download stalled; retry when the connection is available.' }
          $read = $pending.GetAwaiter().GetResult()
        } finally { $cancel.Dispose() }
        if ($read -eq 0) { break }
        $received += $read
        $retryable=$false
        if ($received -gt $Limit) { throw 'Installer download exceeds the available space or metadata size limit.' }
        $outputStream.Write($buffer,0,$read)
        $retryable=$true
        if ($clock.ElapsedMilliseconds - $lastProgress -ge 200) { Show-Bytes 'Downloading installer' $received $total; $lastProgress=$clock.ElapsedMilliseconds }
      }
      if ($total -gt 0 -and $received -ne $total) { throw 'Installer download was interrupted.' }
      $retryable=$false
      $outputStream.Flush($true)
      return
    } catch {
      if (!$retryable) { throw }
      if ($attempt -eq 3) { throw 'Installer download failed after three attempts. Check network, proxy and certificates, then retry.' }
      Write-Host "Download interrupted; retrying ($attempt/2)..."
      Start-Sleep -Seconds $attempt
    } finally {
      if ($inputStream) { $inputStream.Dispose() }
      if ($outputStream) { $outputStream.Dispose() }
      if ($response) { $response.Dispose() }
      if ($terminal) { Write-Progress -Activity 'Downloading installer' -Completed }
    }
  }
}
try {
  Assert-OrdinaryPath $cache
  [IO.Directory]::CreateDirectory($cache) | Out-Null
  $lockPath = Join-Path $cache 'download.lock'
  Assert-OrdinaryPath $lockPath
  try { $lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) }
  catch { throw 'Cannot lock native downloads. Another download may be running; check permissions or wait and retry.' }
  Assert-OrdinaryPath $work
  if (Test-Path -LiteralPath $work) {
    # A killed PowerShell host can leave its native child alive. Do not remove its source bundle.
    $children = Get-CimInstance Win32_Process -Filter "Name = 'tar.exe' OR Name = 'oac-daemon.exe'"
    foreach ($child in $children) {
      if (($child.ExecutablePath -and $child.ExecutablePath.StartsWith($work, [StringComparison]::OrdinalIgnoreCase)) -or ($child.CommandLine -and $child.CommandLine.IndexOf($work, [StringComparison]::OrdinalIgnoreCase) -ge 0)) {
        throw 'A native installer is still using the download directory; wait for it to finish and retry.'
      }
    }
    Remove-Item -LiteralPath $work -Recurse -Force
  }
  [IO.Directory]::CreateDirectory($work) | Out-Null
  $ownsStaging = $true
  $handler = [Net.Http.HttpClientHandler]::new()
  $handler.AllowAutoRedirect = $false
  $client = [Net.Http.HttpClient]::new($handler)
  $client.Timeout = [TimeSpan]::FromSeconds(15)
  Assert-Space 0
  Write-Host 'Downloading the installer matched to Core...'
  $checksum = Join-Path $work 'checksum'
  Get-NativeFile "$Base/windows-$architecture.sha256" $checksum 1024
  $expected = [IO.File]::ReadAllText($checksum).Trim()
  if ($expected -cnotmatch '^[0-9a-f]{64}$') { throw 'Core returned an invalid installer checksum.' }
  $archive = Join-Path $work 'bundle.tar.gz'
  $available = [OacNativeDownloadSpace]::Available($cache)
  Get-NativeFile "$Base/windows-$architecture.tar.gz" $archive ($available-64MB)
  Write-Host 'Verifying the installer archive...'
  if ((Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant() -ne $expected) { throw 'Installer checksum mismatch; download again.' }
  Write-Host 'Checking extraction space...'
  $file = [IO.File]::OpenRead($archive)
  $gzip = [IO.Compression.GZipStream]::new($file,[IO.Compression.CompressionMode]::Decompress)
  try {
    $buffer = New-Object byte[] 65536
    [long]$unpacked=0
    while (($count=$gzip.Read($buffer,0,$buffer.Length)) -gt 0) { $unpacked += $count }
    Assert-Space $unpacked
  } finally { $gzip.Dispose(); $file.Dispose() }
  $bundle = Join-Path $work 'bundle'
  [IO.Directory]::CreateDirectory($bundle) | Out-Null
  Write-Host 'Extracting the installer...'
  & (Join-Path $env:SystemRoot 'System32\tar.exe') -xzf $archive -C $bundle
  if ($LASTEXITCODE -ne 0) { throw 'Installer extraction failed. Check disk space, quota and filesystem permissions.' }
  $endpoint = $Base -replace '/install/[^/]+$', '/installation'
  Write-Host 'Starting installation...'
  & (Join-Path $bundle 'oac-daemon.exe') install --onboard-url $endpoint --authorization $Authorization @InstallArguments
  if ($LASTEXITCODE -ne 0) { throw 'Installation or connection failed; follow the installer guidance and retry.' }
} finally {
  if ($client) { $client.Dispose() }
  if ($lock) {
    # Only clean staging created by this invocation, after its native child returned.
    try { if ($ownsStaging -and (Test-Path -LiteralPath $work)) { Remove-Item -LiteralPath $work -Recurse -Force } }
    finally { $lock.Dispose() }
  }
}
