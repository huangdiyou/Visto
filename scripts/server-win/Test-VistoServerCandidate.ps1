param(
    [Parameter(Mandatory = $true)]
    [string]$ArchivePath,
    [string]$ReportPath = "dist\server\windows-candidate-acceptance.json",
    [string]$DiagnosticsPath = "dist\server\windows-candidate-diagnostics.zip"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-Administrator {
    $principal = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "Windows candidate acceptance requires an elevated PowerShell session."
    }
}

function Get-AvailableLoopbackPort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    try {
        $listener.Start()
        return ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    } finally {
        $listener.Stop()
    }
}

function Wait-VistoServerReady {
    param(
        [Parameter(Mandatory = $true)][string]$CliPath,
        [Parameter(Mandatory = $true)][string]$Address,
        [Parameter(Mandatory = $true)][string]$DataDirectory,
        [int]$TimeoutSeconds = 90
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        & $CliPath --address $Address --data-dir $DataDirectory --json status *> $null
        if ($LASTEXITCODE -eq 0) { return }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "Visto Server did not become ready at $Address."
}

Assert-Administrator
$ArchivePath = (Resolve-Path -LiteralPath $ArchivePath).Path
$ReportPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($ReportPath)
$DiagnosticsPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($DiagnosticsPath)
$runId = [guid]::NewGuid().ToString("N")
$taskName = "Visto Server Acceptance $runId"
$port = Get-AvailableLoopbackPort
$address = "127.0.0.1:$port"
$acceptanceRoot = Join-Path $env:ProgramData "Visto\acceptance-$runId"
$packageParent = Join-Path $acceptanceRoot "package"
$dataDir = Join-Path $acceptanceRoot "data"
$backupDir = Join-Path $acceptanceRoot "backups"
$markerPath = Join-Path $dataDir "candidate-acceptance-marker.txt"
$packageRoot = $null
$installed = $false
$checks = [ordered]@{}
$startedAt = (Get-Date).ToUniversalTime()

try {
    New-Item -ItemType Directory -Path $packageParent -Force | Out-Null
    Expand-Archive -LiteralPath $ArchivePath -DestinationPath $packageParent -Force
    if (Test-Path -LiteralPath (Join-Path $packageParent "visto-server.json")) {
        $packageRoot = $packageParent
    } else {
        $packageRoots = @(
            Get-ChildItem -LiteralPath $packageParent -Directory |
                Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName "visto-server.json") }
        )
        if ($packageRoots.Count -ne 1) {
            throw "Candidate archive must expose exactly one Visto Server package root."
        }
        $packageRoot = $packageRoots[0].FullName
    }
    $configPath = Join-Path $packageRoot "visto-server.json"
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $config.address = $address
    $config.dataDir = $dataDir
    $config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding UTF8

    $installScript = Join-Path $packageRoot "Install-VistoServer.ps1"
    $backupScript = Join-Path $packageRoot "Backup-VistoServer.ps1"
    $restoreScript = Join-Path $packageRoot "Restore-VistoServer.ps1"
    $uninstallScript = Join-Path $packageRoot "Uninstall-VistoServer.ps1"
    $cliPath = Join-Path $packageRoot "bin\visto-server.exe"
    $ffmpegPath = Join-Path $packageRoot "runtime\ffmpeg\ffmpeg.exe"
    $ffprobePath = Join-Path $packageRoot "runtime\ffmpeg\ffprobe.exe"
    foreach ($requiredPath in @($installScript, $backupScript, $restoreScript, $uninstallScript, $cliPath, $ffmpegPath, $ffprobePath)) {
        if (-not (Test-Path -LiteralPath $requiredPath)) {
            throw "Candidate package is missing: $requiredPath"
        }
    }

    $ffmpegEncoders = @(& $ffmpegPath -hide_banner -encoders 2>&1)
    if ($LASTEXITCODE -ne 0 -or -not ($ffmpegEncoders -match "libopenh264")) {
        throw "Candidate package FFmpeg runtime does not provide libopenh264."
    }
    & $ffprobePath -version *> $null
    if ($LASTEXITCODE -ne 0) { throw "Candidate package FFprobe runtime cannot start." }
    $checks.mediaRuntime = "passed"

    & $installScript -TaskName $taskName | Out-Null
    $installed = $true
    Wait-VistoServerReady -CliPath $cliPath -Address $address -DataDirectory $dataDir
    $checks.installAndHealth = "passed"

    "before-backup" | Set-Content -LiteralPath $markerPath -Encoding ASCII
    $backupOutput = @(& $backupScript -BackupDirectory $backupDir -TaskName $taskName)
    $backupLine = @($backupOutput | Where-Object { $_ -like "Backup created: *" } | Select-Object -Last 1)
    if ($backupLine.Count -ne 1) {
        throw "Backup script did not return exactly one archive path."
    }
    $backupPath = $backupLine[0].Substring("Backup created: ".Length)
    if (-not (Test-Path -LiteralPath $backupPath) -or -not (Test-Path -LiteralPath "$backupPath.json")) {
        throw "Backup archive or metadata is missing."
    }
    $checks.backup = "passed"

    "after-backup" | Set-Content -LiteralPath $markerPath -Encoding ASCII
    # The restore script prints the precheck verdict (one check per line) to
    # stdout; a rejection exits 11 after printing the reasons. Capture the
    # output so a rejection names its reason in the CI log instead of dying
    # behind an Out-Null.
    $restoreOutput = @(& $restoreScript -BackupFile $backupPath -TaskName $taskName -ConfirmRestore 2>&1 |
        ForEach-Object { [string]$_ })
    $restoreExit = $LASTEXITCODE
    if ($restoreExit -ne 0) {
        throw "Restore failed (exit $restoreExit): $($restoreOutput -join ' | ')"
    }
    Wait-VistoServerReady -CliPath $cliPath -Address $address -DataDirectory $dataDir
    if ((Get-Content -LiteralPath $markerPath -Raw).Trim() -ne "before-backup") {
        throw "Restore did not recover the backed-up marker. Restore output: $($restoreOutput -join ' | ')"
    }
    $checks.restore = "passed"

    $diagnosticsParent = Split-Path -Parent $DiagnosticsPath
    if ($diagnosticsParent) { New-Item -ItemType Directory -Path $diagnosticsParent -Force | Out-Null }
    & $cliPath --address $address --data-dir $dataDir --json diagnostics export --output $DiagnosticsPath *> $null
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $DiagnosticsPath)) {
        throw "Diagnostics export failed."
    }
    $checks.diagnostics = "passed"

    $conflictProcess = Start-Process -FilePath (Join-Path $packageRoot "bin\visto-core.exe") -PassThru -WindowStyle Hidden -Environment @{
        REVIEW_STUDIO_ADDR = $address
        REVIEW_STUDIO_DATA_DIR = (Join-Path $acceptanceRoot "conflict-data")
        REVIEW_STUDIO_WEB_DIR = (Join-Path $packageRoot "web")
        VISTO_HOST_MANAGEMENT_TOKEN = "candidate-conflict-token"
        REVIEW_STUDIO_SOFTWARE_VIDEO_ENCODER = "libopenh264"
        PATH = "$(Join-Path $packageRoot 'runtime\ffmpeg');$env:PATH"
    }
    if (-not $conflictProcess.WaitForExit(15000)) {
        Stop-Process -Id $conflictProcess.Id -Force -ErrorAction SilentlyContinue
        throw "A second Core unexpectedly remained running on the occupied port."
    }
    if ($conflictProcess.ExitCode -eq 0) {
        throw "A second Core unexpectedly accepted the occupied port."
    }
    Wait-VistoServerReady -CliPath $cliPath -Address $address -DataDirectory $dataDir
    $checks.portConflict = "passed"

    & $uninstallScript -TaskName $taskName | Out-Null
    $installed = $false
    if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
        throw "Uninstall left the scheduled task registered."
    }
    if (-not (Test-Path -LiteralPath $markerPath) -or (Get-Content -LiteralPath $markerPath -Raw).Trim() -ne "before-backup") {
        throw "Uninstall did not preserve the candidate data directory."
    }
    $checks.uninstallPreservesData = "passed"

    $reportParent = Split-Path -Parent $ReportPath
    if ($reportParent) { New-Item -ItemType Directory -Path $reportParent -Force | Out-Null }
    [ordered]@{
        schemaVersion = 1
        status = "passed"
        archive = Split-Path -Leaf $ArchivePath
        archiveSha256 = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
        startedAt = $startedAt.ToString("o")
        completedAt = (Get-Date).ToUniversalTime().ToString("o")
        checks = $checks
    } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $ReportPath -Encoding UTF8
    Write-Output "Windows candidate acceptance passed: $ReportPath"
} finally {
    if ($installed) {
        Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    }
    if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
        Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $acceptanceRoot) {
        Remove-Item -LiteralPath $acceptanceRoot -Recurse -Force
    }
}
