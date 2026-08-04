# Multica installer for Windows — one command to get started.
#
# Install CLI (default): connects to multica.ai
#   irm https://cdn.leagent.me/computer/install.ps1 | iex
#
# Self-host: starts a local Multica server + installs CLI + configures
#   $env:MULTICA_MODE="local"; irm https://cdn.leagent.me/computer/install.ps1 | iex
#

$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
$RepoUrl       = "https://github.com/LRM-Teams/multica.git"
$RepoWebUrl    = "https://github.com/LRM-Teams/multica"
# CLI binary releases are served from our own domain, not GitHub: an
# unauthenticated request to the private LRM-Teams/multica repo's GitHub
# API/asset host always 404s. See server/internal/cli/update.go
# ReleaseManifestBaseURL.
#
# # Primary feed: filed custom domain. OSS via MULTICA_RELEASE_MANIFEST_BASE_URL.
$ReleaseManifestBaseUrl = if ($env:MULTICA_RELEASE_MANIFEST_BASE_URL) { $env:MULTICA_RELEASE_MANIFEST_BASE_URL } else { "https://cdn.leagent.me/computer" }
$InstallScriptUrl = "$ReleaseManifestBaseUrl/install.ps1"
$DefaultInstallDir = Join-Path $env:USERPROFILE ".multica\server"
$InstallDir    = if ($env:MULTICA_INSTALL_DIR) { $env:MULTICA_INSTALL_DIR } else { $DefaultInstallDir }

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
function Write-Info  { param([string]$Msg) Write-Host "==> $Msg" -ForegroundColor Cyan }
function Write-Ok    { param([string]$Msg) Write-Host "[OK] $Msg" -ForegroundColor Green }
function Write-Warn  { param([string]$Msg) Write-Warning $Msg }
function Write-Fail  { param([string]$Msg) Write-Host "[ERROR] $Msg" -ForegroundColor Red; exit 1 }

function Test-CommandExists {
    param([string]$Name)
    $null -ne (Get-Command $Name -ErrorAction SilentlyContinue)
}

function New-RandomHex {
    param([int]$ByteCount)

    $bytes = New-Object byte[] $ByteCount
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($bytes)
    } finally {
        $rng.Dispose()
    }
    return -join ($bytes | ForEach-Object { "{0:x2}" -f $_ })
}

function Get-EnvFileValue {
    param(
        [string]$Path,
        [string]$Name,
        [string]$Default
    )

    if (-not (Test-Path $Path)) {
        return $Default
    }

    $prefix = "$Name="
    $line = Get-Content $Path |
        Where-Object { $_.StartsWith($prefix) } |
        Select-Object -Last 1
    if (-not $line) {
        return $Default
    }

    $value = $line.Substring($prefix.Length).Trim().Trim('"').Trim("'")
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $Default
    }
    return $value
}

function Get-SelfHostBackendPort {
    foreach ($name in @("BACKEND_PORT", "API_PORT", "SERVER_PORT", "PORT")) {
        $value = Get-EnvFileValue -Path (Join-Path $InstallDir ".env") -Name $name -Default ""
        if (-not [string]::IsNullOrWhiteSpace($value)) {
            return $value
        }
    }
    return "8080"
}

function Get-SelfHostFrontendPort {
    return Get-EnvFileValue -Path (Join-Path $InstallDir ".env") -Name "FRONTEND_PORT" -Default "3000"
}

function Get-ReleaseManifest {
    try {
        return Invoke-RestMethod -Uri "$ReleaseManifestBaseUrl/latest.json" -ErrorAction Stop
    } catch {
        return $null
    }
}

function Get-LatestVersion {
    $manifest = Get-ReleaseManifest
    if ($manifest -and $manifest.tag) {
        return $manifest.tag
    }
    return $null
}

function Test-SourceRepoTag {
    param([string]$Tag)

    try {
        git ls-remote --exit-code --tags $RepoUrl "refs/tags/$Tag" *> $null
        return $LASTEXITCODE -eq 0
    } catch {
        return $false
    }
}

