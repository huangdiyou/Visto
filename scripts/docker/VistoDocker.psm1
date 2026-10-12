Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-VistoDocker {
    param([string]$ComposeFile)

    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker Engine is required. Install Docker Compose v2 and retry."
    }
    if (-not (Test-Path -LiteralPath $ComposeFile)) {
        throw "Compose file was not found: $ComposeFile"
    }
    & docker compose version | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose v2 is not available."
    }
}

function Invoke-VistoCompose {
    param(
        [string]$ComposeFile,
        [string]$ProjectName,
        [string[]]$Arguments
    )

    & docker compose -f $ComposeFile -p $ProjectName @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose command failed: $($Arguments -join ' ')"
    }
}

function Get-VistoContainerInspection {
    param([string]$ContainerID)

    $inspection = @(& docker inspect $ContainerID) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or -not $inspection) {
        throw "Could not inspect Docker container: $ContainerID"
    }
    return @($inspection)[0]
}

function Get-VistoDataVolume {
    param([string]$ComposeFile, [string]$ProjectName)

    $containerID = (& docker compose -f $ComposeFile -p $ProjectName ps -q core).Trim()
    if (-not $containerID) {
        throw "The Visto Core container is not available. Start the deployment before creating a backup."
    }
    $container = Get-VistoContainerInspection -ContainerID $containerID
    $mount = @($container.Mounts) |
        Where-Object { $_.Type -eq "volume" -and $_.Destination -eq "/var/lib/visto" } |
        Select-Object -First 1
    if (-not $mount -or -not $mount.Name) {
        throw "Could not determine the Visto data volume from the Core container."
    }
    return [string]$mount.Name
}

function Get-VistoCoreDataDir {
    param([string]$ComposeFile, [string]$ProjectName)

    $containerID = (& docker compose -f $ComposeFile -p $ProjectName ps -q core).Trim()
    if (-not $containerID) {
        throw "The Visto Core container is not available. Start the deployment before running this operation."
    }
    $container = Get-VistoContainerInspection -ContainerID $containerID
    $envLine = @($container.Config.Env) |
        Where-Object { $_ -like "REVIEW_STUDIO_DATA_DIR=*" } |
        Select-Object -First 1
    if (-not $envLine) {
        throw "Could not determine the data directory from the Core container environment (REVIEW_STUDIO_DATA_DIR)."
    }
    return [string]$envLine.Substring("REVIEW_STUDIO_DATA_DIR=".Length).Trim()
}

function Wait-VistoCoreHealthy {
    param(
        [string]$ComposeFile,
        [string]$ProjectName,
        [int]$TimeoutSeconds = 90
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $containerID = (& docker compose -f $ComposeFile -p $ProjectName ps -q core).Trim()
        if ($containerID) {
            $container = Get-VistoContainerInspection -ContainerID $containerID
            $health = [string]$container.State.Status
            if ($container.State.PSObject.Properties["Health"] -and $container.State.Health) {
                $health = [string]$container.State.Health.Status
            }
            if ($health -eq "healthy") { return }
            if ($health -eq "exited" -or $health -eq "dead") {
                throw "The Core container stopped while waiting for its health check."
            }
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)

    throw "The Core health check did not succeed within $TimeoutSeconds seconds."
}

function Test-VistoBackupArchive {
    param(
        [Parameter(Mandatory = $true)]
        [string]$UtilityImage,
        [Parameter(Mandatory = $true)]
        [string]$BackupDirectory,
        [Parameter(Mandatory = $true)]
        [string]$BackupName,
        [Int64]$MaximumExpandedBytes = 100GB,
        [int]$MaximumEntryCount = 100000
    )

    if ($MaximumExpandedBytes -le 0) {
        throw "Maximum expanded backup size must be greater than zero."
    }
    if ($MaximumEntryCount -le 0) {
        throw "Maximum backup entry count must be greater than zero."
    }
    $volumeArgument = "${BackupDirectory}:/backup:ro"
    $entries = @(& docker run --rm --user 0 --entrypoint tar --volume $volumeArgument $UtilityImage -tzf "/backup/$BackupName")
    if ($LASTEXITCODE -ne 0 -or -not $entries) {
        throw "Backup archive could not be read. Current data was not changed."
    }
    # A flood of members, including zero-byte ones, can exhaust container memory
    # and inodes even while the expanded byte total stays well under the limit.
    if ($entries.Count -gt $MaximumEntryCount) {
        throw "Backup archive exceeds the entry limit of $MaximumEntryCount. Current data was not changed."
    }
    foreach ($entry in @($entries)) {
        Assert-VistoArchiveEntryName -Entry $entry
    }

    $details = & docker run --rm --user 0 --entrypoint tar --volume $volumeArgument $UtilityImage -tvzf "/backup/$BackupName"
    if ($LASTEXITCODE -ne 0 -or -not $details) {
        throw "Backup archive details could not be read. Current data was not changed."
    }
    [Int64]$expandedBytes = 0
    foreach ($detail in @($details)) {
        $line = [string]$detail
        if ($line -notmatch '^(?<type>[-d])[^\s]*\s+\S+\s+(?<size>\d+)\s+') {
            throw "Backup archive contains an unsupported member type."
        }
        try {
            $memberBytes = [Convert]::ToInt64($Matches["size"])
            if ($memberBytes -lt 0 -or $expandedBytes -gt ([Int64]::MaxValue - $memberBytes)) {
                throw "Backup archive expanded size overflowed Int64."
            }
            $expandedBytes += $memberBytes
        } catch {
            throw "Backup archive expanded size is invalid."
        }
        if ($expandedBytes -gt $MaximumExpandedBytes) {
            throw "Backup archive expands beyond the configured limit of $MaximumExpandedBytes bytes. Current data was not changed."
        }
    }
    return [PSCustomObject]@{
        EntryCount = @($entries).Count
        ExpandedBytes = $expandedBytes
    }
}

function Assert-VistoArchiveEntryName {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Entry
    )

    # Only normalise separators. Do not use TrimStart("./"): that call strips any
    # number of leading '.' and '/' characters, which silently rewrites "../x"
    # into "x" and "/etc/x" into "etc/x" and defeats every guard below.
    $name = $Entry.Replace("\", "/")
    if (-not $name) { return }

    # Absolute paths, UNC paths and Windows drive letters are rejected before any
    # prefix handling, so trimming can never hide them.
    if ($name.StartsWith("/") -or $name.StartsWith("//") -or $name.Contains(":")) {
        throw "Backup archive contains an unsafe path: $Entry"
    }

    # Remove only legitimate leading "./" segments, one at a time.
    while ($name.StartsWith("./")) {
        $name = $name.Substring(2)
    }
    if (-not $name) { return }

    $segments = $name.Split("/", [StringSplitOptions]::RemoveEmptyEntries)
    if ($segments.Count -eq 0) { return }
    if ($segments -contains "..") {
        throw "Backup archive contains an unsafe path: $Entry"
    }
}

Export-ModuleMember -Function Assert-VistoDocker, Invoke-VistoCompose, Get-VistoDataVolume, Get-VistoCoreDataDir, Wait-VistoCoreHealthy, Test-VistoBackupArchive, Assert-VistoArchiveEntryName
