param(
    [Parameter(Mandatory = $true)][string]$PackageArchive,
    [string]$BackupDirectory = "$env:ProgramData\Visto\backups",
    [string]$TaskName = "Visto Server",
    [string]$InstallDirectory
)
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$archive = (Resolve-Path -LiteralPath $PackageArchive).Path
$checksum = "$archive.sha256"
$manifest = "$archive.manifest.json"
if (-not (Test-Path -LiteralPath $checksum -PathType Leaf)) { throw "Download the complete update: its checksum file is missing." }
if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) { throw "Download the complete update: its version manifest is missing." }
$records = @(Get-Content -LiteralPath $checksum)
if ($records.Count -eq 1 -and $records[0] -match '^([a-fA-F0-9]{64})[ \t]+\*?(.+)$') {
    $digest = $Matches[1]
    $fileName = $Matches[2]
} elseif ($records.Count -eq 2 -and $records[0] -cmatch '^SHA256  (.+)$' -and $records[1] -match '^[a-fA-F0-9]{64}$') {
    $fileName = $records[0].Substring(8)
    $digest = $records[1]
} else { throw "The checksum file is invalid." }
if ($fileName -cne [IO.Path]::GetFileName($archive)) { throw "The checksum belongs to a different archive." }
& (Join-Path $PSScriptRoot "Update-VistoServer.ps1") -PackageArchive $archive `
    -ExpectedSha256 $digest -ManifestFile $manifest -BackupDirectory $BackupDirectory -TaskName $TaskName -InstallDirectory $InstallDirectory
