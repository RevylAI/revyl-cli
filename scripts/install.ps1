<#
.SYNOPSIS
Installs the Revyl CLI for the current Windows user.

.PARAMETER Version
Release tag to install; defaults to REVYL_VERSION or the latest release.

.PARAMETER InstallDir
Destination directory; defaults to REVYL_INSTALL_DIR or ~/.revyl/bin.

.PARAMETER NoModifyPath
Skips the user PATH update; REVYL_NO_MODIFY_PATH=1 has the same effect.

.PARAMETER DryRun
Prints the install plan without network, filesystem, or PATH changes.
#>
[CmdletBinding()]
param(
    [string] $Version = $env:REVYL_VERSION,
    [string] $InstallDir = $env:REVYL_INSTALL_DIR,
    [switch] $NoModifyPath,
    [switch] $DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = (
    [Net.ServicePointManager]::SecurityProtocol -bor
    [Net.SecurityProtocolType]::Tls12
)

$script:RevylRepository = "RevylAI/revyl-cli"
$script:RevylBinaryName = "revyl.exe"

<#
.SYNOPSIS
Maps a Windows processor architecture to a Revyl release architecture.
.PARAMETER NativeArchitecture
Native architecture reported by a compatibility layer, when present.
.PARAMETER ProcessArchitecture
Architecture reported for the current process.
.OUTPUTS
System.String. The release architecture.
.NOTES
Throws when the architecture is unsupported or unavailable.
#>
function Get-RevylArchitecture {
    [CmdletBinding()]
    param(
        [AllowEmptyString()]
        [string] $NativeArchitecture = $env:PROCESSOR_ARCHITEW6432,
        [AllowEmptyString()]
        [string] $ProcessArchitecture = $env:PROCESSOR_ARCHITECTURE
    )

    $detectedArchitecture = if (
        -not [string]::IsNullOrWhiteSpace($NativeArchitecture)
    ) {
        $NativeArchitecture
    }
    else {
        $ProcessArchitecture
    }

    if ([string]::IsNullOrWhiteSpace($detectedArchitecture)) {
        throw "Could not detect the Windows architecture. Revyl supports amd64 and arm64."
    }

    switch ($detectedArchitecture.ToUpperInvariant()) {
        { $_ -in @("AMD64", "X64", "X86_64") } { return "amd64" }
        { $_ -in @("ARM64", "AARCH64") } { return "arm64" }
        default {
            throw "Unsupported Windows architecture: '$detectedArchitecture'. Revyl supports amd64 and arm64."
        }
    }
}

<#
.SYNOPSIS
Resolves and validates the release tag to install.
.PARAMETER ConfiguredVersion
Explicit release tag, or an empty value to resolve the latest release.
.PARAMETER LatestReleaseFetcher
Fetcher used to resolve the latest release tag.
.OUTPUTS
System.String. The validated release tag.
.NOTES
Throws when resolution fails or the tag is unsafe.
#>
function Resolve-RevylVersion {
    [CmdletBinding()]
    param(
        [AllowEmptyString()]
        [string] $ConfiguredVersion,
        [scriptblock] $LatestReleaseFetcher = {
            $response = Invoke-RestMethod `
                -Uri "https://api.github.com/repos/$script:RevylRepository/releases/latest" `
                -Method Get `
                -UserAgent "revyl-installer" `
                -TimeoutSec 30

            if ($null -eq $response.tag_name) {
                throw "The latest release response did not contain a tag."
            }

            return [string] $response.tag_name
        }
    )

    if ([string]::IsNullOrWhiteSpace($ConfiguredVersion)) {
        try {
            $resolvedVersion = [string] (& $LatestReleaseFetcher)
        }
        catch {
            throw "Could not determine the latest Revyl version. Set REVYL_VERSION to a release tag and retry."
        }
    }
    else {
        $resolvedVersion = $ConfiguredVersion.Trim()
    }

    if ($resolvedVersion -notmatch "^v?[0-9][0-9A-Za-z._-]*$") {
        throw "Invalid Revyl version '$resolvedVersion'. Use a release tag such as v0.1.13."
    }

    return $resolvedVersion
}

<#
.SYNOPSIS
Resolves the configured install directory to an absolute path.
.PARAMETER ConfiguredDirectory
Configured install directory, or an empty value for the default.
.PARAMETER HomeDirectory
Current user's home directory.
.OUTPUTS
System.String. The absolute install directory.
.NOTES
Throws when no user home is available or the path is invalid.
#>
function Resolve-RevylInstallDirectory {
    [CmdletBinding()]
    param(
        [AllowEmptyString()]
        [string] $ConfiguredDirectory,
        [AllowEmptyString()]
        [string] $HomeDirectory = [Environment]::GetFolderPath("UserProfile")
    )

    if ([string]::IsNullOrWhiteSpace($ConfiguredDirectory)) {
        if ([string]::IsNullOrWhiteSpace($HomeDirectory)) {
            throw "Could not determine the current user's home directory. Set REVYL_INSTALL_DIR and retry."
        }

        $candidateDirectory = Join-Path -Path $HomeDirectory -ChildPath ".revyl\bin"
    }
    else {
        $candidateDirectory = [Environment]::ExpandEnvironmentVariables(
            $ConfiguredDirectory.Trim()
        )

        if ($candidateDirectory -eq "~") {
            $candidateDirectory = $HomeDirectory
        }
        elseif (
            $candidateDirectory.StartsWith("~\") -or
            $candidateDirectory.StartsWith("~/")
        ) {
            $candidateDirectory = Join-Path `
                -Path $HomeDirectory `
                -ChildPath $candidateDirectory.Substring(2)
        }
    }

    if ($candidateDirectory -match "[;`r`n]") {
        throw "REVYL_INSTALL_DIR cannot contain semicolons or line breaks."
    }

    if ($candidateDirectory.IndexOfAny([IO.Path]::GetInvalidPathChars()) -ge 0) {
        throw "REVYL_INSTALL_DIR contains invalid path characters."
    }

    return [IO.Path]::GetFullPath($candidateDirectory)
}

<#
.SYNOPSIS
Creates the immutable release download plan.
.PARAMETER Version
Validated release tag.
.PARAMETER InstallDirectory
Absolute destination directory.
.PARAMETER Architecture
Revyl release architecture.
.OUTPUTS
System.Management.Automation.PSCustomObject. The install plan.
#>
function New-RevylInstallPlan {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $Version,
        [Parameter(Mandatory)]
        [string] $InstallDirectory,
        [Parameter(Mandatory)]
        [ValidateSet("amd64", "arm64")]
        [string] $Architecture
    )

    $assetName = "revyl-windows-$Architecture.exe"
    $releaseBaseUrl = "https://github.com/$script:RevylRepository/releases/download/$Version"

    return [pscustomobject] @{
        Version          = $Version
        Architecture     = $Architecture
        AssetName        = $assetName
        DownloadUrl      = "$releaseBaseUrl/$assetName"
        ChecksumsUrl     = "$releaseBaseUrl/checksums.txt"
        InstallDirectory = $InstallDirectory
        DestinationPath  = Join-Path -Path $InstallDirectory -ChildPath $script:RevylBinaryName
    }
}

<#
.SYNOPSIS
Downloads one release artifact with a bounded request.
.PARAMETER Uri
Public release artifact URI.
.PARAMETER Destination
Local destination path.
.OUTPUTS
None.
.NOTES
Throws when the request fails or produces an empty file.
#>
function Invoke-RevylDownload {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [uri] $Uri,
        [Parameter(Mandatory)]
        [string] $Destination
    )

    Invoke-WebRequest `
        -Uri $Uri `
        -OutFile $Destination `
        -UseBasicParsing `
        -UserAgent "revyl-installer" `
        -TimeoutSec 120 | Out-Null

    if (-not (Test-Path -LiteralPath $Destination -PathType Leaf)) {
        throw "The download did not create the expected file."
    }

    if ((Get-Item -LiteralPath $Destination).Length -eq 0) {
        throw "The downloaded file was empty."
    }
}

<#
.SYNOPSIS
Reads the exact SHA256 entry for a release asset.
.PARAMETER ChecksumsPath
Path to checksums.txt.
.PARAMETER AssetName
Exact release asset filename.
.OUTPUTS
System.String. The lowercase SHA256 digest.
.NOTES
Throws when the entry is missing, malformed, or duplicated.
#>
function Get-RevylExpectedChecksum {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $ChecksumsPath,
        [Parameter(Mandatory)]
        [string] $AssetName
    )

    $matchingHashes = @()

    foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
        $match = [regex]::Match(
            $line,
            "^\s*([0-9A-Fa-f]{64})\s+\*?(.+?)\s*$"
        )

        if (
            $match.Success -and
            [string]::Equals(
                $match.Groups[2].Value,
                $AssetName,
                [StringComparison]::Ordinal
            )
        ) {
            $matchingHashes += $match.Groups[1].Value.ToLowerInvariant()
        }
    }

    if ($matchingHashes.Count -eq 0) {
        throw "checksums.txt does not contain an entry for '$AssetName'."
    }

    if ($matchingHashes.Count -ne 1) {
        throw "checksums.txt contains multiple entries for '$AssetName'."
    }

    return $matchingHashes[0]
}

<#
.SYNOPSIS
Verifies a downloaded asset against checksums.txt.
.PARAMETER BinaryPath
Downloaded release asset path.
.PARAMETER ChecksumsPath
Downloaded checksums.txt path.
.PARAMETER AssetName
Exact release asset filename.
.OUTPUTS
None.
.NOTES
Throws when the expected entry is unavailable or the hash differs.
#>
function Confirm-RevylChecksum {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $BinaryPath,
        [Parameter(Mandatory)]
        [string] $ChecksumsPath,
        [Parameter(Mandatory)]
        [string] $AssetName
    )

    $expectedHash = Get-RevylExpectedChecksum `
        -ChecksumsPath $ChecksumsPath `
        -AssetName $AssetName
    $actualHash = (Get-FileHash -LiteralPath $BinaryPath -Algorithm SHA256).Hash.ToLowerInvariant()

    if (
        -not [string]::Equals(
            $actualHash,
            $expectedHash,
            [StringComparison]::Ordinal
        )
    ) {
        throw "Checksum mismatch for '$AssetName'. Installation stopped."
    }
}

<#
.SYNOPSIS
Atomically replaces the installed Revyl executable.
.PARAMETER SourcePath
Verified downloaded executable.
.PARAMETER InstallDirectory
Destination directory.
.PARAMETER BinaryName
Installed executable filename.
.OUTPUTS
System.String. The installed executable path.
.NOTES
Throws when staging or atomic replacement fails.
#>
function Install-RevylBinaryAtomically {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $SourcePath,
        [Parameter(Mandatory)]
        [string] $InstallDirectory,
        [string] $BinaryName = $script:RevylBinaryName
    )

    New-Item -ItemType Directory -Path $InstallDirectory -Force | Out-Null

    $destinationPath = Join-Path -Path $InstallDirectory -ChildPath $BinaryName
    $stagedPath = Join-Path `
        -Path $InstallDirectory `
        -ChildPath ".$BinaryName.$([guid]::NewGuid().ToString('N')).tmp"

    if (
        (Test-Path -LiteralPath $destinationPath) -and
        -not (Test-Path -LiteralPath $destinationPath -PathType Leaf)
    ) {
        throw "The install destination exists but is not a file: '$destinationPath'."
    }

    try {
        Copy-Item -LiteralPath $SourcePath -Destination $stagedPath

        if (Test-Path -LiteralPath $destinationPath -PathType Leaf) {
            # File.Replace rejects a null/empty backup path; mirror launch-revyl.ps1.
            $backupPath = "$destinationPath.backup.$PID"
            try {
                [IO.File]::Replace($stagedPath, $destinationPath, $backupPath, $true)
            }
            finally {
                Remove-Item -LiteralPath $backupPath -Force -ErrorAction SilentlyContinue
            }
        }
        else {
            [IO.File]::Move($stagedPath, $destinationPath)
        }
    }
    finally {
        Remove-Item -LiteralPath $stagedPath -Force -ErrorAction SilentlyContinue
    }

    return $destinationPath
}

<#
.SYNOPSIS
Calculates an idempotent current-user PATH update.
.PARAMETER InstallDirectory
Absolute Revyl install directory.
.PARAMETER CurrentUserPath
Existing current-user PATH value.
.PARAMETER NoModifyPath
Prevents creation of a modified PATH value.
.OUTPUTS
System.Management.Automation.PSCustomObject. The PATH update decision.
#>
function Get-RevylUserPathUpdate {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $InstallDirectory,
        [AllowNull()]
        [AllowEmptyString()]
        [string] $CurrentUserPath,
        [switch] $NoModifyPath
    )

    $trimCharacters = [char[]] @("\", "/")
    $targetPath = [IO.Path]::GetFullPath(
        [Environment]::ExpandEnvironmentVariables($InstallDirectory)
    ).TrimEnd($trimCharacters)

    foreach ($entry in @($CurrentUserPath -split ";")) {
        if ([string]::IsNullOrWhiteSpace($entry)) {
            continue
        }

        $expandedEntry = [Environment]::ExpandEnvironmentVariables(
            $entry.Trim().Trim('"')
        )

        try {
            $comparableEntry = [IO.Path]::GetFullPath($expandedEntry).TrimEnd($trimCharacters)
        }
        catch {
            $comparableEntry = $expandedEntry.TrimEnd($trimCharacters)
        }

        if (
            [string]::Equals(
                $comparableEntry,
                $targetPath,
                [StringComparison]::OrdinalIgnoreCase
            )
        ) {
            return [pscustomobject] @{
                AlreadyPresent = $true
                ShouldModify   = $false
                UpdatedPath    = $CurrentUserPath
                Reason         = "already_present"
            }
        }
    }

    if ($NoModifyPath) {
        return [pscustomobject] @{
            AlreadyPresent = $false
            ShouldModify   = $false
            UpdatedPath    = $CurrentUserPath
            Reason         = "modification_disabled"
        }
    }

    $updatedPath = if ([string]::IsNullOrWhiteSpace($CurrentUserPath)) {
        $InstallDirectory
    }
    elseif ($CurrentUserPath.EndsWith(";")) {
        "$CurrentUserPath$InstallDirectory"
    }
    else {
        "$CurrentUserPath;$InstallDirectory"
    }

    return [pscustomobject] @{
        AlreadyPresent = $false
        ShouldModify   = $true
        UpdatedPath    = $updatedPath
        Reason         = "added"
    }
}

<#
.SYNOPSIS
Applies the idempotent current-user PATH update.
.PARAMETER InstallDirectory
Absolute Revyl install directory.
.PARAMETER NoModifyPath
Prevents current-user PATH modification.
.PARAMETER ReadUserPath
Reader for the current-user PATH value.
.PARAMETER WriteUserPath
Writer for the current-user PATH value.
.OUTPUTS
System.Management.Automation.PSCustomObject. The applied PATH decision.
.NOTES
Throws when the current-user environment cannot be read or written.
#>
function Set-RevylUserPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string] $InstallDirectory,
        [switch] $NoModifyPath,
        [scriptblock] $ReadUserPath = {
            [Environment]::GetEnvironmentVariable("Path", "User")
        },
        [scriptblock] $WriteUserPath = {
            param([string] $Value)
            [Environment]::SetEnvironmentVariable("Path", $Value, "User")
        }
    )

    $currentUserPath = [string] (& $ReadUserPath)
    $update = Get-RevylUserPathUpdate `
        -InstallDirectory $InstallDirectory `
        -CurrentUserPath $currentUserPath `
        -NoModifyPath:$NoModifyPath

    if ($update.ShouldModify) {
        & $WriteUserPath $update.UpdatedPath | Out-Null
    }

    return $update
}

