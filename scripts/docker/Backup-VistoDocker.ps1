param(
    [string]$ComposeFile,
    [string]$ProjectName = "visto",
    [string]$BackupDirectory = (Join-Path (Get-Location) "backups")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
Import-Module (Join-Path $PSScriptRoot "VistoDocker.psm1") -Force

if (-not $ComposeFile) { $ComposeFile = Join-Path $PSScriptRoot "..\..\compose.yaml" }
$ComposeFile = (Resolve-Path -LiteralPath $ComposeFile).Path
Assert-VistoDocker -ComposeFile $ComposeFile
New-Item -ItemType Directory -Path $BackupDirectory -Force | Out-Null
$BackupDirectory = (Resolve-Path -LiteralPath $BackupDirectory).Path

$volume = Get-VistoDataVolume -ComposeFile $ComposeFile -ProjectName $ProjectName
$dataDir = Get-VistoCoreDataDir -ComposeFile $ComposeFile -ProjectName $ProjectName
$coreContainer = (& docker compose -f $ComposeFile -p $ProjectName ps -q core).Trim()
$utilityImage = (& docker inspect $coreContainer --format '{{.Image}}').Trim()
if ($utilityImage -notmatch '^sha256:[a-f0-9]{64}$') { throw "Could not resolve the running Core image digest." }
$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$archiveName = "visto-data-$timestamp.tar.gz"
$archivePath = Join-Path $BackupDirectory $archiveName
$metadataPath = "$archivePath.json"

Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("stop", "core")
try {
    & docker run --rm --user 0 --entrypoint tar --volume "${volume}:/source:ro" --volume "${BackupDirectory}:/backup" $utilityImage -czf "/backup/$archiveName" -C /source .
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $archivePath)) {
        throw "Docker could not create the backup archive."
    }
    $checksum = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    # A successful backup requires verified database facts.
    & docker run --rm --user 0 --entrypoint /usr/local/bin/visto-server `
        --volume "${volume}:${dataDir}:ro" `
        --volume "${BackupDirectory}:/backup" `
        $utilityImage backup metadata `
        --data-dir $dataDir `
        --archive $archiveName `
        --sha256 $checksum `
        --no-archive-root `
        --compose-project $ProjectName `
        --data-volume $volume `
        --output "/backup/$archiveName.json" | Out-Null
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $metadataPath)) {
        throw "Backup failed: database facts could not be verified; update must not continue."
    }
    Write-Output "Backup created: $archivePath"
    Write-Output "Metadata: $metadataPath"
} finally {
    Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("up", "-d", "core")
    Wait-VistoCoreHealthy -ComposeFile $ComposeFile -ProjectName $ProjectName
}
