param(
    [Parameter(Mandatory = $true)]
    [string]$CoreImage,
    [Parameter(Mandatory = $true)]
    [string]$WebImage,
    [string]$ComposeFile,
    [string]$ProjectName = "visto",
    [string]$BackupDirectory = (Join-Path (Get-Location) "backups"),
    [Int64]$MaximumExpandedBytes = 100GB
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
Import-Module (Join-Path $PSScriptRoot "VistoDocker.psm1") -Force

foreach ($image in @($CoreImage, $WebImage)) {
    if ($image -notmatch '@sha256:[a-fA-F0-9]{64}$') {
        throw "Docker updates require immutable image references with @sha256 digest: $image"
    }
}
if (-not $ComposeFile) { $ComposeFile = Join-Path $PSScriptRoot "..\..\compose.yaml" }
$ComposeFile = (Resolve-Path -LiteralPath $ComposeFile).Path
Assert-VistoDocker -ComposeFile $ComposeFile

$backupOutput = & (Join-Path $PSScriptRoot "Backup-VistoDocker.ps1") -ComposeFile $ComposeFile -ProjectName $ProjectName -BackupDirectory $BackupDirectory
if ($LASTEXITCODE -ne 0) { throw "Update was not started because the pre-update backup failed." }
$backupLine = @($backupOutput | Where-Object { $_ -like "Backup created: *" } | Select-Object -Last 1)
if ($backupLine.Count -ne 1) { throw "The pre-update backup did not return its archive path." }
$backupFile = $backupLine[0].Substring("Backup created: ".Length)

$coreContainer = (& docker compose -f $ComposeFile -p $ProjectName ps -q core).Trim()
$webContainer = (& docker compose -f $ComposeFile -p $ProjectName ps -q web).Trim()
$previousCoreImage = (& docker inspect $coreContainer --format '{{.Image}}').Trim()
$previousWebImage = (& docker inspect $webContainer --format '{{.Image}}').Trim()
$previousCoreSetting = $env:VISTO_CORE_IMAGE
$previousWebSetting = $env:VISTO_WEB_IMAGE

try {
    & docker pull $CoreImage
    if ($LASTEXITCODE -ne 0) { throw "Could not download Core image $CoreImage" }
    & docker pull $WebImage
    if ($LASTEXITCODE -ne 0) { throw "Could not download Web image $WebImage" }
    $env:VISTO_CORE_IMAGE = $CoreImage
    $env:VISTO_WEB_IMAGE = $WebImage
    Invoke-VistoCompose -ComposeFile $ComposeFile -ProjectName $ProjectName -Arguments @("up", "-d", "--no-build")
    Wait-VistoCoreHealthy -ComposeFile $ComposeFile -ProjectName $ProjectName
    Write-Output "Update completed. Core image: $CoreImage; Web image: $WebImage"
} catch {
    $failure = $_
    Write-Warning "Update health check failed. Restoring the pre-update data and prior images."
    $env:VISTO_CORE_IMAGE = $previousCoreImage
    $env:VISTO_WEB_IMAGE = $previousWebImage
    & (Join-Path $PSScriptRoot "Restore-VistoDocker.ps1") -BackupFile $backupFile -ComposeFile $ComposeFile -ProjectName $ProjectName -MaximumExpandedBytes $MaximumExpandedBytes -ConfirmRestore
    if ($LASTEXITCODE -ne 0) { throw "Update failed and automatic restore also failed: $failure" }
    throw "Update failed; the pre-update data and prior images were restored: $failure"
} finally {
    $env:VISTO_CORE_IMAGE = $previousCoreSetting
    $env:VISTO_WEB_IMAGE = $previousWebSetting
}
