param(
    [Parameter(Mandatory = $true)]
    [string]$BackupFile,
    [string]$TaskName = "Visto Server",
    [switch]$ConfirmRestore,
    [switch]$CheckOnly,
    [int]$MaximumEntryCount = 100000,
    [Int64]$MaximumExpandedBytes = 100GB,
    [double]$MaximumCompressionRatio = 100.0
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Wait-Server {
    param([string]$Address, [int]$TimeoutSeconds = 90)
    $hostName, $port = $Address -split ":", 2
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        try {
            $client = [Net.Sockets.TcpClient]::new()
            $connection = $client.BeginConnect($hostName, [int]$port, $null, $null)
            if ($connection.AsyncWaitHandle.WaitOne(1000) -and $client.Connected) {
                $client.Close()
                return
            }
            $client.Close()
        } catch { }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "Visto Server did not become reachable at $Address."
}

# Restore path rejections are a distinct outcome from a broken script: exit
# code 11 mirrors visto-server backup precheck. finally blocks still run so
# staging directories are cleaned up.
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

if ($CheckOnly -and $ConfirmRestore) {
    throw "-CheckOnly and -ConfirmRestore are mutually exclusive."
}
if (-not $ConfirmRestore -and -not $CheckOnly) {
    throw "Restore replaces current Visto data. Re-run with -ConfirmRestore after verifying the backup file, or run with -CheckOnly to validate the backup without changing anything."
}
$principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run this command from an elevated PowerShell window."
}
$packageRoot = Split-Path -Parent $PSCommandPath
$securityModule = Join-Path $packageRoot "VistoServer.Security.psm1"
if (-not (Test-Path -LiteralPath $securityModule)) { throw "VistoServer.Security.psm1 is missing." }
Import-Module $securityModule -Force
$packageRoot = Assert-VistoManagedDirectoryAcl -Path $packageRoot
$config = Get-Content -LiteralPath (Join-Path $packageRoot "visto-server.json") -Raw | ConvertFrom-Json
$dataDir = [Environment]::ExpandEnvironmentVariables([string]$config.dataDir)
$dataDir = Assert-VistoManagedDirectoryAcl -Path $dataDir
$address = [string]$config.address
$BackupFile = (Resolve-Path -LiteralPath $BackupFile).Path

