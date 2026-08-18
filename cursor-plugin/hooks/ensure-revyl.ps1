<#
.SYNOPSIS
Reports Revyl plugin runtime readiness, installs the pinned CLI when allowed,
and bridges an injected API key or tells the agent how to sign in.
#>

[CmdletBinding()]
param(
    [AllowEmptyString()]
    [string] $InstallRoot = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$script:RuntimeUnavailableMessage = "The Revyl plugin runtime is not ready. Update or reinstall the plugin, or set REVYL_BINARY to an executable Revyl CLI path."
$script:LoginGuidance = "Run revyl auth login and post the printed approval URL as a clickable markdown link. Wait for approval. REVYL_API_KEY is optional for unattended agents."
$script:InstallOnFirstCommand = "The Revyl CLI installs on the first revyl command."

# Resolve-PluginRoot returns the logical plugin install root for copied or linked hooks.
function Resolve-PluginRoot {
    foreach ($Candidate in @($env:CURSOR_PLUGIN_ROOT, $InstallRoot)) {
        if (
            [string]::IsNullOrWhiteSpace($Candidate) -or
            $Candidate -eq '${CURSOR_PLUGIN_ROOT}'
        ) {
            continue
        }
        if (Test-Path -LiteralPath $Candidate -PathType Container) {
            return [System.IO.Path]::GetFullPath($Candidate)
        }
    }
    return [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
}

$script:PluginRoot = Resolve-PluginRoot

# Clear-LiteralInterpolation unsets Cloud values stored as unexpanded text.
function Clear-LiteralInterpolation {
    if ($env:REVYL_API_KEY -eq '${env:REVYL_API_KEY}') {
        Remove-Item -Path Env:REVYL_API_KEY -ErrorAction SilentlyContinue
    }
    if ($env:REVYL_BINARY -eq '${env:REVYL_BINARY}') {
        Remove-Item -Path Env:REVYL_BINARY -ErrorAction SilentlyContinue
    }
}

# Test-UsableApiKey reports whether the environment holds a real credential.
function Test-UsableApiKey {
    -not [string]::IsNullOrWhiteSpace($env:REVYL_API_KEY)
}

# Test-ExecutableSelection verifies a command name or executable file.
function Test-ExecutableSelection {
    param(
        [AllowEmptyString()]
        [string] $Selection
    )

    if ([string]::IsNullOrWhiteSpace($Selection)) {
        return $false
    }
    if (Test-Path -LiteralPath $Selection -PathType Leaf) {
        return $true
    }
    return $null -ne (
        Get-Command `
            -Name $Selection `
            -CommandType Application `
            -ErrorAction SilentlyContinue |
            Select-Object -First 1
    )
}

# Test-PluginRuntimeReady verifies an override or prepared runtime manifest.
function Test-PluginRuntimeReady {
    if (Test-ExecutableSelection -Selection $env:REVYL_BINARY) {
        return $true
    }

    try {
        $manifestPath = Join-Path $script:PluginRoot "runtime-manifest.json"
        if (Test-Path -LiteralPath $manifestPath -PathType Leaf) {
            $manifest = Get-Content -LiteralPath $manifestPath -Raw |
                ConvertFrom-Json
            return [bool] $manifest.prepared
        }
    }
    catch {
        # A malformed manifest is reported through the generic readiness message.
    }
    return $false
}

# Read-HookEventName returns a supported Cursor hook event or an empty value.
function Read-HookEventName {
    try {
        $payloadText = [Console]::In.ReadToEnd()
        if ([string]::IsNullOrWhiteSpace($payloadText)) {
            return ""
        }
        $payload = $payloadText | ConvertFrom-Json
        return [string] $payload.hook_event_name
    }
    catch {
        return ""
    }
}

# Invoke-Launcher runs the pinned runtime with the given argv.
function Invoke-Launcher {
    param(
        [AllowEmptyString()]
        [string] $NoDownload,
        [Parameter(Mandatory = $true)]
        [string[]] $Arguments
    )

    $launcher = Join-Path $script:PluginRoot "hooks/launch-revyl.cmd"
    if (-not (Test-Path -LiteralPath $launcher -PathType Leaf)) {
        return $false
    }

    $previousNoDownload = $env:REVYL_RUNTIME_NO_DOWNLOAD
    $previousTelemetry = $env:REVYL_TELEMETRY_DISABLED
    $previousNotifier = $env:REVYL_NO_UPDATE_NOTIFIER
    try {
        $env:REVYL_RUNTIME_NO_DOWNLOAD = $NoDownload
        $env:REVYL_TELEMETRY_DISABLED = "1"
        $env:REVYL_NO_UPDATE_NOTIFIER = "1"
        & $launcher @Arguments *> $null
        return $LASTEXITCODE -eq 0
    }
    catch {
        return $false
    }
    finally {
        $env:REVYL_RUNTIME_NO_DOWNLOAD = $previousNoDownload
        $env:REVYL_TELEMETRY_DISABLED = $previousTelemetry
        $env:REVYL_NO_UPDATE_NOTIFIER = $previousNotifier
    }
}

# Invoke-EnsureRuntime downloads or resolves the pin via a cheap CLI command.
function Invoke-EnsureRuntime {
    param(
        [AllowEmptyString()]
        [string] $NoDownload
    )

    Invoke-Launcher -NoDownload $NoDownload -Arguments @("version")
}

# Invoke-PersistKey writes an injected REVYL_API_KEY into the CLI credential store.
function Invoke-PersistKey {
    Invoke-Launcher -NoDownload "" -Arguments @("auth", "persist-cloud-env")
}

# Write-HookResponse prints the Cursor hook payload for one event.
function Write-HookResponse {
    param(
        [AllowEmptyString()]
        [string] $EventName,
        [AllowEmptyString()]
        [string] $Message
    )

    $response = switch ($EventName) {
        "beforeShellExecution" {
            if ($Message) {
                [pscustomobject] @{
                    permission    = "allow"
                    agent_message = $Message
                }
            }
            else {
                [pscustomobject] @{ permission = "allow" }
            }
            break
        }
        "sessionStart" {
            if ($Message) {
                [pscustomobject] @{ additional_context = $Message }
            }
            else {
                [pscustomobject] @{}
            }
            break
        }
        default {
            [pscustomobject] @{}
        }
    }

    $response | ConvertTo-Json -Compress
}

Clear-LiteralInterpolation
$eventName = Read-HookEventName
$supportedEvent = $eventName -in @("beforeShellExecution", "sessionStart")
if (-not $supportedEvent) {
    Write-HookResponse -EventName $eventName -Message ""
    exit 0
}

if (-not (Test-PluginRuntimeReady)) {
    Write-HookResponse -EventName $eventName -Message $script:RuntimeUnavailableMessage
    exit 0
}

$ensureMode = "none"
$doPersist = $false
$failMessage = $script:LoginGuidance
$successMessage = $script:LoginGuidance
if ($eventName -eq "sessionStart") {
    if (Test-UsableApiKey) {
        $ensureMode = "nodownload"
        $doPersist = $true
        $failMessage = $script:InstallOnFirstCommand
        $successMessage = ""
    }
}
else {
    $ensureMode = "download"
    if (Test-UsableApiKey) {
        $doPersist = $true
        $failMessage = $script:RuntimeUnavailableMessage
        $successMessage = ""
    }
}

if ($ensureMode -eq "nodownload") {
    if (-not (Invoke-EnsureRuntime -NoDownload "1")) {
        Write-HookResponse -EventName $eventName -Message $failMessage
        exit 0
    }
}
elseif ($ensureMode -eq "download") {
    if (-not $doPersist) {
        $null = Invoke-EnsureRuntime -NoDownload ""
    }
    elseif (-not (Invoke-EnsureRuntime -NoDownload "")) {
        Write-HookResponse -EventName $eventName -Message $failMessage
        exit 0
    }
}

if ($doPersist) {
    if (-not (Invoke-PersistKey)) {
        Write-HookResponse -EventName $eventName -Message $failMessage
        exit 0
    }
}

Write-HookResponse -EventName $eventName -Message $successMessage
