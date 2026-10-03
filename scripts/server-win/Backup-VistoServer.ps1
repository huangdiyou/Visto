param(
    [string]$BackupDirectory = "$env:ProgramData\Visto\backups",
    [string]$TaskName = "Visto Server"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-Administrator {
    $principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "Run this command from an elevated PowerShell window."
    }
}

Assert-Administrator
$packageRoot = Split-Path -Parent $PSCommandPath
$securityModule = Join-Path $packageRoot "VistoServer.Security.psm1"
if (-not (Test-Path -LiteralPath $securityModule)) { throw "VistoServer.Security.psm1 is missing." }
Import-Module $securityModule -Force
$packageRoot = Assert-VistoManagedDirectoryAcl -Path $packageRoot
$config = Get-Content -LiteralPath (Join-Path $packageRoot "visto-server.json") -Raw | ConvertFrom-Json
$dataDir = [Environment]::ExpandEnvironmentVariables([string]$config.dataDir)
if (-not (Test-Path -LiteralPath $dataDir)) { throw "Visto data directory was not found: $dataDir" }
$dataDir = Assert-VistoManagedDirectoryAcl -Path $dataDir

$BackupDirectory = Protect-VistoManagedDirectory -Path $BackupDirectory
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$archive = Join-Path $BackupDirectory "visto-server-data-$timestamp.zip"
$metadata = "$archive.json"
$serverBinary = Join-Path $packageRoot "bin\visto-server.exe"

Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Wait-VistoScheduledTaskStopped -TaskName $TaskName
Wait-VistoFileUnlocked -Path (Join-Path $dataDir "review-studio.db")
try {
    Compress-Archive -LiteralPath $dataDir -DestinationPath $archive -CompressionLevel Optimal
    $checksum = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    $archiveName = Split-Path -Leaf $archive
    # A successful backup requires verified database facts.
    & $serverBinary backup metadata `
        --data-dir $dataDir `
        --archive $archiveName `
        --sha256 $checksum `
        --output $metadata
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $metadata)) {
        throw "Backup failed: database facts could not be verified; update must not continue."
    }
    Write-Output "Backup created: $archive"
} finally {
    Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}
