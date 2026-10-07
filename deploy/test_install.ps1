# Exercise the actual launcher with a native oac binary and local release assets.
param([Parameter(Mandatory=$true)][string]$Binary)
$ErrorActionPreference = 'Stop'
$script:corrupt = $false
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
    if ($Uri.EndsWith('.sha256')) {
        $digest = (Get-FileHash -Algorithm SHA256 $Binary).Hash.ToLowerInvariant()
        if ($script:corrupt) { $digest = '0' * 64 }
        [IO.File]::WriteAllText($OutFile, "$digest  oac-windows-amd64.exe`n")
    } else {
        Copy-Item $Binary $OutFile
    }
}
& "$PSScriptRoot/install.ps1" --help

$script:corrupt = $true
$caught = $false
try { & "$PSScriptRoot/install.ps1" --help }
catch {
    if ($_.Exception.Message -notlike '*checksum mismatch*') { throw }
    $caught = $true
}
if (-not $caught) { throw 'A corrupt binary was executed.' }

$script:corrupt = $false
$caught = $false
try { & "$PSScriptRoot/install.ps1" --unknown-option }
catch {
    if ($_.Exception.Message -notlike '*Installation failed*') { throw }
    $caught = $true
}
if (-not $caught) { throw 'A failed native command was reported as successful.' }
