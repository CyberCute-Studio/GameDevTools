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
    $OutputPath = "bin/gamedevtools$suffix.exe"
}
if (-not [System.IO.Path]::IsPathRooted($OutputPath)) {
    $OutputPath = Join-Path $root $OutputPath
}

$outputDirectory = Split-Path -Parent $OutputPath
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

$buildArgs = @("build", "-o", $OutputPath)
if ($Configuration -eq "Debug") {
    $buildArgs += "-gcflags=all=-N -l"
} else {
    $buildArgs += "-ldflags=-s -w -H=windowsgui"
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
