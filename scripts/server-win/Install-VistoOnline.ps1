param(
    [string]$Package,
    [string]$Destination = (Join-Path $env:ProgramFiles 'Visto Server'),
    [switch]$CheckOnly,
    [switch]$NonInteractive,
    [switch]$ConfirmDownload
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$repository = 'https://github.com/huangdiyou/Visto'
if ($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitOperatingSystem) {
    throw 'Supported platform: Windows x64.'
}
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') {
    throw 'Windows ARM64 is not supported by this package.'
}
if (-not $CheckOnly) {
    $identity = [Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $identity.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw '请在以管理员身份运行的 Windows PowerShell 中执行安装命令。'
    }
    if (Get-ScheduledTask -TaskName 'Visto Server' -ErrorAction SilentlyContinue) {
        throw '已存在 Visto Server。请按更新与维护指南操作，现有实例未修改。'
    }
    if ((Test-Path -LiteralPath $Destination) -and @(Get-ChildItem -LiteralPath $Destination -Force).Count -gt 0) {
        throw '安装目录已有内容，请保留它并按维护指南操作。'
    }
    if ($NonInteractive -and -not $ConfirmDownload) {
        throw 'Unattended installation needs -ConfirmDownload.'
    }
}
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$work = Join-Path ([IO.Path]::GetTempPath()) ('visto-install-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
    if ($Package) {
        $Package = (Resolve-Path -LiteralPath $Package).Path
        $name = [IO.Path]::GetFileName($Package)
        if ($name -notmatch '^Visto-Server_(?<version>\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?)_windows-x64\.zip$') {
            throw '请保留 Windows 安装包的原始文件名。'
        }
        $version = $Matches.version
        $tag = 'v' + $version
        Write-Host "使用已下载的 Visto $version，不获取其他版本。"
    } else {
        # Read Latest once, then download every asset from the same fixed tag.
        $release = Invoke-RestMethod -Uri 'https://api.github.com/repos/huangdiyou/Visto/releases/latest' -Headers @{'Accept'='application/vnd.github+json'; 'Cache-Control'='no-cache'} -TimeoutSec 60
        if ($release.draft -or $release.prerelease -or $release.tag_name -notmatch '^v?(?<version>\d+\.\d+\.\d+(?:\+[A-Za-z0-9.-]+)?)$') {
            throw 'GitHub 没有返回可安装的最新正式版。'
        }
        $version = $Matches.version
        $tag = [string]$release.tag_name
        $name = "Visto-Server_${version}_windows-x64.zip"
        if (@($release.assets | Where-Object {$_.name -eq $name}).Count -ne 1) {
            throw '最新正式版尚未提供 Windows 安装包。'
        }
        $Package = Join-Path $work $name
        Write-Host "最新正式版：Visto $version · Windows x64"
        Invoke-WebRequest -UseBasicParsing -Uri "$repository/releases/download/$tag/$name" -OutFile $Package -TimeoutSec 600
    }
    $checksum = Join-Path $work 'checksum'
    if (Test-Path -LiteralPath "$Package.sha256") {
        Copy-Item -LiteralPath "$Package.sha256" -Destination $checksum
    } else {
        Invoke-WebRequest -UseBasicParsing -Uri "$repository/releases/download/$tag/$name.sha256" -OutFile $checksum -TimeoutSec 60
    }
    $pattern = '^([a-fA-F0-9]{64})[ \t]+\*?' + [Regex]::Escape($name) + '(?:\r?\n)?$'
    $sidecar = Get-Content -LiteralPath $checksum -Raw
    $legacyPattern = '^SHA256[ \t]+' + [Regex]::Escape($name) + '\r?\n([a-fA-F0-9]{64})(?:\r?\n)?$'
    if ($sidecar -match $pattern) { $expected = $Matches[1] }
    elseif ($sidecar -match $legacyPattern) { $expected = $Matches[1] }
    else { throw '校验文件与所选安装包不匹配。' }
    if ((Get-FileHash -LiteralPath $Package -Algorithm SHA256).Hash -ne $expected) {
        throw '安装包验证失败，没有开始安装。'
    }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($Package)
    try {
        $seen = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        foreach ($entry in $zip.Entries) {
            $path = $entry.FullName.Replace('\', '/')
            if ($path -match '(^/|^[A-Za-z]:|(^|/)\.\.(/|$)|:|[\x00-\x1f]|[ .]($|/))') { throw '压缩包包含不安全路径。' }
            if (-not $seen.Add($path.TrimEnd('/'))) { throw '压缩包包含重复路径。' }
            $kind = ($entry.ExternalAttributes -shr 16) -band 0xF000
            if ($kind -ne 0 -and $kind -ne 0x8000 -and $kind -ne 0x4000) { throw '压缩包包含链接或特殊文件。' }
        }
    } finally { $zip.Dispose() }
    $payload = Join-Path $work 'package'
    [IO.Compression.ZipFile]::ExtractToDirectory($Package, $payload)
    foreach ($file in @('Install-VistoServer.ps1','VistoServer.Security.psm1','visto-server.json','bin\visto-core.exe','web\index.html','runtime\ffmpeg\ffmpeg.exe','runtime\ffmpeg\ffprobe.exe')) {
        if (-not (Test-Path -LiteralPath (Join-Path $payload $file) -PathType Leaf)) { throw "安装包缺少 $file" }
    }
    $packageConfig = Get-Content -LiteralPath (Join-Path $payload 'visto-server.json') -Raw | ConvertFrom-Json
    if ($packageConfig.version -ne $version) { throw '安装包版本信息与所选发行版不一致。' }
    if ($CheckOnly) {
        Write-Host "已检查 Visto $version · Windows x64 的安装材料；没有安装或修改计划任务。"
        return
    }
    Write-Host "将安装 Visto $version 至 $Destination，视频组件已包含在包中。"
    if (-not $ConfirmDownload) {
        $answer = Read-Host '继续安装？[Y/n]'
        if ($answer -notin @('', 'y', 'Y', 'yes', 'YES')) { Write-Host '已取消，没有开始安装。'; return }
    }
    Import-Module (Join-Path $payload 'VistoServer.Security.psm1') -Force
    $Destination = Protect-VistoManagedDirectory -Path $Destination
    Get-ChildItem -LiteralPath $payload -Force | Copy-Item -Destination $Destination -Recurse
    & (Join-Path $Destination 'Install-VistoServer.ps1')
    $config = Get-Content -LiteralPath (Join-Path $Destination 'visto-server.json') -Raw | ConvertFrom-Json
    if ($config.address -notmatch '^127\.0\.0\.1:\d+$') { throw '请检查工作空间监听地址。' }
    $address = [string]$config.address
    $installModule = Join-Path $Destination 'VistoServer.Install.psm1'
    if (Test-Path -LiteralPath $installModule -PathType Leaf) {
        # New packages own the same readiness contract for online and manual routes.
        # Install-VistoServer has already executed this check before returning.
    } else {
        # Released 1.0.3 has no module; retain the website compatibility check.
        $ready = $false
        for ($attempt = 0; $attempt -lt 45; $attempt++) {
            try {
                $health = Invoke-RestMethod -Uri "http://$address/health/ready" -TimeoutSec 2
                $page = Invoke-WebRequest -UseBasicParsing -Uri "http://$address/" -TimeoutSec 2
                if ($health.status -eq 'ready' -and $health.dependencies.ffmpeg -eq $true -and $health.dependencies.ffprobe -eq $true -and $page.Content -match 'id=["'']root["'']') { $ready = $true; break }
            } catch { }
            Start-Sleep -Seconds 1
        }
        if (-not $ready) { throw '服务或视频组件尚未就绪，请保留日志并按维护指南排查。' }
    }
    Write-Host "安装完成。打开 http://$address/ 创建工作空间。无需额外安装视频组件。"
} catch {
    Write-Host '安装未完成。请保留上方错误和已安装文件，不要删除已有项目数据。'
    if (Test-Path -LiteralPath (Join-Path $Destination 'visto-install-fingerprint.json')) {
        Write-Host "同一份新包可重试：在安装目录运行 powershell -NoProfile -ExecutionPolicy Bypass -File .\Install-VistoServer.ps1 -Repair"
    }
    throw
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
