param(
    [switch]$Foreground
)

$ErrorActionPreference = "Stop"
$PackageRoot = Split-Path -Parent $PSCommandPath
$securityModule = Join-Path $PackageRoot "VistoServer.Security.psm1"
if (-not (Test-Path -LiteralPath $securityModule)) { throw "VistoServer.Security.psm1 is missing." }
Import-Module $securityModule -Force
$PackageRoot = Assert-VistoManagedDirectoryAcl -Path $PackageRoot
$config = Get-Content -LiteralPath (Join-Path $PackageRoot "visto-server.json") -Raw | ConvertFrom-Json
$dataDir = [Environment]::ExpandEnvironmentVariables([string]$config.dataDir)
$webDir = Join-Path $PackageRoot ([string]$config.webDir)
$core = Join-Path $PackageRoot "bin\visto-core.exe"
$ffmpegRuntime = Join-Path $PackageRoot "runtime\ffmpeg"

if (-not (Test-Path -LiteralPath $core)) { throw "Visto Core is missing from this Server package." }
if (-not (Test-Path -LiteralPath $webDir)) { throw "The bundled Studio web files are missing." }
if (-not (Test-Path -LiteralPath (Join-Path $ffmpegRuntime "ffmpeg.exe")) -or
    -not (Test-Path -LiteralPath (Join-Path $ffmpegRuntime "ffprobe.exe"))) {
    throw "The audited FFmpeg runtime is missing from this Server package."
}
$dataDir = Assert-VistoManagedDirectoryAcl -Path $dataDir
$hostTokenPath = Join-Path $dataDir "host-management-token.txt"
if (-not (Test-Path -LiteralPath $hostTokenPath)) {
    $tokenBytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($tokenBytes) } finally { $rng.Dispose() }
    [Convert]::ToBase64String($tokenBytes) | Set-Content -LiteralPath $hostTokenPath -Encoding ASCII -NoNewline
}
Protect-VistoSecretFile -Path $hostTokenPath
$hostManagementToken = (Get-Content -LiteralPath $hostTokenPath -Raw).Trim()

$env:REVIEW_STUDIO_ADDR = [string]$config.address
$env:REVIEW_STUDIO_DATA_DIR = $dataDir
$env:REVIEW_STUDIO_WEB_DIR = $webDir
$env:VISTO_HOST_MANAGEMENT_TOKEN = $hostManagementToken
$env:REVIEW_STUDIO_SOFTWARE_VIDEO_ENCODER = "libopenh264"
$env:PATH = "$ffmpegRuntime;$env:PATH"

# Optional deployment lock for the transport policy. When requireRemoteHttps is
# true in visto-server.json, "remote access requires HTTPS" is pinned on and the
# Owner page shows it as forced. Leave it absent or false to let the Owner decide
# in Owner settings -> Network and security; that setting applies without a
# restart, while this one requires one.
if ($config.PSObject.Properties.Name -contains "requireRemoteHttps" -and
    [bool]$config.requireRemoteHttps) {
    $env:REVIEW_STUDIO_REQUIRE_HTTPS = "1"
} else {
    Remove-Item Env:REVIEW_STUDIO_REQUIRE_HTTPS -ErrorAction SilentlyContinue
}

# Optional deployment override for the host access switch (D2,
# docs/FREE_TIER_BOUNDARY_DESIGN.md §2.4). The first-run wizard asks once whether
# an Owner web session may add host directories as storage locations; that answer
# is stored and cannot be changed from the web afterwards, because the session
# that benefits from it must not be able to grant it. Set
# "allowWebHostPaths": true or false in visto-server.json to pin the value on
# this host. Leave it absent to let the wizard's answer apply.
if ($config.PSObject.Properties.Name -contains "allowWebHostPaths") {
    $allowWebHostPaths = if ([bool]$config.allowWebHostPaths) { "1" } else { "0" }
    $env:VISTO_ALLOW_WEB_HOST_PATHS = $allowWebHostPaths
} else {
    Remove-Item Env:VISTO_ALLOW_WEB_HOST_PATHS -ErrorAction SilentlyContinue
}

if ($Foreground) {
    & $core
    exit $LASTEXITCODE
}

& $core
exit $LASTEXITCODE