function Get-SelfHostRef {
    if ($env:MULTICA_SELFHOST_REF) {
        return $env:MULTICA_SELFHOST_REF
    }

    $latest = Get-LatestVersion
    if ($latest -and (Test-SourceRepoTag -Tag $latest)) {
        return $latest
    }

    return "main"
}

function Checkout-ServerRef {
    param([string]$Ref)

    if ($Ref -eq "main") {
        git fetch origin main --depth 1 2>$null
        git checkout --force main 2>$null
        git reset --hard origin/main 2>$null
        return
    }

    git fetch origin --tags --force 2>$null
    $tagRef = "refs/tags/$Ref"
    git show-ref --verify --quiet $tagRef 2>$null
    if ($LASTEXITCODE -eq 0) {
        git checkout --force $Ref 2>$null
        return
    }

    git fetch origin $Ref --depth 1 2>$null
    git checkout --force $Ref 2>$null
}

function Pull-OfficialSelfHostImages {
    docker compose -f docker-compose.selfhost.yml pull
    if ($LASTEXITCODE -eq 0) {
        return
    }

    Write-Host ""
    Write-Warn "Official images for the selected self-host channel are not published yet."
    Write-Host "This can happen before the first GHCR release is available."
    Write-Host "From $InstallDir, build from source instead:"
    Write-Host "  docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d --build"
    exit 1
}

function Convert-ToCliArch {
    param([object]$Value)

    if ($null -eq $Value) {
        return $null
    }

    $normalized = "$Value".Trim().ToUpperInvariant()
    switch ($normalized) {
        "9"      { return "amd64" }
        "AMD64"  { return "amd64" }
        "X64"    { return "amd64" }
        "X86_64" { return "amd64" }
        "12"     { return "arm64" }
        "ARM64"  { return "arm64" }
        "AARCH64" { return "arm64" }
        default  { return $null }
    }
}

function Get-WindowsCliArch {
    $signals = @()
    $nativeArchSignalFound = $false

    # Prefer the native processor architecture over the current PowerShell
    # process architecture. This keeps Windows on ARM from being misdetected
    # when PowerShell is running through x64/x86 emulation.
    try {
        if (Get-Command Get-CimInstance -ErrorAction SilentlyContinue) {
            $processorArch = Get-CimInstance -ClassName Win32_Processor -ErrorAction Stop |
                Select-Object -First 1 -ExpandProperty Architecture
            $signals += [pscustomobject]@{ Source = "Win32_Processor.Architecture"; Value = $processorArch }
            $nativeArchSignalFound = $true
        }
    } catch {}

    try {
        if (-not $nativeArchSignalFound -and (Get-Command Get-WmiObject -ErrorAction SilentlyContinue)) {
            $processorArch = Get-WmiObject -Class Win32_Processor -ErrorAction Stop |
                Select-Object -First 1 -ExpandProperty Architecture
            $signals += [pscustomobject]@{ Source = "Win32_Processor.Architecture"; Value = $processorArch }
            $nativeArchSignalFound = $true
        }
    } catch {}

    try {
        $signals += [pscustomobject]@{
            Source = "RuntimeInformation.OSArchitecture"
            Value = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
        }
    } catch {}

    $signals += [pscustomobject]@{ Source = "PROCESSOR_ARCHITEW6432"; Value = $env:PROCESSOR_ARCHITEW6432 }
    $signals += [pscustomobject]@{ Source = "PROCESSOR_ARCHITECTURE"; Value = $env:PROCESSOR_ARCHITECTURE }

    foreach ($signal in $signals) {
        $arch = Convert-ToCliArch $signal.Value
        if ($arch) {
            return $arch
        }
    }

    $details = ($signals |
        Where-Object { $null -ne $_.Value -and "$($_.Value)".Trim() -ne "" } |
        ForEach-Object { "$($_.Source)=$($_.Value)" }) -join ", "
    if (-not $details) {
        $details = "no architecture signals available"
    }

    Write-Fail "Unsupported Windows architecture ($details). Only x64 and ARM64 are supported."
}

function Get-InstalledCliVersion {
    try {
        $firstLine = multica version 2>$null | Select-Object -First 1
        if ("$firstLine" -match '\b(v?\d+(?:\.\d+)+)\b') {
            $version = $Matches[1]
            if ($version -notlike 'v*') {
                $version = "v$version"
            }
            return $version
        }
    } catch {}

    return $null
}

