# The Windows native lane of harness-gate (M1-G-GATE-20; README.md, section Lanes).
#
#   pwsh -File tools/harness-gate/lanes/win.ps1
#
# It selects the go floor with GOTOOLCHAIN (the host default may be a later Go),
# checks the lane and stops at the first preflight check not met, naming it, before
# any gate runs. It then runs the gate with --evidence and leaves the record in
# docs/stories/harness-DSH-M1/evidence/runs/win-<sha7>-<UTC yyyymmddThhmmZ>.txt.
# Exit status: the gate's (0 pass, 1 a gate failed, 2 setup); 3 when a preflight
# check fails.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Fail([string] $Message) {
    [Console]::Error.WriteLine("win lane: preflight FAILED: $Message")
    exit 3
}

$root = (& git -C $PSScriptRoot rev-parse --show-toplevel 2>$null)
if ($LASTEXITCODE -ne 0 -or -not $root) { Fail "not inside a git checkout: $PSScriptRoot" }
$root = [IO.Path]::GetFullPath($root.Trim())
Set-Location -LiteralPath $root

# 1. The checkout is on a local NTFS or ReFS (Dev Drive) volume.
$drive = [IO.DriveInfo]::new([IO.Path]::GetPathRoot($root))
if ($drive.DriveType -ne [IO.DriveType]::Fixed -or $drive.DriveFormat -notin @('NTFS', 'ReFS')) {
    Fail "checkout on $($drive.DriveFormat) ($($drive.DriveType) drive $($drive.Name)); use a local NTFS or ReFS volume"
}

# 2. Not elevated: an Administrator token can bypass the permission tests.
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail 'running elevated (Administrator); use a non-elevated shell'
}

# 3. go is the floor of src/harness/go.mod, selected with GOTOOLCHAIN (Go fetches it
#    when the default go on PATH is another release).
$floorLine = Select-String -LiteralPath (Join-Path $root 'src/harness/go.mod') -Pattern '^go\s+(\S+)' | Select-Object -First 1
if (-not $floorLine) { Fail 'no go line in src/harness/go.mod' }
$floor = $floorLine.Matches[0].Groups[1].Value
$env:GOTOOLCHAIN = "go$floor"   # go1.27.4 at the time of writing (go-floor-1-27-4)
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail 'go is not on PATH' }
$version = (& go version 2>&1 | Select-Object -Last 1)
if ($LASTEXITCODE -ne 0) { Fail "go version with GOTOOLCHAIN=go${floor}: $version" }
$got = ("$version" -split '\s+')[2]
if ($got -ne "go$floor") { Fail "go version is $got, not the floor go$floor (src/harness/go.mod)" }

# 4. go env CC resolves to a C compiler (cgo, and so -race; MSYS2 UCRT64 gcc).
$env:CGO_ENABLED = '1'
$cc = "$(& go env CC)".Trim()
Remove-Item Env:CGO_ENABLED
$ccName = ($cc -split '\s+')[0]
if (-not $ccName) { Fail 'go env CC is empty; put MSYS2 UCRT64 gcc on PATH' }
if (-not (Get-Command $ccName -ErrorAction SilentlyContinue)) { Fail "go env CC ($ccName) does not resolve; put MSYS2 UCRT64 gcc on PATH" }

# 5. The tree is clean, with core.autocrlf false or unset.
$autocrlf = "$(& git config --get core.autocrlf)".Trim()
if ($autocrlf -notin @('', 'false')) { Fail "core.autocrlf is $autocrlf; set it to false (.gitattributes forces eol=lf)" }
$dirty = (& git -c core.autocrlf=false status --porcelain --untracked-files=all) -join "`n"
if ($LASTEXITCODE -ne 0) { Fail 'git status failed' }
if ($dirty) { Fail "the tree is not clean:`n$dirty" }

# (TMPDIR on ext4 is a WSL-lane check only.)

# 6. pwsh resolves on PATH to an executable (architecture.md "Shells"): the pwsh
#    executor rows run on this lane and fail, never skip, without it.
$pwsh = Get-Command pwsh -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $pwsh) { Fail 'pwsh is not on PATH; install PowerShell 7 (winget install Microsoft.PowerShell)' }
if (-not [IO.Path]::IsPathRooted($pwsh.Source)) { Fail "pwsh resolves to $($pwsh.Source), not an absolute path" }
$pwshVersion = "$(& $pwsh.Source -NoLogo -NoProfile -NonInteractive -Command '$PSVersionTable.PSVersion.ToString()' 2>&1)".Trim()
if ($LASTEXITCODE -ne 0 -or -not $pwshVersion) { Fail "pwsh at $($pwsh.Source) does not run: $pwshVersion" }

$sha = "$(& git rev-parse HEAD)".Trim()
$stamp = [DateTime]::UtcNow.ToString("yyyyMMdd'T'HHmm'Z'")
$runs = Join-Path $root 'docs/stories/harness-DSH-M1/evidence/runs'
$record = Join-Path $runs "win-$($sha.Substring(0, 7))-$stamp.txt"
Write-Output "win lane: preflight passed: $($drive.DriveFormat) checkout, not elevated, $got, CC=$ccName, clean tree, pwsh $pwshVersion ($($pwsh.Source))"

New-Item -ItemType Directory -Force -Path $runs | Out-Null
$env:GOWORK = 'off'
& go -C tools/harness-gate run . --root ../.. --evidence $record
$gate = $LASTEXITCODE
Write-Output "win lane: gate exit $gate, record $record"
exit $gate
