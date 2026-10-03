param(
    [Parameter(Mandatory = $true)]
    [string]$PackageArchive,
    [Parameter(Mandatory = $true)]
    [string]$ExpectedSha256,
    [string]$ManifestFile,
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

function Assert-UpdateArchiveLayout {
    param(
        [string]$ArchivePath,
        [int]$MaximumEntryCount = 100000,
        [Int64]$MaximumExpandedBytes = 100GB,
        [double]$MaximumCompressionRatio = 100.0
    )

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $runtimeFiles = @(
        "runtime/ffmpeg/ffmpeg.exe", "runtime/ffmpeg/ffprobe.exe",
        "runtime/ffmpeg/LICENSE.txt", "runtime/ffmpeg/FFmpeg-BUILD.txt",
        "runtime/ffmpeg/ffmpeg-distribution-audit.json", "runtime/ffmpeg/server-ffmpeg-runtime.json"
    )
    $required = @(
        "bin/visto-core.exe", "bin/visto-server.exe", "Start-VistoServer.ps1",
        "Install-VistoServer.ps1", "Uninstall-VistoServer.ps1", "Backup-VistoServer.ps1",
        "Restore-VistoServer.ps1", "Update-VistoServer.ps1", "VistoServer.Security.psm1", "visto-server.json"
    ) + $runtimeFiles
    $allowedRootFiles = @(
        "Start-VistoServer.ps1", "Install-VistoServer.ps1", "Uninstall-VistoServer.ps1",
        "Backup-VistoServer.ps1", "Restore-VistoServer.ps1", "Update-VistoServer.ps1",
        "VistoServer.Security.psm1",
        "DEPLOY_WINDOWS_SERVER.md", "THIRD_PARTY_DISTRIBUTION_INVENTORY.md",
        "FFMPEG_DISTRIBUTION.md", "LICENSE.txt", "NOTICE.txt", "THIRD_PARTY_NOTICES.md",
        "THIRD_PARTY.spdx.json", "visto-server.json"
    )
    $seen = @{}
    $entryCount = 0
    $expandedBytes = [Int64]0
    $archive = [IO.Compression.ZipFile]::OpenRead($ArchivePath)
    try {
        foreach ($entry in $archive.Entries) {
            $entryCount++
            if ($entryCount -gt $MaximumEntryCount) {
                throw "The update archive exceeds the entry limit of $MaximumEntryCount."
            }
            $name = Assert-VistoServerArchiveEntryName -Entry $entry.FullName
            if (-not $name) { continue }
            $unixType = (($entry.ExternalAttributes -shr 16) -band 0xF000)
            if ($unixType -eq 0xA000) { throw "The update archive contains a symbolic link: $name" }
            if ($seen.ContainsKey($name)) { throw "The update archive contains a duplicate path: $name" }
            $seen[$name] = $true
            $isDirectory = $entry.FullName.EndsWith("/") -or $entry.FullName.EndsWith("\")
            if (-not $isDirectory) {
                $memberBytes = [Int64]$entry.Length
                if ($memberBytes -lt 0 -or $expandedBytes -gt ([Int64]::MaxValue - $memberBytes)) {
                    throw "The update archive expanded size overflowed Int64."
                }
                $expandedBytes += $memberBytes
                if ($expandedBytes -gt $MaximumExpandedBytes) {
                    throw "The update archive expands beyond the configured limit of $MaximumExpandedBytes bytes."
                }
                $compressedBytes = [Int64]$entry.CompressedLength
                if ($compressedBytes -gt 0 -and $memberBytes -gt 0) {
                    $ratio = [double]$memberBytes / [double]$compressedBytes
                    if ($ratio -gt $MaximumCompressionRatio) {
                        throw "The update archive contains a member with an excessive compression ratio."
                    }
                }
            }
            if ($isDirectory) { continue }
            if ($name.StartsWith("web/")) { continue }
            if ($name -in $runtimeFiles) { continue }
            if ($name -in @("bin/visto-core.exe", "bin/visto-server.exe")) { continue }
            if ($name -notin $allowedRootFiles) { throw "The update archive contains an unexpected file: $name" }
        }
    } finally {
        $archive.Dispose()
    }
    foreach ($name in $required) {
        if (-not $seen.ContainsKey($name)) { throw "The update archive is missing required file: $name" }
    }
}

Assert-Administrator
$packageRoot = Split-Path -Parent $PSCommandPath
$securityModule = Join-Path $packageRoot "VistoServer.Security.psm1"
if (-not (Test-Path -LiteralPath $securityModule)) { throw "VistoServer.Security.psm1 is missing." }
Import-Module $securityModule -Force
$packageRoot = Assert-VistoManagedDirectoryAcl -Path $packageRoot
$PackageArchive = (Resolve-Path -LiteralPath $PackageArchive).Path
if (-not $ManifestFile) { $ManifestFile = "$PackageArchive.manifest.json" }
$ManifestFile = (Resolve-Path -LiteralPath $ManifestFile).Path
# The free tier update channel is unsigned (docs/FREE_TIER_BOUNDARY_DESIGN.md),
# so integrity is anchored on the digest published next to the package. It proves
# the download is intact and is the intended file; it does not prove provenance.
if ($ExpectedSha256 -notmatch '^[0-9a-fA-F]{64}$') {
    throw "-ExpectedSha256 must be the 64-character digest published next to the package."
}
$ExpectedSha256 = $ExpectedSha256.ToLowerInvariant()

# SAR-F36: bind the caller-supplied package and manifest to an immutable private
# copy before verification, so the archive cannot be swapped between the
# integrity check, layout validation, and extraction (TOCTOU).
$maintenanceRoot = Protect-VistoManagedDirectory -Path (Join-Path $env:ProgramData "Visto")
$inputStaging = Join-Path $maintenanceRoot ("update-input-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $inputStaging -Force | Out-Null
$stagedPackage = Join-Path $inputStaging (Split-Path -Leaf $PackageArchive)
$stagedManifest = Join-Path $inputStaging (Split-Path -Leaf $ManifestFile)
Copy-Item -LiteralPath $PackageArchive -Destination $stagedPackage -Force
Copy-Item -LiteralPath $ManifestFile -Destination $stagedManifest -Force
$inputStaging = Protect-VistoManagedDirectory -Path $inputStaging
$PackageArchive = $stagedPackage
$ManifestFile = $stagedManifest

$verifier = Join-Path $packageRoot "bin\visto-server.exe"
if (-not (Test-Path -LiteralPath $verifier)) { throw "The installed trusted package verifier is unavailable." }
& $verifier update verify-package --artifact $PackageArchive --kind windows-server `
    --platform windows-amd64 --sha256 $ExpectedSha256 | Out-Null
if ($LASTEXITCODE -ne 0) { throw "The update package did not match the published SHA-256." }
Assert-UpdateArchiveLayout -ArchivePath $PackageArchive
$config = Get-Content -LiteralPath (Join-Path $packageRoot "visto-server.json") -Raw | ConvertFrom-Json
$address = [string]$config.address
$staging = Join-Path $maintenanceRoot ("update-staging-" + [guid]::NewGuid().ToString("N"))
$recoveryRoot = Join-Path $maintenanceRoot ("recovery\\" + (Get-Date -Format "yyyyMMdd-HHmmss"))
$dataBackup = $null

New-Item -ItemType Directory -Path $staging, $recoveryRoot -Force | Out-Null
try {
    Expand-Archive -LiteralPath $PackageArchive -DestinationPath $staging -Force
    $backupOutput = & (Join-Path $packageRoot "Backup-VistoServer.ps1") -BackupDirectory $BackupDirectory -TaskName $TaskName
    $backupLine = @($backupOutput | Where-Object { $_ -like "Backup created: *" } | Select-Object -Last 1)
    if ($backupLine.Count -ne 1) { throw "Pre-update data backup did not return its archive path." }
    $dataBackup = $backupLine[0].Substring("Backup created: ".Length)

    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Wait-VistoScheduledTaskStopped -TaskName $TaskName
    Copy-Item -Path (Join-Path $packageRoot "*") -Destination $recoveryRoot -Recurse -Force
    Get-ChildItem -LiteralPath $packageRoot -Force | Remove-Item -Recurse -Force
    Copy-Item -Path (Join-Path $staging "*") -Destination $packageRoot -Recurse -Force
    Start-ScheduledTask -TaskName $TaskName
    Wait-Server -Address $address
    Write-Output "Update completed."
} catch {
    $failure = $_
    Write-Warning "Update failed. Restoring the previous program and data backup."
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Wait-VistoScheduledTaskStopped -TaskName $TaskName
    if (Test-Path -LiteralPath (Join-Path $recoveryRoot "bin\visto-core.exe")) {
        Get-ChildItem -LiteralPath $packageRoot -Force | Remove-Item -Recurse -Force
        Copy-Item -Path (Join-Path $recoveryRoot "*") -Destination $packageRoot -Recurse -Force
    }
    $restore = Join-Path $packageRoot "Restore-VistoServer.ps1"
    if ((Test-Path -LiteralPath $restore) -and $dataBackup) {
        & $restore -BackupFile $dataBackup -TaskName $TaskName -ConfirmRestore
    } else {
        Start-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    }
    throw "Update failed; rollback was attempted: $failure"
} finally {
    if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging -Recurse -Force }
    if (Test-Path -LiteralPath $inputStaging) { Remove-Item -LiteralPath $inputStaging -Recurse -Force }
}