<#
.SYNOPSIS
Runs the checksum-verified Revyl installation workflow.
.PARAMETER RequestedVersion
Configured version or an empty value for latest.
.PARAMETER RequestedInstallDirectory
Configured install directory or an empty value for the default.
.PARAMETER SkipPathModification
Prevents current-user PATH modification.
.PARAMETER Preview
Returns the install plan without side effects.
.PARAMETER DownloadFile
Artifact downloader seam used by isolated tests.
.PARAMETER UpdateUserPath
PATH updater seam used by isolated tests.
.OUTPUTS
System.Management.Automation.PSCustomObject. The completed or previewed install plan.
.NOTES
Throws on any resolution, download, verification, install, or PATH failure.
#>
function Invoke-RevylInstaller {
    [CmdletBinding()]
    param(
        [AllowEmptyString()]
        [string] $RequestedVersion,
        [AllowEmptyString()]
        [string] $RequestedInstallDirectory,
        [switch] $SkipPathModification,
        [switch] $Preview,
        [scriptblock] $DownloadFile = {
            param([uri] $Uri, [string] $Destination)
            Invoke-RevylDownload -Uri $Uri -Destination $Destination
        },
        [scriptblock] $UpdateUserPath = {
            param([string] $TargetDirectory, [bool] $SkipModification)
            Set-RevylUserPath `
                -InstallDirectory $TargetDirectory `
                -NoModifyPath:$SkipModification
        }
    )

    Write-Host ""
    Write-Host "  Revyl CLI Installer" -ForegroundColor Cyan
    Write-Host "  -------------------" -ForegroundColor DarkCyan

    $architecture = Get-RevylArchitecture
    $installDirectory = Resolve-RevylInstallDirectory `
        -ConfiguredDirectory $RequestedInstallDirectory

    if ($Preview) {
        $previewVersion = if ([string]::IsNullOrWhiteSpace($RequestedVersion)) {
            "latest"
        }
        else {
            Resolve-RevylVersion -ConfiguredVersion $RequestedVersion
        }
        $previewPlan = New-RevylInstallPlan `
            -Version $previewVersion `
            -InstallDirectory $installDirectory `
            -Architecture $architecture

        Write-Host "  Dry run; no changes will be made."
        Write-Host "  Platform: windows/$architecture"
        Write-Host "  Version:  $previewVersion"
        Write-Host "  Asset:    $($previewPlan.AssetName)"
        Write-Host "  Install:  $installDirectory"
        if ($SkipPathModification) {
            Write-Host "  PATH:     unchanged"
        }
        else {
            Write-Host "  PATH:     add install directory if missing"
        }
        Write-Host ""

        return $previewPlan
    }

    if ([string]::IsNullOrWhiteSpace($RequestedVersion)) {
        Write-Host "  Resolving latest version..."
    }

    $resolvedVersion = Resolve-RevylVersion -ConfiguredVersion $RequestedVersion
    $plan = New-RevylInstallPlan `
        -Version $resolvedVersion `
        -InstallDirectory $installDirectory `
        -Architecture $architecture

    Write-Host "  Platform: windows/$architecture"
    Write-Host "  Version:  $resolvedVersion"
    Write-Host "  Downloading $($plan.AssetName)..."

    $temporaryDirectory = Join-Path `
        -Path ([IO.Path]::GetTempPath()) `
        -ChildPath "revyl-install-$([guid]::NewGuid().ToString('N'))"
    $temporaryBinary = Join-Path -Path $temporaryDirectory -ChildPath $plan.AssetName
    $temporaryChecksums = Join-Path -Path $temporaryDirectory -ChildPath "checksums.txt"

    try {
        New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null

        try {
            & $DownloadFile ([uri] $plan.DownloadUrl) $temporaryBinary | Out-Null
        }
        catch {
            throw "Failed to download '$($plan.AssetName)' for version '$resolvedVersion'."
        }

        try {
            & $DownloadFile ([uri] $plan.ChecksumsUrl) $temporaryChecksums | Out-Null
        }
        catch {
            throw "Could not download checksums.txt for version '$resolvedVersion'. Verification is required."
        }

        Confirm-RevylChecksum `
            -BinaryPath $temporaryBinary `
            -ChecksumsPath $temporaryChecksums `
            -AssetName $plan.AssetName
        Write-Host "  Checksum verified." -ForegroundColor Green

        $installedPath = Install-RevylBinaryAtomically `
            -SourcePath $temporaryBinary `
            -InstallDirectory $plan.InstallDirectory
        Write-Host "  Installed to $installedPath" -ForegroundColor Green

        $pathUpdate = & $UpdateUserPath `
            $plan.InstallDirectory `
            ([bool] $SkipPathModification)

        if ($pathUpdate.Reason -eq "added") {
            Write-Host "  Added the install directory to your user PATH." -ForegroundColor Green
        }
        elseif ($pathUpdate.Reason -eq "modification_disabled") {
            Write-Host "  PATH unchanged. Add '$($plan.InstallDirectory)' to your user PATH."
        }

        Write-Host ""
        Write-Host "  Revyl CLI $resolvedVersion installed successfully!" -ForegroundColor Green
        Write-Host "  Open a new terminal, then run: revyl --help"
        Write-Host ""

        return $plan
    }
    finally {
        Remove-Item `
            -LiteralPath $temporaryDirectory `
            -Recurse `
            -Force `
            -ErrorAction SilentlyContinue
    }
}

if ($env:REVYL_INSTALLER_NO_RUN -ne "1") {
    if ($env:REVYL_NO_MODIFY_PATH -eq "1") {
        $NoModifyPath = $true
    }

    try {
        $null = Invoke-RevylInstaller `
            -RequestedVersion $Version `
            -RequestedInstallDirectory $InstallDir `
            -SkipPathModification:$NoModifyPath `
            -Preview:$DryRun
    }
    catch {
        Write-Host ""
        Write-Host "  ERROR: $($_.Exception.Message)" -ForegroundColor Red
        Write-Host "  No unverified Revyl binary was installed." -ForegroundColor Red
        Write-Host ""
        exit 1
    }
}
