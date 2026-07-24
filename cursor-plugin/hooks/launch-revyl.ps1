<#
.SYNOPSIS
Resolves the plugin-pinned Revyl runtime and starts its MCP server.

.PARAMETER RevylArguments
Arguments forwarded unchanged to the selected Revyl executable.
#>
[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $RevylArguments
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = (
    [Net.ServicePointManager]::SecurityProtocol -bor
    [Net.SecurityProtocolType]::Tls12
)

# Write-BootstrapError reports failures exclusively on stderr.
function Write-BootstrapError {
    param(
        [Parameter(Mandatory = $true)]
        [string] $Message
    )

    [Console]::Error.WriteLine("Revyl plugin runtime error: $Message")
}

# Resolve-OverrideBinary returns an explicit runtime override as an absolute path.
function Resolve-OverrideBinary {
    param(
        [Parameter(Mandatory = $true)]
        [string] $RequestedBinary
    )

    if (Test-Path -LiteralPath $RequestedBinary -PathType Leaf) {
        return (Get-Item -LiteralPath $RequestedBinary).FullName
    }

    $command = Get-Command `
        -Name $RequestedBinary `
        -CommandType Application `
        -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        throw "REVYL_BINARY is not executable: $RequestedBinary"
    }
    return [IO.Path]::GetFullPath($command.Source)
}

# Get-RuntimeArchitecture maps the native Windows architecture to release naming.
function Get-RuntimeArchitecture {
    $detectedArchitecture = if (
        -not [string]::IsNullOrWhiteSpace($env:PROCESSOR_ARCHITEW6432)
    ) {
        $env:PROCESSOR_ARCHITEW6432
    }
    else {
        $env:PROCESSOR_ARCHITECTURE
    }

    switch ($detectedArchitecture.ToUpperInvariant()) {
        { $_ -in @("AMD64", "X64", "X86_64") } { return "amd64" }
        { $_ -in @("ARM64", "AARCH64") } { return "arm64" }
        default { throw "unsupported Windows architecture: $detectedArchitecture" }
    }
}

# Test-RuntimeChecksum verifies one cached or downloaded executable.
function Test-RuntimeChecksum {
    param(
        [Parameter(Mandatory = $true)]
        [string] $Path,
        [Parameter(Mandatory = $true)]
        [string] $ExpectedChecksum
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return $false
    }
    $actualChecksum = (
        Get-FileHash -LiteralPath $Path -Algorithm SHA256
    ).Hash.ToLowerInvariant()
    return [string]::Equals(
        $actualChecksum,
        $ExpectedChecksum,
        [StringComparison]::Ordinal
    )
}

# Invoke-RuntimeDownload downloads one bounded HTTPS artifact.
function Invoke-RuntimeDownload {
    param(
        [Parameter(Mandatory = $true)]
        [uri] $Uri,
        [Parameter(Mandatory = $true)]
        [string] $Destination
    )

    Invoke-WebRequest `
        -Uri $Uri `
        -OutFile $Destination `
        -UseBasicParsing `
        -UserAgent "revyl-cursor-plugin" `
        -TimeoutSec 180 | Out-Null

    if (
        -not (Test-Path -LiteralPath $Destination -PathType Leaf) -or
        (Get-Item -LiteralPath $Destination).Length -eq 0
    ) {
        throw "the runtime download was empty"
    }
}

# Install-RuntimeAtomically publishes a verified temporary executable.
function Install-RuntimeAtomically {
    param(
        [Parameter(Mandatory = $true)]
        [string] $Source,
        [Parameter(Mandatory = $true)]
        [string] $Destination
    )

    if (Test-Path -LiteralPath $Destination -PathType Leaf) {
        $backup = "$Destination.backup.$PID"
        try {
            [IO.File]::Replace($Source, $Destination, $backup, $true)
        }
        finally {
            Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
        }
        return
    }

    try {
        [IO.File]::Move($Source, $Destination)
    }
    catch {
        if (-not (Test-Path -LiteralPath $Destination -PathType Leaf)) {
            throw
        }
        Remove-Item -LiteralPath $Source -Force -ErrorAction SilentlyContinue
    }
}

# Invoke-RevylRuntime forwards stdio and returns the child exit status.
function Invoke-RevylRuntime {
    param(
        [Parameter(Mandatory = $true)]
        [string] $BinaryPath,
        [string[]] $Arguments
    )

    $env:REVYL_MCP_EXECUTABLE = $BinaryPath
    & $BinaryPath @Arguments
    if ($null -eq $LASTEXITCODE) {
        return 0
    }
    return $LASTEXITCODE
}

$temporaryPath = $null
try {
    if ($env:REVYL_API_KEY -eq '${env:REVYL_API_KEY}') {
        Remove-Item -Path Env:REVYL_API_KEY -ErrorAction SilentlyContinue
    }

    if (
        -not [string]::IsNullOrWhiteSpace($env:REVYL_BINARY) -and
        $env:REVYL_BINARY -ne '${env:REVYL_BINARY}'
    ) {
        $overrideBinary = Resolve-OverrideBinary -RequestedBinary $env:REVYL_BINARY
        exit (Invoke-RevylRuntime -BinaryPath $overrideBinary -Arguments $RevylArguments)
    }

    $pluginDirectory = Split-Path -Parent $PSScriptRoot
    $manifestPath = if (
        -not [string]::IsNullOrWhiteSpace($env:REVYL_RUNTIME_MANIFEST)
    ) {
        $env:REVYL_RUNTIME_MANIFEST
    }
    else {
        Join-Path -Path $pluginDirectory -ChildPath "runtime-manifest.json"
    }
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "runtime manifest not found at $manifestPath"
    }

    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ([int] $manifest.schema_version -ne 1) {
        throw "unsupported runtime manifest schema: $($manifest.schema_version)"
    }
    if (-not [bool] $manifest.prepared) {
        throw "this plugin release has no prepared runtime; reinstall or update the Revyl plugin"
    }

    $pluginVersion = [string] $manifest.plugin_version
    $runtimeVersion = [string] $manifest.runtime_version
    $releaseTag = [string] $manifest.release_tag
    $releaseBaseUrl = [string] $manifest.release_base_url
    $semanticVersionPattern = "^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$"
    if ($pluginVersion -notmatch $semanticVersionPattern) {
        throw "invalid plugin version in runtime manifest"
    }
    if ($runtimeVersion -notmatch $semanticVersionPattern) {
        throw "invalid runtime version in runtime manifest"
    }
    if ($releaseTag -ne "v$runtimeVersion") {
        throw "runtime release tag does not match its version"
    }

    $expectedBaseUrl = "https://github.com/RevylAI/revyl-cli/releases/download/$releaseTag"
    if ($releaseBaseUrl -ne $expectedBaseUrl) {
        throw "runtime release URL is not immutable"
    }

    $architecture = Get-RuntimeArchitecture
    $platform = "windows_$architecture"
    $assetProperty = "${platform}_asset"
    $checksumProperty = "${platform}_sha256"
    $asset = [string] $manifest.$assetProperty
    $expectedChecksum = ([string] $manifest.$checksumProperty).ToLowerInvariant()
    if ($asset -notmatch "^revyl-windows-(amd64|arm64)\.exe$") {
        throw "runtime manifest contains an invalid asset name for $platform"
    }
    if ($expectedChecksum -notmatch "^[0-9a-f]{64}$") {
        throw "runtime manifest contains an invalid checksum for $platform"
    }

    $cacheRoot = if (
        -not [string]::IsNullOrWhiteSpace($env:REVYL_PLUGIN_CACHE_DIR)
    ) {
        $env:REVYL_PLUGIN_CACHE_DIR
    }
    else {
        Join-Path `
            -Path ([Environment]::GetFolderPath("LocalApplicationData")) `
            -ChildPath "Revyl\cursor-plugin"
    }
    $runtimeDirectory = Join-Path `
        -Path $cacheRoot `
        -ChildPath "$runtimeVersion\$platform"
    $runtimeBinary = Join-Path -Path $runtimeDirectory -ChildPath "revyl.exe"

    if (Test-RuntimeChecksum -Path $runtimeBinary -ExpectedChecksum $expectedChecksum) {
        exit (Invoke-RevylRuntime -BinaryPath $runtimeBinary -Arguments $RevylArguments)
    }

    New-Item -ItemType Directory -Path $runtimeDirectory -Force | Out-Null
    $temporaryPath = Join-Path `
        -Path $runtimeDirectory `
        -ChildPath ".revyl.download.$PID"
    $downloadUri = [uri] "$releaseBaseUrl/$asset"
    Invoke-RuntimeDownload -Uri $downloadUri -Destination $temporaryPath
    if (-not (Test-RuntimeChecksum -Path $temporaryPath -ExpectedChecksum $expectedChecksum)) {
        throw "checksum verification failed for $asset"
    }

    Install-RuntimeAtomically -Source $temporaryPath -Destination $runtimeBinary
    $temporaryPath = $null
    if (-not (Test-RuntimeChecksum -Path $runtimeBinary -ExpectedChecksum $expectedChecksum)) {
        throw "cached runtime verification failed after installation"
    }

    exit (Invoke-RevylRuntime -BinaryPath $runtimeBinary -Arguments $RevylArguments)
}
catch {
    Write-BootstrapError -Message $_.Exception.Message
    exit 1
}
finally {
    if ($null -ne $temporaryPath) {
        Remove-Item -LiteralPath $temporaryPath -Force -ErrorAction SilentlyContinue
    }
}
