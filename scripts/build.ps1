[CmdletBinding()]
param(
    [ValidateSet("Debug", "Release")]
    [string]$Configuration = "Debug",
    [string]$OutputPath
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

if (-not $OutputPath) {
    $suffix = if ($Configuration -eq "Debug") { "-debug" } else { "" }
    $OutputPath = "bin/cybercute-dev-tools$suffix.exe"
}
if (-not [System.IO.Path]::IsPathRooted($OutputPath)) {
    $OutputPath = Join-Path $root $OutputPath
}

$outputDirectory = Split-Path -Parent $OutputPath
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

$buildArgs = @("build", "-o", $OutputPath)
if ($Configuration -eq "Debug") {
    $buildArgs += "-gcflags=github.com/CyberCute-Studio/GameDevTools/...=-N -l"
} else {
    # These leave nothing in the executable that depends on where or when it
    # was built, so the same source and toolchain give the same bytes.
    $buildArgs += "-trimpath"
    $buildArgs += "-buildvcs=false"
    $buildArgs += "-ldflags=-s -w -H=windowsgui -buildid="
}
$buildArgs += "./cmd/gamedevtools"

Push-Location $root
try {
    & go @buildArgs
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}

Write-Output $OutputPath
