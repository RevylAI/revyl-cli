# Installs this plugin under Cursor's local plugin root.

[CmdletBinding()]
param(
    [ValidateSet("Copy", "Link")]
    [string]$Mode = "Copy",
    [switch]$DryRun,
    [switch]$Status
)

$ErrorActionPreference = "Stop"

$PluginName = "revyl"
$SourceDirectory = $PSScriptRoot
$LocalPluginRoot = if ($env:CURSOR_PLUGIN_LOCAL_DIR) {
    $env:CURSOR_PLUGIN_LOCAL_DIR
} else {
    Join-Path $HOME ".cursor\plugins\local"
}
$Destination = Join-Path $LocalPluginRoot $PluginName
$Staging = Join-Path $LocalPluginRoot ".$PluginName.install.$PID"
$Previous = Join-Path $LocalPluginRoot ".$PluginName.previous.$PID"
$LinkedDirectories = @(".cursor-plugin", "assets", "hooks", "rules", "skills")
$MutationStarted = $false

# Resolve-RevylBinary returns a selected command or file as an absolute executable path.
function Resolve-RevylBinary {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RequestedBinary
    )

    if (Test-Path -LiteralPath $RequestedBinary) {
        $Item = Get-Item -LiteralPath $RequestedBinary
        if ($Item.PSIsContainer) {
            throw "REVYL_BINARY is not an executable file: $RequestedBinary"
        }
        return $Item.FullName
    }

    $Command = Get-Command -Name $RequestedBinary -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $Command) {
        throw "REVYL_BINARY is not executable: $RequestedBinary"
    }
    return [System.IO.Path]::GetFullPath($Command.Source)
}

# Set-InstalledRuntimeOverride rewrites only the staged Revyl binary override.
function Set-InstalledRuntimeOverride {
    param(
        [Parameter(Mandatory = $true)]
        [string]$McpPath,
        [Parameter(Mandatory = $true)]
        [string]$SelectedBinary
    )

    $McpConfig = Get-Content -LiteralPath $McpPath -Raw | ConvertFrom-Json
    if (
        $null -eq $McpConfig.mcpServers.revyl -or
        $McpConfig.mcpServers.revyl.env.REVYL_BINARY -ne '${env:REVYL_BINARY}'
    ) {
        throw "mcp.json has no REVYL_BINARY override to rewrite."
    }

    $McpConfig.mcpServers.revyl.env.REVYL_BINARY = $SelectedBinary
    $SerializedConfig = $McpConfig | ConvertTo-Json -Depth 100
    $Utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($McpPath, "$SerializedConfig`n", $Utf8WithoutBom)
}

# Test-SourceArtifact validates the maintained plugin before local mutation.
function Test-SourceArtifact {
    $ManifestPath = Join-Path $SourceDirectory ".cursor-plugin\plugin.json"
    if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) {
        throw "source artifact has no .cursor-plugin/plugin.json."
    }
    $Manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
    if ($Manifest.name -ne $PluginName) {
        throw "source manifest does not declare the revyl plugin."
    }
    if (-not (Test-Path -LiteralPath (Join-Path $SourceDirectory "mcp.json") -PathType Leaf)) {
        throw "source artifact has no mcp.json."
    }
}

