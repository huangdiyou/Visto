Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$script:VistoWriteSIDs = @("S-1-5-18", "S-1-5-32-544")
$script:VistoInheritance = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit
$script:VistoWriteMask = [Security.AccessControl.FileSystemRights]::FullControl

function Assert-VistoManagedDirectory {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [switch]$Create
    )

    if ($Create) { New-Item -ItemType Directory -Path $Path -Force | Out-Null }
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if (-not $item.PSIsContainer) { throw "Expected a directory: $Path" }
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing a reparse-point directory: $Path"
    }
    return $item.FullName
}

function Protect-VistoManagedDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)

    $resolved = Assert-VistoManagedDirectory -Path $Path -Create
    $items = @((Get-Item -LiteralPath $resolved -Force)) + @(Get-ChildItem -LiteralPath $resolved -Recurse -Force)
    foreach ($item in $items) {
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Refusing a reparse point below managed directory: $($item.FullName)"
        }
        if ($item.PSIsContainer) {
            $acl = New-Object Security.AccessControl.DirectorySecurity
            $inheritance = $script:VistoInheritance
        } else {
            $acl = New-Object Security.AccessControl.FileSecurity
            $inheritance = [Security.AccessControl.InheritanceFlags]::None
        }
        $acl.SetAccessRuleProtection($true, $false)
        foreach ($sid in $script:VistoWriteSIDs) {
            $identity = [Security.Principal.SecurityIdentifier]::new($sid)
            $rule = New-Object Security.AccessControl.FileSystemAccessRule(
                $identity,
                [Security.AccessControl.FileSystemRights]::FullControl,
                $inheritance,
                [Security.AccessControl.PropagationFlags]::None,
                [Security.AccessControl.AccessControlType]::Allow
            )
            $acl.AddAccessRule($rule)
        }
        Set-Acl -LiteralPath $item.FullName -AclObject $acl
    }
    return $resolved
}

function Assert-VistoManagedDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    $resolved = Assert-VistoManagedDirectory -Path $Path
    $acl = Get-Acl -LiteralPath $resolved
    foreach ($rule in $acl.Access) {
        if ($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow) { continue }
        $sid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
        if (($rule.FileSystemRights -band $script:VistoWriteMask) -ne 0 -and $sid -notin $script:VistoWriteSIDs) {
            throw "Directory grants write access outside SYSTEM and Administrators: $resolved ($sid)"
        }
    }
    return $resolved
}

function Protect-VistoSecretFile {
    param([Parameter(Mandatory = $true)][string]$Path)

    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing a reparse-point secret file: $Path"
    }
    $acl = New-Object Security.AccessControl.FileSecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($sid in $script:VistoWriteSIDs) {
        $identity = [Security.Principal.SecurityIdentifier]::new($sid)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule(
            $identity,
            [Security.AccessControl.FileSystemRights]::FullControl,
            [Security.AccessControl.AccessControlType]::Allow
        )
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $item.FullName -AclObject $acl
}

function Wait-VistoScheduledTaskStopped {
    param(
        [Parameter(Mandatory = $true)][string]$TaskName,
        [int]$TimeoutSeconds = 30
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        if (-not $task -or [string]$task.State -notin @("Running", "Queued")) {
            return
        }
        Start-Sleep -Milliseconds 200
    } while ((Get-Date) -lt $deadline)

    throw "Scheduled task '$TaskName' did not stop within $TimeoutSeconds seconds."
}

function Wait-VistoFileUnlocked {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [int]$TimeoutSeconds = 30
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if (-not (Test-Path -LiteralPath $Path)) { return }
        $stream = $null
        try {
            $stream = [IO.File]::Open(
                $Path,
                [IO.FileMode]::Open,
                [IO.FileAccess]::ReadWrite,
                [IO.FileShare]::None
            )
            return
        } catch [IO.IOException] {
            # Stop-ScheduledTask is asynchronous; wait until the Core process
            # has actually released SQLite before copying the data directory.
        } finally {
            if ($stream) { $stream.Dispose() }
        }
        Start-Sleep -Milliseconds 200
    } while ((Get-Date) -lt $deadline)

    throw "File remained in use after the Visto Server task stopped: $Path"
}

# Validates one archive member name and returns its normalised form.
# Do not use TrimStart("./"): that call strips any number of leading '.' and
# '/' characters, which silently rewrites "../x" into "x" and "/etc/x" into
# "etc/x" and defeats every guard below (SAR-F58).
function Assert-VistoServerArchiveEntryName {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Entry
    )

    $name = $Entry.Replace("\", "/")
    if (-not $name) { return "" }

    # Absolute paths, UNC paths and Windows drive letters are rejected before
    # any prefix handling, so trimming can never hide them.
    if ($name.StartsWith("/") -or $name -match '^[A-Za-z]:' -or $name.Contains(":")) {
        throw "Archive contains an unsafe path: $Entry"
    }

    # Remove only legitimate leading "./" segments, one at a time.
    while ($name.StartsWith("./")) {
        $name = $name.Substring(2)
    }
    if (-not $name) { return "" }

    $segments = $name.Split("/", [StringSplitOptions]::RemoveEmptyEntries)
    if ($segments -contains "..") {
        throw "Archive contains an unsafe path: $Entry"
    }
    return $name
}

Export-ModuleMember -Function Assert-VistoManagedDirectory, Protect-VistoManagedDirectory, Assert-VistoManagedDirectoryAcl, Protect-VistoSecretFile, Wait-VistoScheduledTaskStopped, Wait-VistoFileUnlocked, Assert-VistoServerArchiveEntryName
