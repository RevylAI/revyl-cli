[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $RevylArguments
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$env:REVYL_PLUGIN_HOST = "codex"
& (Join-Path $PSScriptRoot "launch-runtime.ps1") @RevylArguments
exit $LASTEXITCODE