# ---------------------------------------------------------------------------
# CLI Installation
# ---------------------------------------------------------------------------
function Install-CliBinary {
    param([object]$Manifest)

    Write-Info "Installing Multica CLI from the release feed..."

    if (-not [Environment]::Is64BitOperatingSystem) {
        Write-Fail "Multica requires a 64-bit Windows installation."
    }

    $arch = Get-WindowsCliArch

    if (-not $Manifest -or -not $Manifest.tag) {
        Write-Fail "Could not fetch the release manifest from $ReleaseManifestBaseUrl/latest.json. Check your network connection."
    }
    $latest = $Manifest.tag
    $platformKey = "windows-$arch"
    $asset = if ($Manifest.platforms) { $Manifest.platforms.$platformKey } else { $null }
    if (-not $asset -or -not $asset.url -or -not $asset.sha256) {
        Write-Fail "No published release asset for platform $platformKey in $latest."
    }
    $url = $asset.url
    $expectedHash = "$($asset.sha256)".ToLower()

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "multica-install"

    if (Test-Path $tmpDir) { Remove-Item $tmpDir -Recurse -Force }
    New-Item -ItemType Directory -Path $tmpDir | Out-Null

    Write-Info "Downloading $url ..."
    try {
        Invoke-WebRequest -Uri $url -OutFile (Join-Path $tmpDir "multica.zip") -UseBasicParsing
    } catch {
        Remove-Item $tmpDir -Recurse -Force
        Write-Fail "Failed to download CLI binary: $_"
    }

    # Verify SHA256 checksum — the manifest carries it inline (see
    # server/internal/cli/update.go ReleaseAsset), so there is no separate
    # checksums.txt fetch, and a missing/mismatched hash fails closed rather
    # than silently skipping verification.
    $zipFile = Join-Path $tmpDir "multica.zip"
    $actualHash = (Get-FileHash -Path $zipFile -Algorithm SHA256).Hash.ToLower()
    if ($actualHash -ne $expectedHash) {
        Remove-Item $tmpDir -Recurse -Force
        Write-Fail "Checksum verification failed. Expected: $expectedHash, Got: $actualHash"
    }
    Write-Ok "Checksum verified"

    Expand-Archive -Path (Join-Path $tmpDir "multica.zip") -DestinationPath $tmpDir -Force

    $binDir = Join-Path $env:USERPROFILE ".multica\bin"
    if (-not (Test-Path $binDir)) {
        New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    }

    $exeSrc = Join-Path $tmpDir "multica.exe"
    if (-not (Test-Path $exeSrc)) {
        $exeSrc = Get-ChildItem -Path $tmpDir -Filter "multica.exe" -Recurse | Select-Object -First 1 -ExpandProperty FullName
    }
    if (-not $exeSrc -or -not (Test-Path $exeSrc)) {
        Remove-Item $tmpDir -Recurse -Force
        Write-Fail "multica.exe not found in downloaded archive."
    }

    Copy-Item $exeSrc (Join-Path $binDir "multica.exe") -Force
    Remove-Item $tmpDir -Recurse -Force

    Add-ToUserPath $binDir
    Write-Ok "Multica CLI installed to $binDir\multica.exe"
}

