param(
    [string]$TaskName = "Visto Server",
    [switch]$RemoveData
)

$ErrorActionPreference = "Stop"
$administrator = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $administrator.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run this uninstaller from an elevated PowerShell window."
}

$PackageRoot = Split-Path -Parent $PSCommandPath
$securityModule = Join-Path $PackageRoot "VistoServer.Security.psm1"
if (-not (Test-Path -LiteralPath $securityModule)) { throw "VistoServer.Security.psm1 is missing." }
Import-Module $securityModule -Force
$PackageRoot = Assert-VistoManagedDirectoryAcl -Path $PackageRoot
$config = Get-Content -LiteralPath (Join-Path $PackageRoot "visto-server.json") -Raw | ConvertFrom-Json
Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
Wait-VistoScheduledTaskStopped -TaskName $TaskName
Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue

if ($RemoveData) {
    $dataDir = [Environment]::ExpandEnvironmentVariables([string]$config.dataDir)
    if ($dataDir -notlike "$env:ProgramData\Visto\*") { throw "Refusing to delete a data directory outside ProgramData\\Visto." }
    if (Test-Path -LiteralPath $dataDir) {
        $dataDir = Assert-VistoManagedDirectoryAcl -Path $dataDir
        Remove-Item -LiteralPath $dataDir -Recurse -Force
    }
    Write-Host "Visto Server was removed with its data."
} else {
    Write-Host "Visto Server was removed. Data was kept."
}
