# The same native oac command owns installation on Windows.
# The body runs in its own scope so `& ([scriptblock]::Create((irm <url>))) <flags>` works; a failure sets the exit code and exits only when run as a file, never closing an interactive session.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    $failure = $null
    $directory = $null
    $version = 'latest'
    function Get-Release([string]$name) {
        for ($attempt = 1; $attempt -le 5; $attempt++) {
            try { Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile (Join-Path $directory $name); return }
            catch {
                if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 404) { throw "Release $version not found." }
                Start-Sleep -Seconds 3
            }
        }
        throw "Could not download $name from GitHub. Check the network (set HTTPS_PROXY if a proxy is required) and rerun the installer."
    }
    try {
        for ($i = 0; $i -lt $args.Count; $i++) {
            if ($args[$i] -eq '--version') {
                if ($i + 1 -ge $args.Count) { throw 'Missing release tag after --version.' }
                $version = $args[$i + 1]
            } elseif ($args[$i] -like '--version=*') {
                $version = $args[$i].Substring('--version='.Length)
            }
        }
        $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
        if ($architecture -ne 'x64') { throw 'Windows x64 is required.' }
        $base = 'https://github.com/MiniMax-AI/OpenAgentCore/releases/latest/download'
        if ($version -ne 'latest') { $base = "https://github.com/MiniMax-AI/OpenAgentCore/releases/download/$version" }
        $directory = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
        New-Item -ItemType Directory -Path $directory | Out-Null
        $asset = 'oac-windows-amd64.exe'
        Get-Release $asset
        Get-Release "$asset.sha256"
        $binary = Join-Path $directory $asset
        $actual = (Get-FileHash -Algorithm SHA256 $binary).Hash.ToLowerInvariant() + "  $asset"
        if ($actual -cne (Get-Content -Raw (Join-Path $directory "$asset.sha256")).Trim()) { throw 'Installer checksum mismatch. Rerun the installer to download it again.' }
        & $binary install @args
        if ($LASTEXITCODE -ne 0) { throw "Installation failed (exit $LASTEXITCODE). Fix the reported error and rerun." }
    } catch {
        $failure = $_.Exception.Message
    } finally {
        if ($directory) { Remove-Item -Recurse -Force $directory -ErrorAction SilentlyContinue }
    }
    if ($failure) {
        $global:LASTEXITCODE = 1
        Write-Error $failure -ErrorAction Continue
        if ($PSCommandPath) { exit 1 }
    }
} @args