# SAR-F36: bind the caller-supplied backup and its metadata to an immutable
# private copy before hashing/validation, so the archive cannot be swapped
# between SHA-256 verification, entry validation, and extraction (TOCTOU).
$inputRoot = Join-Path (Split-Path -Parent $dataDir) (".visto-restore-input-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $inputRoot -Force | Out-Null
$stagedBackup = Join-Path $inputRoot (Split-Path -Leaf $BackupFile)
Copy-Item -LiteralPath $BackupFile -Destination $stagedBackup -Force
$sourceMetadataPath = "$BackupFile.json"
if (Test-Path -LiteralPath $sourceMetadataPath) {
    Copy-Item -LiteralPath $sourceMetadataPath -Destination (Join-Path $inputRoot (Split-Path -Leaf $sourceMetadataPath)) -Force
}
$inputRoot = Protect-VistoManagedDirectory -Path $inputRoot
$BackupFile = $stagedBackup

$metadataPath = "$BackupFile.json"
if (-not (Test-Path -LiteralPath $metadataPath)) { throw "Backup metadata file is required: $metadataPath" }
$metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
$schemaVersion = [int]$metadata.schemaVersion
if ($schemaVersion -ne 1 -and $schemaVersion -ne 2) {
    Reject-Restore "Backup metadata schemaVersion $schemaVersion is not supported by this restore script. Current data was not changed."
}
if ([string]$metadata.archive -ne (Split-Path -Leaf $BackupFile)) {
    throw "Backup metadata does not describe the selected archive."
}
$actualHash = (Get-FileHash -LiteralPath $BackupFile -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -ne ([string]$metadata.sha256).ToLowerInvariant()) {
    throw "Backup SHA-256 does not match its metadata. Current data was not changed."
}
# Legacy schemaVersion 1 backups record no source data directory, so no check
# can prove this restore lands back where the backup was taken. Refuse before
# anything is touched; do not fall back to assuming the current directory.
if ($schemaVersion -eq 1) {
    Reject-Restore $legacyRejection
}
$archiveRoot = [string]$metadata.archiveRoot
if (-not $archiveRoot) { $archiveRoot = Split-Path -Leaf $dataDir }
if ($archiveRoot -notmatch '^[A-Za-z0-9._-]+$') { throw "Backup archive root is invalid." }

Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::OpenRead($BackupFile)
try {
    $hasContent = $false
    $entryCount = 0
    $expandedBytes = [Int64]0
    foreach ($entry in $zip.Entries) {
        $entryCount++
        if ($entryCount -gt $MaximumEntryCount) {
            throw "Backup exceeds the entry limit of $MaximumEntryCount. Current data was not changed."
        }
        $name = Assert-VistoServerArchiveEntryName -Entry $entry.FullName
        if (-not $name) { continue }
        $segments = $name.Split("/", [StringSplitOptions]::RemoveEmptyEntries)
        if ($segments[0] -ne $archiveRoot) { throw "Backup contains data outside its declared root." }
        $unixType = (($entry.ExternalAttributes -shr 16) -band 0xF000)
        if ($unixType -eq 0xA000) { throw "Backup contains a symbolic link: $name" }
        if (-not ($entry.FullName.EndsWith("/") -or $entry.FullName.EndsWith("\"))) {
            $hasContent = $true
            $memberBytes = [Int64]$entry.Length
            if ($memberBytes -lt 0 -or $expandedBytes -gt ([Int64]::MaxValue - $memberBytes)) {
                throw "Backup archive expanded size overflowed Int64."
            }
            $expandedBytes += $memberBytes
            if ($expandedBytes -gt $MaximumExpandedBytes) {
                throw "Backup expands beyond the configured limit of $MaximumExpandedBytes bytes. Current data was not changed."
            }
            $compressedBytes = [Int64]$entry.CompressedLength
            if ($compressedBytes -gt 0 -and $memberBytes -gt 0) {
                $ratio = [double]$memberBytes / [double]$compressedBytes
                if ($ratio -gt $MaximumCompressionRatio) {
                    throw "Backup contains a member with an excessive compression ratio. Current data was not changed."
                }
            }
        }
    }
    if (-not $hasContent) { throw "Backup archive is empty." }
} finally {
    $zip.Dispose()
}

$dataParent = Split-Path -Parent $dataDir
New-Item -ItemType Directory -Path $dataParent -Force | Out-Null
$stage = Join-Path $dataParent (".visto-restore-stage-" + [guid]::NewGuid().ToString("N"))
$rollback = Join-Path $dataParent (".visto-restore-rollback-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $stage -Force | Out-Null
Expand-Archive -LiteralPath $BackupFile -DestinationPath $stage -Force
$candidate = Join-Path $stage $archiveRoot
if (-not (Test-Path -LiteralPath $candidate -PathType Container)) {
    throw "Backup archive did not produce its declared data directory."
}
# ZIP archives do not preserve the Windows ACL that protects the managed data
# directory. Re-apply the trusted SYSTEM/Administrators ACL before the atomic
# switch; otherwise the restored service correctly refuses to start.
$candidate = Protect-VistoManagedDirectory -Path $candidate

# Restore path precheck: read the database and storage roots from the extracted
# copy and compare them against the metadata and the configured data directory.
# This runs BEFORE the scheduled task is stopped; a rejection leaves the
# running service, its configuration and its data completely untouched.
$serverBinary = Join-Path $packageRoot "bin\visto-server.exe"
if (-not (Test-Path -LiteralPath $serverBinary)) {
    throw "Restore precheck could not run: $serverBinary is missing. Current data was not changed."
}
$precheckOutput = & $serverBinary backup precheck `
    --metadata $metadataPath `
    --staged-data-dir $candidate `
    --target-data-dir $dataDir 2>&1
$precheckExit = $LASTEXITCODE
foreach ($line in @($precheckOutput)) { if ($line) { Write-Output ([string]$line) } }
if ($precheckExit -eq 11) {
    if ($CheckOnly) {
        Reject-Restore "Restore precheck rejected this backup (see the reasons above). No service was stopped and no data was changed."
    }
    Reject-Restore "Restore precheck rejected this backup (see the reasons above). Current data was not changed."
}
if ($precheckExit -ne 0) {
    throw "Restore precheck could not run (exit $precheckExit). Current data was not changed."
}
if ($CheckOnly) {
    Write-Output "Check-only precheck passed: this backup can be restored into $dataDir."
    Write-Output "No service was stopped and no data was changed."
    exit 0
}

$switched = $false
try {
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Wait-VistoScheduledTaskStopped -TaskName $TaskName
    if (Test-Path -LiteralPath $dataDir) { Move-Item -LiteralPath $dataDir -Destination $rollback }
    Move-Item -LiteralPath $candidate -Destination $dataDir
    $switched = $true
    Start-ScheduledTask -TaskName $TaskName
    Wait-Server -Address $address
    if (Test-Path -LiteralPath $rollback) { Remove-Item -LiteralPath $rollback -Recurse -Force }
} catch {
    $failure = $_
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Wait-VistoScheduledTaskStopped -TaskName $TaskName
    if ($switched -and (Test-Path -LiteralPath $dataDir)) { Remove-Item -LiteralPath $dataDir -Recurse -Force }
    if (Test-Path -LiteralPath $rollback) { Move-Item -LiteralPath $rollback -Destination $dataDir }
    Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    throw "Restore failed and the previous data was restored: $failure"
} finally {
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    if (Test-Path -LiteralPath $inputRoot) { Remove-Item -LiteralPath $inputRoot -Recurse -Force }
}
Write-Output "Restore completed."
