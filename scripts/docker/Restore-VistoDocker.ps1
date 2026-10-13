param(
    [Parameter(Mandatory = $true)]
    [string]$BackupFile,
    [string]$ComposeFile,
    [string]$ProjectName = "visto",
    [Int64]$MaximumExpandedBytes = 100GB,
    [int]$MaximumEntryCount = 100000,
    [switch]$ConfirmRestore,
    [switch]$CheckOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
Import-Module (Join-Path $PSScriptRoot "VistoDocker.psm1") -Force

# Restore path rejections are a distinct outcome from a broken script: exit
# code 11 mirrors visto-server backup precheck. finally blocks still run so
# staging volumes and directories are cleaned up.
function Reject-Restore {
    param([string]$Message)
    [Console]::Error.WriteLine("ERROR: $Message")
    exit 11
}

$legacyRejection =
    "Backup metadata uses the legacy format (schemaVersion 1) without a source data directory, " +
    "so the restore cannot prove it would land in the original data directory. " +
    "Current data was not changed. Create a fresh backup only if the original data still exists and is readable; " +
    "if only the legacy archive remains, preserve it and its metadata. This release has no supported recovery or conversion path for that archive; do not relabel it as v2."

if (-not $ComposeFile) { $ComposeFile = Join-Path $PSScriptRoot "..\..\compose.yaml" }
if ($CheckOnly -and $ConfirmRestore) {
    throw "-CheckOnly and -ConfirmRestore are mutually exclusive."
}
if (-not $ConfirmRestore -and -not $CheckOnly) {
    throw "Restore replaces the current Visto data volume. Re-run with -ConfirmRestore after verifying the backup file, or run with -CheckOnly to validate the backup without changing anything."
}
$ComposeFile = (Resolve-Path -LiteralPath $ComposeFile).Path
$BackupFile = (Resolve-Path -LiteralPath $BackupFile).Path
Assert-VistoDocker -ComposeFile $ComposeFile

# Copy the archive and its metadata to a private staging directory so hash,
# validation, and extraction are all bound to the same staged copy. This
# closes the time-of-check/time-of-use window where the host path could be
# swapped between validation and extraction.
$backupName = Split-Path -Leaf $BackupFile
if ($backupName -notmatch '^[A-Za-z0-9._-]+$') { throw "Backup file name is unsafe." }
$stagingDir = Join-Path ([System.IO.Path]::GetTempPath()) ("visto-restore-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $stagingDir | Out-Null
try {
    $stagingBackup = Join-Path $stagingDir $backupName
    Copy-Item -LiteralPath $BackupFile -Destination $stagingBackup
    $stagingMetadata = Join-Path $stagingDir ($backupName + ".json")
    Copy-Item -LiteralPath "$BackupFile.json" -Destination $stagingMetadata

$metadataPath = $stagingMetadata
if (-not (Test-Path -LiteralPath $metadataPath)) { throw "Backup metadata file is required: $metadataPath" }
$metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
$schemaVersion = [int]$metadata.schemaVersion
if ($schemaVersion -ne 1 -and $schemaVersion -ne 2) {
    Reject-Restore "Backup metadata schemaVersion $schemaVersion is not supported by this restore script. Current data was not changed."
}
if ([string]$metadata.archive -ne $backupName) {
    throw "Backup metadata does not describe the selected archive."
}
$actualHash = (Get-FileHash -LiteralPath $stagingBackup -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -ne ([string]$metadata.sha256).ToLowerInvariant()) {
    throw "Backup SHA-256 does not match its metadata. Current data was not changed."
}
# Legacy schemaVersion 1 backups record no source data directory, so no check
# can prove this restore lands back where the backup was taken. Refuse before
# anything is touched; do not fall back to assuming the current volume.
if ($schemaVersion -eq 1) {
    Reject-Restore $legacyRejection
}

$volume = Get-VistoDataVolume -ComposeFile $ComposeFile -ProjectName $ProjectName
$coreContainer = (& docker compose -f $ComposeFile -p $ProjectName ps -q core).Trim()
$utilityImage = (& docker inspect $coreContainer --format '{{.Image}}').Trim()
if ($utilityImage -notmatch '^sha256:[a-f0-9]{64}$') { throw "Could not resolve the running Core image digest." }
$dataDir = Get-VistoCoreDataDir -ComposeFile $ComposeFile -ProjectName $ProjectName
$backupDirectory = $stagingDir

# A schemaVersion 2 backup records the compose project, the data volume and
# the container data directory it was taken from. A different target is a
# different instance, not a restore target.
if ($metadata.PSObject.Properties["composeProject"] -and [string]$metadata.composeProject -and [string]$metadata.composeProject -ne $ProjectName) {
    Reject-Restore "Backup metadata records compose project $($metadata.composeProject) but this restore targets $ProjectName. Current data was not changed."
}
if ($metadata.PSObject.Properties["dataVolume"] -and [string]$metadata.dataVolume -and [string]$metadata.dataVolume -ne $volume) {
    Reject-Restore "Backup metadata records data volume $($metadata.dataVolume) but this restore targets $volume. Current data was not changed."
}

$archiveManifest = Test-VistoBackupArchive `
    -UtilityImage $utilityImage `
    -BackupDirectory $backupDirectory `
    -BackupName $backupName `
    -MaximumExpandedBytes $MaximumExpandedBytes `
    -MaximumEntryCount $MaximumEntryCount

$suffix = [guid]::NewGuid().ToString("N")
$stageVolume = "visto-restore-stage-$suffix"
$rollbackVolume = "visto-restore-rollback-$suffix"
& docker volume create $stageVolume | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Could not create restore staging volume." }
try {
    & docker run --rm --user 0 --entrypoint tar --volume "${stageVolume}:/stage" --volume "${backupDirectory}:/backup:ro" $utilityImage -xzf "/backup/$backupName" -C /stage
    if ($LASTEXITCODE -ne 0) { throw "Backup extraction into staging volume failed. Current data was not changed." }

    # Restore path precheck inside the Core image, against the isolated staging
    # volume and the running container's data directory. This runs BEFORE the
    # stack is stopped; a rejection leaves the stack, its configuration and its
    # data volume completely untouched.
    $precheckOutput = & docker run --rm --user 0 --entrypoint /usr/local/bin/visto-server `
        --volume "${stageVolume}:/stage" `
        --volume "${backupDirectory}:/backup:ro" `
        $utilityImage backup precheck `
        --metadata "/backup/$backupName.json" `
        --staged-data-dir /stage `
        --target-data-dir $dataDir 2>&1
    $precheckExit = $LASTEXITCODE
    foreach ($line in @($precheckOutput)) { if ($line) { Write-Output ([string]$line) } }
    if ($precheckExit -eq 11) {
        if ($CheckOnly) {
            Reject-Restore "Restore precheck rejected this backup (see the reasons above). No stack was stopped and no data was changed."
        }
        Reject-Restore "Restore precheck rejected this backup (see the reasons above). Current data was not changed."
    }
    if ($precheckExit -ne 0) {
        throw "Restore precheck could not run (exit $precheckExit). Current data was not changed."
    }
    if ($CheckOnly) {
        Write-Output "Check-only precheck passed: this backup can be restored into data volume $volume."
        Write-Output "No stack was stopped and no data was changed."
        exit 0
    }

    Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("down")
    & docker volume create $rollbackVolume | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not create rollback volume." }
    & docker run --rm --user 0 --entrypoint sh --volume "${volume}:/source:ro" --volume "${rollbackVolume}:/target" $utilityImage -c 'tar -C /source -cf - . | tar -C /target -xf -'
    if ($LASTEXITCODE -ne 0) { throw "Could not preserve the current data volume." }
    & docker run --rm --user 0 --entrypoint sh --volume "${volume}:/target" --volume "${stageVolume}:/source:ro" $utilityImage -c 'rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; tar -C /source -cf - . | tar -C /target -xf -'
    if ($LASTEXITCODE -ne 0) { throw "Could not switch to the staged restore data." }
    Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("up", "-d")
    Wait-VistoCoreHealthy -ComposeFile $ComposeFile -ProjectName $ProjectName
} catch {
    $failure = $_
    if ((& docker volume ls -q --filter "name=^${rollbackVolume}$").Trim()) {
        Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("down")
        & docker run --rm --user 0 --entrypoint sh --volume "${volume}:/target" --volume "${rollbackVolume}:/source:ro" $utilityImage -c 'rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; tar -C /source -cf - . | tar -C /target -xf -' | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "Restore failed and the rollback volume could not be copied back: $failure"
        }
    }
    Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("up", "-d")
    Wait-VistoCoreHealthy -ComposeFile $ComposeFile -ProjectName $ProjectName
    throw "Restore failed; the previous data was restored and Core is healthy: $failure"
} finally {
    & docker volume rm -f $stageVolume | Out-Null
    & docker volume rm -f $rollbackVolume | Out-Null
}
    Write-Output "Restore completed and Core is healthy."
} finally {
    if (Test-Path -LiteralPath $stagingDir) { Remove-Item -LiteralPath $stagingDir -Recurse -Force }
}
