<#
.SYNOPSIS
Reports Revyl plugin runtime readiness through Cursor's hook protocol and hands
any injected API key to the MCP server.
#>

[CmdletBinding()]
param(
    [AllowEmptyString()]
    [string] $InstallRoot = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$script:RuntimeUnavailableMessage = "The Revyl plugin runtime is not ready. Update or reinstall the plugin, or set REVYL_BINARY to an executable Revyl CLI path."

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

# Test-ExecutableSelection verifies a command name or executable file.
function Test-ExecutableSelection {
    param(
        [AllowEmptyString()]
        [string] $Selection
    )

    if (
        [string]::IsNullOrWhiteSpace($Selection) -or
        $Selection -eq '${env:REVYL_BINARY}'
    ) {
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
        $mcpPath = Join-Path $script:PluginRoot "mcp.json"
        if (Test-Path -LiteralPath $mcpPath -PathType Leaf) {
            $mcp = Get-Content -LiteralPath $mcpPath -Raw | ConvertFrom-Json
            $configuredBinary = [string] $mcp.mcpServers.revyl.env.REVYL_BINARY
            if (Test-ExecutableSelection -Selection $configuredBinary) {
                return $true
            }
        }
    }
    catch {
        # A malformed config is reported through the generic readiness message.
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

# Publish-EnvironmentApiKey hands a visible REVYL_API_KEY to the MCP server.
#
# The server runs as a separate process that does not reliably inherit secrets
# the host injected into the agent, so an agent can hold a valid key that its
# own Revyl tools cannot see. This hook does run in the environment that has the
# key, which makes it the one place able to pass it across. Doing so needs no
# network, and repeating it rewrites the same protected file from the same
# input, so every session can run it unconditionally.
#
# Failures are deliberately silent: a missing credential surfaces as an
# actionable authentication state on the first tool call, and a hook that
# reported it as well would only add noise to sessions that never touch Revyl.
function Publish-EnvironmentApiKey {
    param(
        [AllowEmptyString()]
        [string] $EventName
    )

    if (
        [string]::IsNullOrWhiteSpace($env:REVYL_API_KEY) -or
        $env:REVYL_API_KEY -eq '${env:REVYL_API_KEY}'
    ) {
        return
    }

    $launcher = Join-Path $script:PluginRoot "hooks/launch-revyl.cmd"
    if (-not (Test-Path -LiteralPath $launcher -PathType Leaf)) {
        return
    }

    # sessionStart has the shorter budget of the two events, so it never waits
    # for a runtime download. A first run that arrives before the runtime does is
    # bridged by beforeShellExecution, or by the command the failure names.
    $previousNoDownload = $env:REVYL_RUNTIME_NO_DOWNLOAD
    $previousTelemetry = $env:REVYL_TELEMETRY_DISABLED
    try {
        $env:REVYL_RUNTIME_NO_DOWNLOAD = if ($EventName -eq "sessionStart") { "1" } else { "" }
        $env:REVYL_TELEMETRY_DISABLED = "1"
        & $launcher auth persist-cloud-env *> $null
    }
    catch {
        # The next tool call reports the credential state with a runnable action.
    }
    finally {
        $env:REVYL_RUNTIME_NO_DOWNLOAD = $previousNoDownload
        $env:REVYL_TELEMETRY_DISABLED = $previousTelemetry
    }
}

$eventName = Read-HookEventName
$supportedEvent = $eventName -in @("beforeShellExecution", "sessionStart")
$runtimeReady = $supportedEvent -and (Test-PluginRuntimeReady)
$message = if ($supportedEvent -and -not $runtimeReady) {
    $script:RuntimeUnavailableMessage
}
else {
    ""
}

if ($runtimeReady) {
    Publish-EnvironmentApiKey -EventName $eventName
}

$response = switch ($eventName) {
    "beforeShellExecution" {
        if ($message) {
            [pscustomobject] @{
                permission    = "allow"
                agent_message = $message
            }
        }
        else {
            [pscustomobject] @{ permission = "allow" }
        }
        break
    }
    "sessionStart" {
        if ($message) {
            [pscustomobject] @{ additional_context = $message }
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
