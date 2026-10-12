Set-StrictMode -Version Latest
function Wait-VistoInstallationReady {
    param([Parameter(Mandatory=$true)][string]$Address)
    for ($attempt = 0; $attempt -lt 45; $attempt++) {
        try {
            $health = Invoke-RestMethod -Uri "http://$Address/health/ready" -TimeoutSec 2
            if ($health.status -eq 'ready' -and $health.dependencies.ffmpeg -eq $true -and $health.dependencies.ffprobe -eq $true) {
                $page = Invoke-WebRequest -UseBasicParsing -Uri "http://$Address/" -TimeoutSec 2
                if ($page.Content -match 'id=["'']root["'']') { return }
            }
        } catch { }
        Start-Sleep -Seconds 1
    }
    throw 'Service, video components or workspace page did not become ready. Keep the files and logs. Retry this unchanged package with Install-VistoServer.ps1 -Repair.'
}
function Get-VistoInstallationMaterial {
    param([Parameter(Mandatory=$true)][string]$PackageRoot)
    $material = [ordered]@{}
    foreach ($file in @('visto-server.json','bin\visto-core.exe','bin\visto-server.exe','Start-VistoServer.ps1','Install-VistoServer.ps1','VistoServer.Security.psm1','VistoServer.Install.psm1')) {
        $material[$file] = (Get-FileHash -LiteralPath (Join-Path $PackageRoot $file) -Algorithm SHA256).Hash
    }
    foreach ($directory in @('web','runtime')) {
        $tree = Join-Path $PackageRoot $directory
        $items = @((Get-Item -LiteralPath $tree -Force)) + @(Get-ChildItem -LiteralPath $tree -Recurse -Force)
        foreach ($item in $items) {
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Linked installation materials are not supported.' }
            if (-not $item.PSIsContainer) {
                $relative = $item.FullName.Substring($PackageRoot.Length).TrimStart([char[]]'\/').Replace('\','/')
                $material[$relative] = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash
            }
        }
    }
    return $material
}
Export-ModuleMember -Function Wait-VistoInstallationReady, Get-VistoInstallationMaterial