# Remove-PluginTree removes a local plugin without traversing linked worktree directories.
function Remove-PluginTree {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if (-not (Test-Path -LiteralPath $Path)) {
        return
    }
    $RootItem = Get-Item -LiteralPath $Path -Force
    if (($RootItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        [System.IO.Directory]::Delete($RootItem.FullName)
        return
    }
    foreach ($RelativePath in $LinkedDirectories) {
        $Candidate = Join-Path $Path $RelativePath
        if (-not (Test-Path -LiteralPath $Candidate)) {
            continue
        }
        $CandidateItem = Get-Item -LiteralPath $Candidate -Force
        if (($CandidateItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            [System.IO.Directory]::Delete($CandidateItem.FullName)
        }
    }
    Remove-Item -LiteralPath $Path -Recurse -Force
}

# Add-WorktreeLinks replaces copied development surfaces with directory junctions.
function Add-WorktreeLinks {
    foreach ($RelativePath in $LinkedDirectories) {
        $LinkedPath = Join-Path $Staging $RelativePath
        Remove-Item -LiteralPath $LinkedPath -Recurse -Force
        New-Item `
            -ItemType Junction `
            -Path $LinkedPath `
            -Target (Join-Path $SourceDirectory $RelativePath) |
            Out-Null
    }
}

# Show-LocalInstallStatus reports the active local plugin without changing it.
function Show-LocalInstallStatus {
    Write-Output "Revyl local plugin status"
    Write-Output "  Destination: $Destination"
    if (-not (Test-Path -LiteralPath $Destination -PathType Container)) {
        Write-Output "  Installed: no"
        return
    }

    $InstalledMode = "copy"
    $InstalledSource = "copied artifact"
    $ManifestDirectory = Join-Path $Destination ".cursor-plugin"
    if (Test-Path -LiteralPath $ManifestDirectory -PathType Container) {
        $ManifestItem = Get-Item -LiteralPath $ManifestDirectory -Force
        if (($ManifestItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            $InstalledMode = "link"
            $Target = @($ManifestItem.Target)[0]
            if ($Target) {
                $InstalledSource = Split-Path -Path $Target -Parent
            }
        }
    }

    $RuntimeOverride = "unavailable"
    $McpPath = Join-Path $Destination "mcp.json"
    if (Test-Path -LiteralPath $McpPath -PathType Leaf) {
        $McpConfig = Get-Content -LiteralPath $McpPath -Raw | ConvertFrom-Json
        if ($McpConfig.mcpServers.revyl.env.REVYL_BINARY) {
            $RuntimeOverride = [string]$McpConfig.mcpServers.revyl.env.REVYL_BINARY
        }
    }

    Write-Output "  Installed: yes"
    Write-Output "  Mode: $InstalledMode"
    Write-Output "  Source: $InstalledSource"
    Write-Output "  Revyl binary: $RuntimeOverride"
}

if ($Status) {
    Show-LocalInstallStatus
    exit 0
}

try {
    Test-SourceArtifact

    $SelectedBinary = $null
    if ($env:REVYL_BINARY) {
        $SelectedBinary = Resolve-RevylBinary -RequestedBinary $env:REVYL_BINARY
    } elseif ($Mode -eq "Link") {
        throw "REVYL_BINARY is required for link mode."
    }

    if ($DryRun) {
        Write-Output "Revyl local plugin dry run"
        Write-Output "  Mode: $($Mode.ToLowerInvariant())"
        Write-Output "  Source: $SourceDirectory"
        Write-Output "  Destination: $Destination"
        if ($SelectedBinary) {
            Write-Output "  Revyl binary: $SelectedBinary"
        } else {
            Write-Output "  Revyl binary: plugin-pinned runtime"
        }
        Write-Output "  Changes: none"
        exit 0
    }

    $MutationStarted = $true
    New-Item -ItemType Directory -Path $LocalPluginRoot -Force | Out-Null
    Remove-PluginTree -Path $Staging
    New-Item -ItemType Directory -Path $Staging | Out-Null
    Get-ChildItem -LiteralPath $SourceDirectory -Force |
        Copy-Item -Destination $Staging -Recurse -Force

    if ($Mode -eq "Link") {
        Add-WorktreeLinks
    }

    $ManifestPath = Join-Path $Staging ".cursor-plugin\plugin.json"
    if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) {
        throw "staged artifact has no .cursor-plugin/plugin.json."
    }

    $Manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
    if ($Manifest.name -ne $PluginName) {
        throw "staged manifest does not declare the revyl plugin."
    }

    if ($SelectedBinary) {
        Set-InstalledRuntimeOverride `
            -McpPath (Join-Path $Staging "mcp.json") `
            -SelectedBinary $SelectedBinary
    }

    Remove-PluginTree -Path $Previous
    $HadPrevious = Test-Path -LiteralPath $Destination
    if ($HadPrevious) {
        Move-Item -LiteralPath $Destination -Destination $Previous
    }
    try {
        Move-Item -LiteralPath $Staging -Destination $Destination
    } catch {
        if ($HadPrevious -and -not (Test-Path -LiteralPath $Destination)) {
            Move-Item -LiteralPath $Previous -Destination $Destination
        }
        throw
    }
    Remove-PluginTree -Path $Previous

    if ($Mode -eq "Link") {
        Write-Output "Linked Revyl plugin from $SourceDirectory"
    } else {
        Write-Output "Installed Revyl plugin copy from $SourceDirectory"
    }
    Write-Output "Local plugin destination: $Destination"
    Write-Output "In Cursor, run: Developer: Reload Window"
} catch {
    Write-Error "Revyl plugin install failed: $($_.Exception.Message)"
    exit 1
} finally {
    if ($MutationStarted) {
        Remove-PluginTree -Path $Staging
    }
}
