# Exercise the actual launcher with a native oac binary and local release assets.
param([Parameter(Mandatory=$true)][string]$Binary)
$ErrorActionPreference = 'Stop'
$testAssets = @{ Binary = $Binary; Corrupt = $false; Status = 0 }
function Start-Sleep { param([int]$Seconds) }
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
    if ($testAssets.Status) {
        $failure = [Exception]::new('request failed')
        $failure | Add-Member -NotePropertyName Response -NotePropertyValue ([pscustomobject]@{ StatusCode = $testAssets.Status })
        throw $failure
    }
    if ($Uri.EndsWith('.sha256')) {
        $digest = (Get-FileHash -Algorithm SHA256 $testAssets.Binary).Hash.ToLowerInvariant()
        if ($testAssets.Corrupt) { $digest = '0' * 64 }
        [IO.File]::WriteAllText($OutFile, "$digest  oac-windows-amd64.exe`n")
    } else {
        Copy-Item $testAssets.Binary $OutFile
    }
}
# A failure writes one error and sets a non-zero exit code; the session stays open.
function Assert-Failure([string]$Pattern, [string[]]$Arguments = @('--help')) {
    $global:LASTEXITCODE = 0
    $output = (& "$PSScriptRoot/install.ps1" @Arguments 2>&1 | Out-String)
    if ($LASTEXITCODE -eq 0 -or $output -notlike $Pattern) { throw "Expected a failure matching $Pattern, got exit $LASTEXITCODE`: $output" }
}
& "$PSScriptRoot/install.ps1" --help
if ($LASTEXITCODE -ne 0) { throw 'The launcher failed on a valid installation.' }

$testAssets.Corrupt = $true
Assert-Failure '*checksum mismatch*'
$testAssets.Corrupt = $false
Assert-Failure '*Installation failed*' @('--unknown-option')
Assert-Failure '*Missing release tag*' @('--version')
$testAssets.Status = 404
Assert-Failure '*Release v9.9.9 not found*' @('--version=v9.9.9')
$testAssets.Status = 503
Assert-Failure '*Check the network*'
exit 0
