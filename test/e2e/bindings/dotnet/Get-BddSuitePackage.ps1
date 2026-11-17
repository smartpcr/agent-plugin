<#
.SYNOPSIS
    Downloads the bdd-suite C# runtime package into this binding's local feed.

.DESCRIPTION
    Smartpcr.BddSuite is attached to each GitHub release of
    smartpcr/bdd-code-extension, but is not on nuget.org. The binding restores
    it from ./feed (RestoreAdditionalProjectSources in Directory.Build.props),
    which this script fills. Run it once before the first build, and again after
    changing the version.
#>
[CmdletBinding()]
param(
    [string] $Version = '0.8.1'
)

$ErrorActionPreference = 'Stop'
$feed = Join-Path $PSScriptRoot 'feed'
New-Item -ItemType Directory -Force -Path $feed | Out-Null

$name = "Smartpcr.BddSuite.$Version.nupkg"
$target = Join-Path $feed $name
if (Test-Path $target) {
    Write-Host "already present: $target"
    return
}

$uri = "https://github.com/smartpcr/bdd-code-extension/releases/download/v$Version/$name"
Write-Host "downloading $uri"
Invoke-WebRequest -Uri $uri -OutFile $target
Write-Host "wrote $target"
