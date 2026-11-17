<#
.SYNOPSIS
    Builds the C# and Go bindings and runs the behaviour suites with each.

.DESCRIPTION
    The C# binding (test/e2e/bindings/dotnet) references the .NET port in src/net;
    the Go binding (test/e2e/bindings/go) references the Go port in src/go. Both
    run the same suites in test/e2e/suites. Results go to artifacts/e2e as
    NUnit 3 XML. The exit code is non-zero when any binding has a failed case.

.EXAMPLE
    pwsh -File test/e2e/Invoke-E2E.ps1
    pwsh -File test/e2e/Invoke-E2E.ps1 -Binding go -Name 'a-policy*'
#>
[CmdletBinding()]
param(
    [ValidateSet('csharp', 'go', 'all')]
    [string] $Binding = 'all',

    # Passed to suiterun run --name (glob[,glob]).
    [string] $Name,

    [string] $Path = 'test/e2e/suites/*.yml'
)

$ErrorActionPreference = 'Stop'
$root = Resolve-Path (Join-Path $PSScriptRoot '../..')
$results = Join-Path $root 'artifacts/e2e'
New-Item -ItemType Directory -Force -Path $results | Out-Null
$exe = if ($IsWindows) { '.exe' } else { '' }

function Invoke-Binding([string] $label, [string] $suiterun) {
    $arguments = @('run', '--path', $Path, '--results', (Join-Path $results "$label.xml"))
    if ($Name) { $arguments += @('--name', $Name) }
    Write-Host "== ${label}: $suiterun $($arguments -join ' ')"
    Push-Location $root
    try {
        & $suiterun lint --path $Path | Out-Host
        if ($LASTEXITCODE -ne 0) { return $LASTEXITCODE }
        & $suiterun @arguments | Out-Host
        return $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
}

$failed = @()

if ($Binding -in 'csharp', 'all') {
    $dotnet = Join-Path $root 'test/e2e/bindings/dotnet'
    & pwsh -NoProfile -File (Join-Path $dotnet 'Get-BddSuitePackage.ps1') | Out-Host
    dotnet build (Join-Path $dotnet 'Suite.Cli') -nologo -v q | Out-Host
    if ($LASTEXITCODE -ne 0) { throw 'the C# binding did not build' }
    $suiterun = Join-Path $dotnet "artifacts/bin/Suite.Cli/Debug/net10.0/suiterun$exe"
    if ((Invoke-Binding 'csharp' $suiterun) -ne 0) { $failed += 'csharp' }
}

if ($Binding -in 'go', 'all') {
    $go = Join-Path $root 'test/e2e/bindings/go'
    $suiterun = Join-Path $results "go/suiterun$exe"
    Push-Location $go
    try {
        go build -o $suiterun ./cmd/suiterun
        if ($LASTEXITCODE -ne 0) { throw 'the Go binding did not build' }
    }
    finally {
        Pop-Location
    }
    if ((Invoke-Binding 'go' $suiterun) -ne 0) { $failed += 'go' }
}

if ($failed) {
    Write-Host "failed: $($failed -join ', ')"
    exit 1
}
Write-Host 'all bindings passed'
