param(
  [Parameter(Mandatory=$true)][string]$Base,
  [Parameter(ValueFromRemainingArguments=$true)][string[]]$InstallArguments
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
# Only the installer receives the grant; every other program started below runs without it.
$grant = $env:OAC_INSTALL_GRANT
Remove-Item Env:OAC_INSTALL_GRANT -ErrorAction SilentlyContinue
$tar = Join-Path $env:SystemRoot 'System32\tar.exe'
if (!(Test-Path -LiteralPath $tar -PathType Leaf)) { throw 'Windows tar.exe is required. Install the Windows archive tools, then rerun this command.' }
$architecture = @{x64='amd64';arm64='arm64'}[[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()]
if (!$architecture) { throw 'Unsupported processor architecture.' }
$root = if ($env:OAC_RUNTIME_HOME) { $env:OAC_RUNTIME_HOME } else { Join-Path $HOME '.oac' }
if (![IO.Path]::IsPathRooted($root)) { throw 'OAC_RUNTIME_HOME must be an absolute directory.' }
function Get-NativeFile([string]$Uri, [string]$Destination) {
  for ($attempt=1; $attempt -le 3; $attempt++) {
    try { Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $Destination -ErrorAction Stop; return }
    catch {
      if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 404) { throw 'This Core has no qualified installer for this platform.' }
      if ($attempt -eq 3) { throw 'Installer download failed. Check the network to Core and GitHub (and the Windows proxy settings), then rerun the command.' }
      Write-Host "Download interrupted; retrying ($attempt/2)..."
      Start-Sleep -Seconds $attempt
    }
  }
}
# The unpacked installer lives here; the installer also keeps unfinished component downloads in this directory.
$cache = Join-Path ([IO.Path]::GetFullPath($root)) 'native-download'
$work = Join-Path $cache ('bundle.' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
try {
  [IO.Directory]::CreateDirectory($work) | Out-Null
  Write-Host 'Downloading the installer matched to Core...'
  Get-NativeFile "$Base/windows-$architecture.sha256" (Join-Path $work 'checksum')
  $expected = ([IO.File]::ReadAllText((Join-Path $work 'checksum'))).Trim()
  if ($expected -cnotmatch '^[0-9a-f]{64}$') { throw 'Core returned an invalid installer checksum.' }
  $archive = Join-Path $work 'installer.tar.gz'
  Get-NativeFile "$Base/windows-$architecture.tar.gz" $archive
  if ((Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant() -ne $expected) { throw 'Installer checksum mismatch; the download was discarded. Rerun the command to download it again.' }
  & $tar -xzf $archive -C $work
  if ($LASTEXITCODE -ne 0) { throw 'Installer extraction failed. Check disk space, quota and filesystem permissions.' }
  Remove-Item -LiteralPath $archive -Force
  $env:OAC_INSTALL_GRANT = $grant
  try { & (Join-Path $work 'oac-daemon.exe') install --onboard-url ($Base -replace '/install/[^/]+$', '/installation') @InstallArguments }
  finally { Remove-Item Env:OAC_INSTALL_GRANT -ErrorAction SilentlyContinue }
  if ($LASTEXITCODE -ne 0) { throw 'Installation or connection failed; follow the installer guidance and retry.' }
} finally {
  Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
