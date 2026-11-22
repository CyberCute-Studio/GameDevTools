<#
.SYNOPSIS
Builds the portable x64 ZIP and its SHA-256 checksum file.

.DESCRIPTION
Builds the release executable for 64-bit Intel/AMD Windows with the toolchain
go.mod pins, then packages it with the README and third-party notices. The same
commit gives the same bytes, wherever it is checked out and whatever the
machine's Go configuration.
#>
[CmdletBinding()]
param(
    [string]$OutputDirectory = "bin/release"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
if (-not [System.IO.Path]::IsPathRooted($OutputDirectory)) {
    $OutputDirectory = Join-Path $root $OutputDirectory
}

$toolchain = (Select-String -Path (Join-Path $root "go.mod") -Pattern '^toolchain (go\S+)$').Matches.Groups[1].Value
if (-not $toolchain) { throw "go.mod names no toolchain." }

$executable = Join-Path $OutputDirectory "cybercute-dev-tools.exe"
# Where modules come from and where they are kept cannot change the bytes, as
# go.sum pins the modules and -trimpath drops the paths, but a machine may need
# them to build at all, so they outlive the env file being turned off below.
$kept = "GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GOSUMDB", "GOINSECURE", "GOVCS", "GOAUTH", "GOMODCACHE", "GOCACHE"
$saved = @{}
foreach ($name in @("GOENV", "GOTOOLCHAIN", "GOOS", "GOARCH", "GOAMD64", "CGO_ENABLED", "GOFLAGS", "GOEXPERIMENT", "GOFIPS140", "GOWORK") + $kept) {
    $saved[$name] = [Environment]::GetEnvironmentVariable($name)
}
try {
    # Reading them needs no particular toolchain, so none is fetched for it.
    $env:GOTOOLCHAIN = "local"
    $configured = & go env -json @kept
    if ($LASTEXITCODE -ne 0) { throw "go env failed with exit code $LASTEXITCODE" }
    if ($configured) {
        $values = ($configured -join "`n") | ConvertFrom-Json
        foreach ($name in $kept) {
            [Environment]::SetEnvironmentVariable($name, $values.$name)
        }
    }

    # An emptied variable falls back to the user's `go env -w` file, so that
    # file is turned off: nothing else in this machine's Go configuration can
    # change the bytes or raise the CPU the executable needs above baseline
    # x64. A go.work file in a parent folder could change the modules linked,
    # so workspace mode is off too.
    $env:GOENV = "off"
    $env:GOWORK = "off"
    $env:GOTOOLCHAIN = $toolchain
    $env:GOFLAGS = ""
    $env:GOEXPERIMENT = ""
    $env:GOFIPS140 = ""
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:GOAMD64 = "v1"
    $env:CGO_ENABLED = "0"
    & (Join-Path $PSScriptRoot "build.ps1") -Configuration Release -OutputPath $executable | Out-Null

    # The packager runs on this machine, so it builds for this machine: with
    # the env file off, no GOOS or GOARCH means the host, whatever the caller's
    # shell had set.
    $env:GOOS = ""
    $env:GOARCH = ""
    Push-Location $root
    try {
        $archive = & go run ./cmd/release package -exe $executable -packaging packaging -out $OutputDirectory
        if ($LASTEXITCODE -ne 0) { throw "packaging failed with exit code $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
} finally {
    foreach ($name in $saved.Keys) {
        [Environment]::SetEnvironmentVariable($name, $saved[$name])
    }
}

Write-Output $archive
