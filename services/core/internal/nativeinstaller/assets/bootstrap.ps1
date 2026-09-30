param(
  [Parameter(Mandatory=$true)][string]$Base,
  [Parameter(Mandatory=$true)][string]$Authorization,
  [Parameter(ValueFromRemainingArguments=$true)][string[]]$InstallArguments
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
$architecture = @{x64='amd64';arm64='arm64'}[$architecture]
if (!$architecture) { throw 'Unsupported processor architecture.' }
$work = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $work | Out-Null
try {
  Write-Host 'Downloading the installer matched to Core...'
  try { $expected = (Invoke-WebRequest -UseBasicParsing "$Base/windows-$architecture.sha256").Content.Trim() }
  catch { throw 'This Core has no qualified installer for this platform.' }
  $archive = Join-Path $work 'bundle.tar.gz'
  Invoke-WebRequest -UseBasicParsing "$Base/windows-$architecture.tar.gz" -OutFile $archive
  Write-Host 'Verifying the installer archive...'
  if ((Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant() -ne $expected) { throw 'Installer checksum mismatch; download again.' }
  $bundle = Join-Path $work 'bundle'
  New-Item -ItemType Directory -Path $bundle | Out-Null
  Write-Host 'Extracting the installer...'
  & (Join-Path $env:SystemRoot 'System32\tar.exe') -xzf $archive -C $bundle
  if ($LASTEXITCODE -ne 0) { throw 'Installer extraction failed.' }
  $endpoint = $Base -replace '/install/[^/]+$', '/installation'
  Write-Host 'Starting installation...'
  & (Join-Path $bundle 'oac-daemon.exe') install --onboard-url $endpoint --authorization $Authorization @InstallArguments
  if ($LASTEXITCODE -ne 0) { throw 'Installation or connection failed; follow the installer guidance and retry.' }
} finally { Remove-Item -LiteralPath $work -Recurse -Force }
