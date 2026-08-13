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

# Resolve-InstalledRuntime returns the first installed CLI byte-identical to the pin.
function Resolve-InstalledRuntime {
    param(
        [Parameter(Mandatory = $true)]
        [string] $ExpectedChecksum
    )

    $candidates = [System.Collections.Generic.List[string]]::new()
    Get-Command `
        -Name "revyl" `
        -CommandType Application `
        -ErrorAction SilentlyContinue |
        ForEach-Object { $candidates.Add($_.Source) }
    if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
        $candidates.Add((Join-Path $env:USERPROFILE ".revyl\bin\revyl.exe"))
    }
    if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $candidates.Add((Join-Path $env:LOCALAPPDATA "Revyl\bin\revyl.exe"))
    }

    foreach ($candidate in $candidates) {
        if (Test-RuntimeChecksum -Path $candidate -ExpectedChecksum $ExpectedChecksum) {
            return (Get-Item -LiteralPath $candidate).FullName
        }
    }
    return $null
}

# Invoke-RuntimeDownload retries one bounded HTTPS artifact with backoff.
function Invoke-RuntimeDownload {
    param(
        [Parameter(Mandatory = $true)]
        [uri] $Uri,
        [Parameter(Mandatory = $true)]
        [string] $Destination
    )

    $delaySeconds = 1
    for ($attempt = 1; $attempt -le $script:DownloadAttempts; $attempt++) {
        try {
            Invoke-WebRequest `
                -Uri $Uri `
                -OutFile $Destination `
                -UseBasicParsing `
                -UserAgent "revyl-cursor-plugin" `
                -TimeoutSec 180 | Out-Null
            break
        }
        catch {
            Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
            if ($attempt -eq $script:DownloadAttempts) {
                throw (
                    "could not download $Uri after $($script:DownloadAttempts) attempts; " +
                    "install the Revyl CLI or set REVYL_BINARY to an executable Revyl CLI path"
                )
            }
            [Console]::Error.WriteLine(
                "Revyl plugin runtime: download attempt $attempt of " +
                "$($script:DownloadAttempts) failed; retrying in ${delaySeconds}s"
            )
            Start-Sleep -Seconds $delaySeconds
            $delaySeconds = $delaySeconds * 2
        }
    }

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

$script:DownloadAttempts = 3

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

    # An already-installed CLI is byte-identical to the pinned asset when its digest
    # matches, so adopting it is equivalent to the download it replaces.
    $installedBinary = Resolve-InstalledRuntime -ExpectedChecksum $expectedChecksum
    if ($null -ne $installedBinary) {
        try {
            New-Item -ItemType Directory -Path $runtimeDirectory -Force | Out-Null
            $temporaryPath = Join-Path `
                -Path $runtimeDirectory `
                -ChildPath ".revyl.adopt.$PID"
            Copy-Item `
                -LiteralPath $installedBinary `
                -Destination $temporaryPath `
                -Force
            if (Test-RuntimeChecksum -Path $temporaryPath -ExpectedChecksum $expectedChecksum) {
                Install-RuntimeAtomically -Source $temporaryPath -Destination $runtimeBinary
                $temporaryPath = $null
                if (Test-RuntimeChecksum -Path $runtimeBinary -ExpectedChecksum $expectedChecksum) {
                    exit (Invoke-RevylRuntime -BinaryPath $runtimeBinary -Arguments $RevylArguments)
                }
            }
        }
        catch {
            # Adoption is best effort; the verified installed binary still runs below.
        }
        if ($null -ne $temporaryPath) {
            Remove-Item -LiteralPath $temporaryPath -Force -ErrorAction SilentlyContinue
            $temporaryPath = $null
        }
        [Console]::Error.WriteLine(
            "Revyl plugin runtime: could not populate the plugin cache; " +
            "running verified $installedBinary"
        )
        exit (Invoke-RevylRuntime -BinaryPath $installedBinary -Arguments $RevylArguments)
    }

    # Callers on a short time budget opt out of the download so they fail fast
    # instead of blocking on the network for a runtime a later run will cache.
    if ($env:REVYL_RUNTIME_NO_DOWNLOAD -eq "1") {
        throw "the pinned Revyl runtime is not cached yet and this invocation may not download it"
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
