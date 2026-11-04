[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $RevylArguments
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$env:REVYL_PLUGIN_HOST = "codex"
$codexMarkers = "$env:CODEX_THREAD_ID$env:CODEX_SHELL$env:CODEX_CI"
if ([string]::IsNullOrEmpty($codexMarkers) -and -not [string]::IsNullOrEmpty($env:CLAUDECODE)) {
    $env:REVYL_PLUGIN_HOST = "claude"
}
& (Join-Path $PSScriptRoot "launch-runtime.ps1") @RevylArguments
exit $LASTEXITCODE
