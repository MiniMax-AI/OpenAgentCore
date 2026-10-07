# Exercise the actual launcher with a native oac binary and local release assets.
param([Parameter(Mandatory=$true)][string]$Binary)
$ErrorActionPreference = 'Stop'
$testAssets = @{ Binary = $Binary; Corrupt = $false }
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
    if ($Uri.EndsWith('.sha256')) {
        $digest = (Get-FileHash -Algorithm SHA256 $testAssets.Binary).Hash.ToLowerInvariant()
        if ($testAssets.Corrupt) { $digest = '0' * 64 }
        [IO.File]::WriteAllText($OutFile, "$digest  oac-windows-amd64.exe`n")
    } else {
        Copy-Item $testAssets.Binary $OutFile
    }
}
& "$PSScriptRoot/install.ps1" --help

$testAssets.Corrupt = $true
$caught = $false
try { & "$PSScriptRoot/install.ps1" --help }
catch {
    if ($_.Exception.Message -notlike '*checksum mismatch*') { throw }
    $caught = $true
}
if (-not $caught) { throw 'A corrupt binary was executed.' }

$testAssets.Corrupt = $false
$caught = $false
try { & "$PSScriptRoot/install.ps1" --unknown-option }
catch {
    if ($_.Exception.Message -notlike '*Installation failed*') { throw }
    $caught = $true
}
if (-not $caught) { throw 'A failed native command was reported as successful.' }
exit 0