function Add-ToUserPath {
    param([string]$Dir)
    $currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($currentPath -and $currentPath.Split(";") -contains $Dir) {
        return
    }
    $newPath = if ($currentPath) { "$currentPath;$Dir" } else { $Dir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    # Also update current session
    if ($env:Path -notlike "*$Dir*") {
        $env:Path = "$Dir;$env:Path"
    }
    Write-Info "Added $Dir to user PATH (restart your terminal for other sessions to pick it up)."
}

function Install-Cli {
    $manifest = Get-ReleaseManifest

    if (Test-CommandExists "multica") {
        $currentVer = Get-InstalledCliVersion
        if (-not $manifest -or -not $manifest.tag) {
            Write-Fail "Could not determine latest release from $ReleaseManifestBaseUrl/latest.json. Refusing to assume the installed CLI is current."
        }
        $latestVer = $manifest.tag

        $currentCmp = if ($currentVer) { $currentVer -replace '^v','' } else { $null }
        $latestCmp = if ($latestVer) { $latestVer -replace '^v','' } else { $null }

        try {
            $isUpToDate = $currentCmp -and $latestCmp -and ([System.Version]$currentCmp -ge [System.Version]$latestCmp)
        } catch {
            $isUpToDate = $currentCmp -and $latestCmp -and ($currentCmp -eq $latestCmp)
        }

        if ($isUpToDate) {
            Write-Ok "Multica CLI is up to date ($currentVer)"
            return
        }

        Write-Info "Multica CLI $currentVer installed, latest is $latestVer - upgrading..."
        Install-CliBinary -Manifest $manifest

        $newVer = Get-InstalledCliVersion
        Write-Ok "Multica CLI upgraded ($currentVer -> $newVer)"
        return
    }

    Install-CliBinary -Manifest $manifest

    if (-not (Test-CommandExists "multica")) {
        Write-Fail "CLI installed but 'multica' not found on PATH. Restart your terminal and try again."
    }
}

# ---------------------------------------------------------------------------
# Docker check
# ---------------------------------------------------------------------------
function Test-Docker {
    if (-not (Test-CommandExists "docker")) {
        Write-Fail @"
Docker is not installed. Multica self-hosting requires Docker and Docker Compose.

Install Docker Desktop for Windows:
  https://docs.docker.com/desktop/install/windows-install/

After installing Docker, re-run this script with `$env:MULTICA_MODE="local"`.
"@
    }

    try {
        docker info 2>$null | Out-Null
    } catch {
        Write-Fail "Docker is installed but not running. Please start Docker Desktop and re-run this script."
    }

    Write-Ok "Docker is available"
}

# ---------------------------------------------------------------------------
# Server setup (self-host / local)
# ---------------------------------------------------------------------------
function Install-Server {
    Write-Info "Setting up Multica server..."
    $serverRef = Get-SelfHostRef
    Write-Info "Using self-host assets from $serverRef..."

    if (Test-Path (Join-Path $InstallDir ".git")) {
        Write-Info "Updating existing installation at $InstallDir..."
        Write-Warn "Any local changes in $InstallDir will be overwritten."
    } else {
        Write-Info "Cloning Multica repository..."
        if (-not (Test-CommandExists "git")) {
            Write-Fail "Git is not installed. Please install git and re-run."
        }
        if (Test-Path $InstallDir) {
            Write-Warn "Removing incomplete installation at $InstallDir..."
            Remove-Item $InstallDir -Recurse -Force
        }
        $parentDir = Split-Path $InstallDir -Parent
        if (-not (Test-Path $parentDir)) {
            New-Item -ItemType Directory -Path $parentDir -Force | Out-Null
        }
        git clone --depth 1 $RepoUrl $InstallDir
    }

    Push-Location $InstallDir
    Checkout-ServerRef $serverRef
    Write-Ok "Repository ready at $InstallDir ($serverRef)"

    if (-not (Test-Path ".env")) {
        Write-Info "Creating .env with random secrets..."
        Copy-Item ".env.example" ".env"
        $jwt = New-RandomHex 32
        $pgpass = New-RandomHex 24
        $content = Get-Content ".env"
        $content = $content -replace '^JWT_SECRET=.*', "JWT_SECRET=$jwt"
        $content = $content -replace '^POSTGRES_PASSWORD=.*', "POSTGRES_PASSWORD=$pgpass"
        $content = $content -replace '^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)', "`${1}$pgpass`${2}"
        $content | Set-Content ".env"
        Write-Ok "Generated .env with random JWT_SECRET and POSTGRES_PASSWORD"
    } else {
        Write-Ok "Using existing .env"
    }

    Write-Info "Pulling official Multica images..."
    Pull-OfficialSelfHostImages
    Write-Info "Starting Multica services (this may take a few minutes on first run)..."
    docker compose -f docker-compose.selfhost.yml up -d

    Write-Info "Waiting for backend to be ready..."
    $backendPort = Get-SelfHostBackendPort
    $ready = $false
    for ($i = 1; $i -le 45; $i++) {
        try {
            $null = Invoke-WebRequest -Uri "http://localhost:$backendPort/health" -UseBasicParsing -TimeoutSec 2
            $ready = $true
            break
        } catch {
            Start-Sleep -Seconds 2
        }
    }

    if ($ready) {
        Write-Ok "Multica server is running"
    } else {
        Write-Warn "Server is still starting. Check logs with:"
        Write-Host "  cd $InstallDir; docker compose -f docker-compose.selfhost.yml logs"
    }

    Pop-Location
}


# ---------------------------------------------------------------------------
# Main: Default mode (cloud)
# ---------------------------------------------------------------------------
function Start-DefaultInstall {
    Write-Host ""
    Write-Host "  Multica - Installer" -ForegroundColor White
    Write-Host ""

    Install-Cli

    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host "  [OK] Multica CLI is ready!" -ForegroundColor Green
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Next: configure your environment"
    Write-Host ""
    Write-Host "     multica setup               " -NoNewline; Write-Host "# Connect to Multica Cloud (multica.ai)" -ForegroundColor DarkGray
    Write-Host "     multica setup self-host      " -NoNewline; Write-Host "# Connect to a self-hosted server" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  Self-hosting? Install the server first:"
    Write-Host "     `$env:MULTICA_MODE=`"with-server`"; irm $InstallScriptUrl | iex"
    Write-Host ""
}

# ---------------------------------------------------------------------------
# Main: Local mode (self-host)
# ---------------------------------------------------------------------------
function Start-LocalInstall {
    Write-Host ""
    Write-Host "  Multica - Self-Host Installer" -ForegroundColor White
    Write-Host "  Provisioning server infrastructure + installing CLI"
    Write-Host ""

    Test-Docker
    Install-Server
    Install-Cli

    Write-Host ""
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host "  [OK] Multica server is running and CLI is ready!" -ForegroundColor Green
    Write-Host "  ============================================" -ForegroundColor Green
    Write-Host ""
    $frontendPort = Get-SelfHostFrontendPort
    $backendPort = Get-SelfHostBackendPort
    Write-Host "  Frontend:  http://localhost:$frontendPort"
    Write-Host "  Backend:   http://localhost:$backendPort"
    Write-Host "  Server at: $InstallDir"
    Write-Host ""
    Write-Host "  Next: configure your CLI to connect"
    Write-Host ""
    Write-Host "     multica setup self-host  " -NoNewline; Write-Host "# Configure + authenticate + start daemon" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host "  Login: configure RESEND_API_KEY in .env for email codes,"
    Write-Host "  or read the generated code from backend logs when Resend is unset."
    Write-Host ""
    Write-Host "  To stop all services:"
    Write-Host "     `$env:MULTICA_MODE=`"stop`"; irm $InstallScriptUrl | iex"
    Write-Host ""
}

# ---------------------------------------------------------------------------
# Stop: shut down a self-hosted installation
# ---------------------------------------------------------------------------
function Start-Stop {
    Write-Host ""
    Write-Info "Stopping Multica services..."

    if (Test-Path $InstallDir) {
        Push-Location $InstallDir
        if (Test-Path "docker-compose.selfhost.yml") {
            docker compose -f docker-compose.selfhost.yml down
            Write-Ok "Docker services stopped"
        } else {
            Write-Warn "No docker-compose.selfhost.yml found at $InstallDir"
        }
        Pop-Location
    } else {
        Write-Warn "No Multica installation found at $InstallDir"
    }

    if (Test-CommandExists "multica") {
        try {
            multica daemon stop 2>$null
            Write-Ok "Daemon stopped"
        } catch {}
    }

    Write-Host ""
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------
$mode = if ($env:MULTICA_MODE) { $env:MULTICA_MODE.ToLower() } else { "default" }

switch ($mode) {
    "with-server" { Start-LocalInstall }
    "local"       { Start-LocalInstall }  # backwards compat alias
    "stop"        { Start-Stop }
    default       { Start-DefaultInstall }
}
