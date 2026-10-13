# The same native oac command owns installation on Windows.
$ErrorActionPreference = 'Stop'
$installArgs = $args
$version = 'latest'
for ($i = 0; $i -lt $installArgs.Count; $i++) {
    if ($installArgs[$i] -eq '--version') {
        if ($i + 1 -ge $installArgs.Count) { throw 'Missing release tag' }
        $version = $installArgs[$i + 1]
    }
}
$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($architecture -ne 'x64') { throw 'Windows x64 is required.' }
$repository = $env:OAC_REPOSITORY
if (-not $repository) { $repository = 'MiniMax-AI/OpenAgentCore' }
$base = "https://github.com/$repository/releases/latest/download"
if ($version -ne 'latest') { $base = "https://github.com/$repository/releases/download/$version" }
$directory = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $directory | Out-Null
try {
    $asset = 'oac-windows-amd64.exe'
    foreach ($name in @($asset, "$asset.sha256")) {
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile (Join-Path $directory $name)
    }
    $binary = Join-Path $directory $asset
    $actual = (Get-FileHash -Algorithm SHA256 $binary).Hash.ToLowerInvariant() + "  $asset"
    if ($actual -cne (Get-Content -Raw (Join-Path $directory "$asset.sha256")).Trim()) { throw 'Installer checksum mismatch.' }
    & $binary install @installArgs
    if ($LASTEXITCODE -ne 0) { throw "Installation failed (exit $LASTEXITCODE). Fix the reported error and rerun." }
} finally {
    Remove-Item -Recurse -Force $directory
}
