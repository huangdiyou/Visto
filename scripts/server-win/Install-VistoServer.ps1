param(
    [string]$TaskName = "Visto Server",
    [switch]$Repair
)

$ErrorActionPreference = "Stop"

function Assert-VistoListenAddressAvailable {
    param([Parameter(Mandatory = $true)][string]$Address)

    if ($Address -notmatch '^(?<host>\[[^\]]+\]|[^:]+):(?<port>\d+)$') {
        throw "Invalid Visto listen address: $Address"
    }
    $listenHost = $Matches['host'].Trim('[', ']')
    $listenPort = [int]$Matches['port']
    if ($listenPort -lt 1 -or $listenPort -gt 65535) { throw "Invalid Visto listen port: $Address" }
    $listeners = @()
    try {
        foreach ($ip in [Net.Dns]::GetHostAddresses($listenHost)) {
            $listener = [Net.Sockets.TcpListener]::new($ip, $listenPort)
            $listeners += $listener
            $listener.ExclusiveAddressUse = $true
            $listener.Start()
        }
        if ($listeners.Count -eq 0) { throw "No local address resolved." }
    } catch {
        throw "Visto cannot bind $Address. Check the address or port conflict before installing; no task was registered and no data directory was created. $($_.Exception.Message)"
    } finally {
        foreach ($listener in $listeners) { $listener.Stop() }
    }
}
$administrator = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $administrator.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Run this installer from an elevated PowerShell window."
}

$PackageRoot = Split-Path -Parent $PSCommandPath
$securityModule = Join-Path $PackageRoot "VistoServer.Security.psm1"
if (-not (Test-Path -LiteralPath $securityModule)) { throw "VistoServer.Security.psm1 is missing." }
Import-Module $securityModule -Force
$existingTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existingTask -and -not $Repair) {
    throw 'A Visto task already exists. For a failed installation of this exact package use -Repair; for another version use the protected update procedure.'
}
if ($Repair -and -not $existingTask) { throw 'No existing Visto task to repair. Run without -Repair for first installation.' }
$PackageRoot = Protect-VistoManagedDirectory -Path $PackageRoot
$runner = Join-Path $PackageRoot "Start-VistoServer.ps1"
if (-not (Test-Path -LiteralPath $runner)) { throw "Start-VistoServer.ps1 is missing." }
$config = Get-Content -LiteralPath (Join-Path $PackageRoot "visto-server.json") -Raw | ConvertFrom-Json
$fingerprintPath = Join-Path $PackageRoot 'visto-install-fingerprint.json'
Import-Module (Join-Path $PackageRoot 'VistoServer.Install.psm1') -Force
$material = Get-VistoInstallationMaterial -PackageRoot $PackageRoot
if ($Repair) {
    $expectedArguments = "-NoProfile -ExecutionPolicy Bypass -File `"$runner`" -Foreground"
    $actions = @($existingTask.Actions)
    if ($actions.Count -ne 1 -or $actions[0].Execute -ne 'powershell.exe' -or $actions[0].Arguments -ne $expectedArguments) {
        throw 'The existing task belongs to another installation. Nothing was restarted.'
    }
    if (-not (Test-Path -LiteralPath $fingerprintPath -PathType Leaf)) { throw 'Original installation fingerprint is missing; use the maintenance instructions.' }
    $record = Get-Content -LiteralPath $fingerprintPath -Raw | ConvertFrom-Json
    if (@($record.PSObject.Properties).Count -ne $material.Count) { throw 'Package file set changed since installation. Repair refused.' }
    foreach ($file in $material.Keys) {
        if ($record.PSObject.Properties.Name -notcontains $file -or $record.$file -ne $material[$file]) { throw 'Package files changed since installation. Repair refused; use the protected update procedure.' }
    }
} else {
    Assert-VistoListenAddressAvailable -Address ([string]$config.address)
}
$dataDir = [Environment]::ExpandEnvironmentVariables([string]$config.dataDir)
$dataDir = Protect-VistoManagedDirectory -Path $dataDir
$hostTokenPath = Join-Path $dataDir "host-management-token.txt"
if ($Repair -and -not (Test-Path -LiteralPath $hostTokenPath -PathType Leaf)) { throw "Existing token is missing; repair did not create a new identity." }
if (-not (Test-Path -LiteralPath $hostTokenPath)) {
    $tokenBytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($tokenBytes) } finally { $rng.Dispose() }
    [Convert]::ToBase64String($tokenBytes) | Set-Content -LiteralPath $hostTokenPath -Encoding ASCII -NoNewline
}
Protect-VistoSecretFile -Path $hostTokenPath
$hostTokenFingerprint = (Get-FileHash -LiteralPath $hostTokenPath -Algorithm SHA256).Hash.Substring(0, 12).ToLowerInvariant()

$installModule = Join-Path $PackageRoot 'VistoServer.Install.psm1'
Import-Module $installModule -Force
if ($Repair) {
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction Stop
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-ScheduledTask -TaskName $TaskName).State -eq 'Running') {
        if ((Get-Date) -ge $deadline) { throw 'Existing task did not stop. Repair aborted.' }
        Start-Sleep -Seconds 1
    }
    Start-ScheduledTask -TaskName $TaskName
    Wait-VistoInstallationReady -Address ([string]$config.address)
    Write-Host "Repaired '$TaskName'; configuration and project data preserved."
    return
}
$material | ConvertTo-Json | Set-Content -LiteralPath $fingerprintPath -Encoding UTF8
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$runner`" -Foreground"
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Days 0)
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Description "Runs the self-hosted Visto Server." | Out-Null
Start-ScheduledTask -TaskName $TaskName
Wait-VistoInstallationReady -Address ([string]$config.address)
Write-Host "Installed and started '$TaskName'."
Write-Host "Host management token file: $hostTokenPath"
Write-Host "Host management token fingerprint: $hostTokenFingerprint"
