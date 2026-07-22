<#
.SYNOPSIS
Reports Revyl plugin runtime readiness through Cursor's hook protocol.
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

$eventName = Read-HookEventName
$supportedEvent = $eventName -in @("beforeShellExecution", "sessionStart")
$message = if ($supportedEvent -and -not (Test-PluginRuntimeReady)) {
    $script:RuntimeUnavailableMessage
}
else {
    ""
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
