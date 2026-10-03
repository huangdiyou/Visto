param(
    [string]$TaskName = "Visto Server"
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
$PackageRoot = Protect-VistoManagedDirectory -Path $PackageRoot
$runner = Join-Path $PackageRoot "Start-VistoServer.ps1"
if (-not (Test-Path -LiteralPath $runner)) { throw "Start-VistoServer.ps1 is missing." }
$config = Get-Content -LiteralPath (Join-Path $PackageRoot "visto-server.json") -Raw | ConvertFrom-Json
Assert-VistoListenAddressAvailable -Address ([string]$config.address)
$dataDir = [Environment]::ExpandEnvironmentVariables([string]$config.dataDir)
$dataDir = Protect-VistoManagedDirectory -Path $dataDir
$hostTokenPath = Join-Path $dataDir "host-management-token.txt"
if (-not (Test-Path -LiteralPath $hostTokenPath)) {
    $tokenBytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($tokenBytes) } finally { $rng.Dispose() }
    [Convert]::ToBase64String($tokenBytes) | Set-Content -LiteralPath $hostTokenPath -Encoding ASCII -NoNewline
}
Protect-VistoSecretFile -Path $hostTokenPath
$hostTokenFingerprint = (Get-FileHash -LiteralPath $hostTokenPath -Algorithm SHA256).Hash.Substring(0, 12).ToLowerInvariant()

$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$runner`" -Foreground"
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Days 0)
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest

Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Description "Runs the self-hosted Visto Server." | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "Installed and started '$TaskName'."
Write-Host "Host management token file: $hostTokenPath"
Write-Host "Host management token fingerprint: $hostTokenFingerprint"
