<#
.SYNOPSIS
Runs the QLT-04 platform validation and writes one JSON result file.

.DESCRIPTION
Records the machine, its displays, and whether it runs without administrator
rights; runs static analysis, the whole test suite, the release executable end
to end, and the release build; and measures NFR-010 through NFR-014 against
their targets. The report holds no computer name, user name, or monitor name.

Run it with nothing else using the desktop. It moves windows, takes the
foreground, and REPLACES THE CLIPBOARD. Close GameDevTools first.

.PARAMETER Architecture
The architecture to build and run: the host's by default. On Windows on Arm,
amd64 runs the release target under Windows' x64 emulation.

.PARAMETER ReferenceMachine
Asserts this is the PRD's reference machine: 64-bit Intel or AMD, four or more
cores, 16 GB RAM installed, Windows 11, not elevated, and not on a hard disk. The
script refuses a machine that is not, and a target that is missed or not measured
then fails the run. Elsewhere the performance results are recorded without
failing it. Windows does not report a CPU's age, so whoever passes this switch
confirms the CPU is from 2020 or later; the report records its name.

.PARAMETER OutputDirectory
Where the result file goes; relative paths are resolved from the repository root.
#>
[CmdletBinding()]
param(
    [ValidateSet("native", "amd64")]
    [string]$Architecture = "native",
    [switch]$ReferenceMachine,
    [string]$OutputDirectory = "bin/validation"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$invariant = [System.Globalization.CultureInfo]::InvariantCulture
if (-not [System.IO.Path]::IsPathRooted($OutputDirectory)) {
    $OutputDirectory = Join-Path $root $OutputDirectory
}
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

$checks = New-Object System.Collections.ArrayList

# Invoke-Go runs one go command, echoing its output, and returns its lines and
# whether it succeeded. Native stderr must not become a terminating error here.
function Invoke-Go {
    param(
        [string]$Name,
        [string[]]$Arguments,
        [hashtable]$Environment = @{}
    )
    Write-Host "==> $Name"
    $saved = @{}
    foreach ($key in $Environment.Keys) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key)
        [Environment]::SetEnvironmentVariable($key, $Environment[$key])
    }
    $watch = [System.Diagnostics.Stopwatch]::StartNew()
    $previous = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $lines = @(& go @Arguments 2>&1 | ForEach-Object {
            $line = "$_"
            Write-Host $line
            $line
        })
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previous
        foreach ($key in $saved.Keys) {
            [Environment]::SetEnvironmentVariable($key, $saved[$key])
        }
    }
    $watch.Stop()
    return [pscustomobject]@{
        Name    = $Name
        Lines   = $lines
        Passed  = ($exitCode -eq 0)
        Seconds = [math]::Round($watch.Elapsed.TotalSeconds, 1)
    }
}

function Add-Check {
    param([string]$Name, [bool]$Passed, [double]$Seconds, [string]$Detail = "")
    [void]$checks.Add([ordered]@{ name = $Name; passed = $Passed; seconds = $Seconds; detail = $Detail })
}

# Get-SourceState reads the commit and the working tree's status, leaving out a
# result folder inside the repository, whose files are this script's own.
function Get-SourceState {
    $arguments = @("status", "--porcelain")
    $repository = [System.IO.Path]::GetFullPath($root).TrimEnd('\', '/')
    $results = [System.IO.Path]::GetFullPath($OutputDirectory).TrimEnd('\', '/')
    if ($results.StartsWith($repository + [System.IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        $relative = $results.Substring($repository.Length + 1).Replace('\', '/')
        $arguments += @("--", ".", ":(exclude,literal)$relative")
    }
    return [pscustomobject]@{
        Commit = (& git rev-parse HEAD).Trim()
        Status = (@(& git @arguments) -join "`n")
    }
}

function Read-Benchmark {
    param([string[]]$Lines, [string]$Name)
    $results = New-Object System.Collections.ArrayList
    foreach ($line in $Lines) {
        if ($line -match "^$Name(-\d+)?\s+\d+\s+(.*)$") {
            $fields = $Matches[2].Trim() -split '\s+'
            $metrics = @{}
            for ($i = 0; $i + 1 -lt $fields.Count; $i += 2) {
                $metrics[$fields[$i + 1]] = [double]::Parse($fields[$i], $invariant)
            }
            [void]$results.Add($metrics)
        }
    }
    return , $results
}

function New-Target {
    param([string]$Requirement, [string]$Measure, [string]$Unit, [double]$Limit, $Value)
    $status = "not-measured"
    $rounded = $null
    if ($null -ne $Value) {
        $rounded = [math]::Round([double]$Value, 1)
        $status = if ([double]$Value -le $Limit) { "met" } else { "missed" }
    }
    return [ordered]@{ requirement = $Requirement; measure = $Measure; unit = $Unit; limit = $Limit; value = $rounded; status = $status }
}

Push-Location $root
try {
    # The processor, not the Go toolchain: an x64 Go under emulation on Windows
    # on Arm reports amd64 as its host.
    $processors = @(Get-CimInstance Win32_Processor)
    $hostArchitecture = switch ([int]$processors[0].Architecture) {
        9 { "amd64" }
        12 { "arm64" }
        default { throw "Unrecognized processor architecture $($processors[0].Architecture)." }
    }
    $target = if ($Architecture -eq "native") { $hostArchitecture } else { $Architecture }
    $goEnvironment = @{ GOOS = "windows"; GOARCH = $target; CGO_ENABLED = "0" }

    # Read before anything is built or run, so the report names the source the
    # results came from.
    $sourceAtStart = Get-SourceState

    $os = Get-CimInstance Win32_OperatingSystem
    $computer = Get-CimInstance Win32_ComputerSystem
    $cores = ($processors | Measure-Object -Property NumberOfCores -Sum).Sum
    $memoryGiB = [math]::Round($computer.TotalPhysicalMemory / 1GB, 1)
    # Usable memory excludes what firmware and an integrated GPU reserve, so the
    # installed amount is what decides a 16 GB machine. A virtual machine may
    # report no modules.
    $installedBytes = (@(Get-CimInstance Win32_PhysicalMemory) | Measure-Object -Property Capacity -Sum).Sum
    $installedGiB = if ($installedBytes -gt 0) { [math]::Round($installedBytes / 1GB, 1) } else { $memoryGiB }
    $systemDiskMedia = "Unknown"
    try {
        $systemDisk = Get-Partition -DriveLetter $env:SystemDrive[0] | Get-Disk
        $physicalDisk = Get-PhysicalDisk | Where-Object { $_.DeviceId -eq "$($systemDisk.Number)" }
        if ($physicalDisk) { $systemDiskMedia = "$($physicalDisk.MediaType)" }
    } catch {
        Write-Host "Could not read the system disk's type: $_"
    }
    $version = Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion"
    $build = [int]$os.BuildNumber
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    $elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    # Windows 11 is build 22000 or later, on a client edition (product type 1).
    # This does not say whether Microsoft still supports the release.
    $windows11Client = ($build -ge 22000) -and ($os.ProductType -eq 1)

    if ($ReferenceMachine) {
        $refused = @()
        if ($target -ne "amd64" -or $hostArchitecture -ne "amd64") { $refused += "it is not 64-bit Intel or AMD" }
        if ($cores -lt 4) { $refused += "it has $cores cores" }
        # A virtual machine's fallback is usable memory, slightly under 16 GB.
        if ($installedGiB -lt 15) { $refused += "it has $installedGiB GB of memory installed" }
        if ($systemDiskMedia -eq "HDD") { $refused += "its system drive is a hard disk" }
        if (-not $windows11Client) { $refused += "it is not Windows 11" }
        if ($elevated) { $refused += "this shell is elevated" }
        if ($refused.Count -gt 0) {
            throw "This is not the reference machine: $($refused -join '; ')."
        }
    }

    $vet = Invoke-Go -Name "go vet" -Arguments @("vet", "./...") -Environment $goEnvironment
    Add-Check -Name "static analysis" -Passed $vet.Passed -Seconds $vet.Seconds

    $suite = Invoke-Go -Name "test suite" -Arguments @("test", "-count=1", "-timeout=30m", "./...") -Environment $goEnvironment
    Add-Check -Name "combined test suite" -Passed $suite.Passed -Seconds $suite.Seconds

    $layoutFile = Join-Path $OutputDirectory "displays-$PID.json"
    $layoutEnvironment = $goEnvironment + @{ GAMEDEVTOOLS_VALIDATION_DISPLAYS = $layoutFile }
    $layout = Invoke-Go -Name "display layout" -Arguments @("test", "-count=1", "-run", "^TestRecordDisplayLayout$", "./internal/display") -Environment $layoutEnvironment
    $displays = @()
    if ($layout.Passed -and (Test-Path -LiteralPath $layoutFile)) {
        # Windows PowerShell passes a JSON array down the pipeline as one object,
        # so ForEach-Object unrolls it into one item per display on either edition.
        $displays = @(Get-Content -Raw -LiteralPath $layoutFile | ConvertFrom-Json | ForEach-Object { $_ })
        Remove-Item -LiteralPath $layoutFile
    }
    Add-Check -Name "display layout recorded" -Passed ($displays.Count -gt 0) -Seconds $layout.Seconds

    $validationEnvironment = $goEnvironment + @{ GAMEDEVTOOLS_VALIDATION = "1" }
    $launchTest = "TestReleaseExecutableRunsAsStandardUserAndExitsCleanly"
    $release = Invoke-Go -Name "release executable" -Arguments @("test", "-count=1", "-v", "-run", "^$launchTest$", "./internal/app") -Environment $validationEnvironment
    # A skip also exits 0, so only the test's own PASS line counts.
    $ranRelease = $release.Passed -and (@($release.Lines | Where-Object { $_ -match "^--- PASS: $launchTest" }).Count -eq 1)
    Add-Check -Name "release executable as a standard user" -Passed $ranRelease -Seconds $release.Seconds

    $buildPath = Join-Path $OutputDirectory "gamedevtools-$target.exe"
    $buildWatch = [System.Diagnostics.Stopwatch]::StartNew()
    $buildPassed = $true
    $buildDetail = ""
    $savedGoEnvironment = @{}
    foreach ($key in $goEnvironment.Keys) {
        $savedGoEnvironment[$key] = [Environment]::GetEnvironmentVariable($key)
        [Environment]::SetEnvironmentVariable($key, $goEnvironment[$key])
    }
    try {
        & (Join-Path $PSScriptRoot "build.ps1") -Configuration Release -OutputPath $buildPath | Out-Null
    } catch {
        $buildPassed = $false
        $buildDetail = "$_"
    } finally {
        foreach ($key in $savedGoEnvironment.Keys) {
            [Environment]::SetEnvironmentVariable($key, $savedGoEnvironment[$key])
        }
    }
    Add-Check -Name "release build" -Passed $buildPassed -Seconds ([math]::Round($buildWatch.Elapsed.TotalSeconds, 1)) -Detail $buildDetail

    function Invoke-Benchmark {
        param([string]$Name, [string]$Package, [string]$Pattern, [string[]]$Options, [hashtable]$Environment)
        # The workflow's job limit allows for this timeout on every benchmark.
        $arguments = @("test", "-run", "^$", "-timeout=15m", "-bench", $Pattern) + $Options + @($Package)
        $result = Invoke-Go -Name "benchmark: $Name" -Arguments $arguments -Environment $Environment
        Add-Check -Name "benchmark: $Name" -Passed $result.Passed -Seconds $result.Seconds
        return $result
    }

    $launch = Invoke-Benchmark -Name "launch" -Package "./internal/app" -Pattern "^BenchmarkWarmLaunchToResponsiveMainWindow$" -Options @("-benchtime=20x") -Environment $validationEnvironment
    $overlay = Invoke-Benchmark -Name "overlay" -Package "./internal/screenshot/overlay" -Pattern "^BenchmarkOverlayAppear" -Options @("-benchtime=30x") -Environment $goEnvironment
    $capture = Invoke-Benchmark -Name "capture" -Package "./internal/screenshot/capture" -Pattern "^BenchmarkCaptureRectVirtualDesktop$" -Options @("-benchtime=30x") -Environment $goEnvironment
    $clipboard = Invoke-Benchmark -Name "clipboard" -Package "./internal/screenshot/output" -Pattern "^BenchmarkSetClipboardImage4K$" -Options @("-benchtime=30x") -Environment $goEnvironment
    $batch = Invoke-Benchmark -Name "batch" -Package "./internal/imageresizer/job" -Pattern "^BenchmarkRun100Mixed12MegapixelJPEGsFit1024$" -Options @("-benchtime=1x", "-count=3") -Environment $goEnvironment

    $launchRuns = Read-Benchmark -Lines $launch.Lines -Name "BenchmarkWarmLaunchToResponsiveMainWindow"
    $freeRuns = Read-Benchmark -Lines $overlay.Lines -Name "BenchmarkOverlayAppearFreeRectangle"
    $presetRuns = Read-Benchmark -Lines $overlay.Lines -Name "BenchmarkOverlayAppearPresetRectangle"
    $captureRuns = Read-Benchmark -Lines $capture.Lines -Name "BenchmarkCaptureRectVirtualDesktop"
    $clipboardRuns = Read-Benchmark -Lines $clipboard.Lines -Name "BenchmarkSetClipboardImage4K"
    $batchRuns = Read-Benchmark -Lines $batch.Lines -Name "BenchmarkRun100Mixed12MegapixelJPEGsFit1024"

    $launchMs = if ($launchRuns.Count -gt 0) { $launchRuns[0]["ns/op"] / 1e6 } else { $null }
    $launchMaxMs = if ($launchRuns.Count -gt 0) { $launchRuns[0]["max-ms"] } else { $null }
    $freeMs = if ($freeRuns.Count -gt 0) { $freeRuns[0]["ns/op"] / 1e6 } else { $null }
    $freeMaxMs = if ($freeRuns.Count -gt 0) { $freeRuns[0]["max-ms"] } else { $null }
    $presetMs = if ($presetRuns.Count -gt 0) { $presetRuns[0]["ns/op"] / 1e6 } else { $null }
    $presetMaxMs = if ($presetRuns.Count -gt 0) { $presetRuns[0]["max-ms"] } else { $null }

    # NFR-012 is defined at 3840 x 2160. The capture benchmark reads the whole
    # virtual desktop, so its throughput is converted to that region's bytes.
    $captureToClipboardMs = $null
    if ($captureRuns.Count -gt 0 -and $clipboardRuns.Count -gt 0 -and $captureRuns[0]["MB/s"] -gt 0) {
        $fourKMegabytes = 3840 * 2160 * 4 / 1e6
        $captureToClipboardMs = ($fourKMegabytes / $captureRuns[0]["MB/s"] * 1000) + ($clipboardRuns[0]["ns/op"] / 1e6)
    }

    $batchMs = $null
    $peakHeap = $null
    $peakRuntime = $null
    if ($batchRuns.Count -gt 0) {
        $batchMs = ($batchRuns | ForEach-Object { $_["ns/op"] / 1e6 } | Measure-Object -Maximum).Maximum
        $peakHeap = ($batchRuns | ForEach-Object { $_["peak-heap-MiB"] } | Measure-Object -Maximum).Maximum
        $peakRuntime = ($batchRuns | ForEach-Object { $_["peak-runtime-MiB"] } | Measure-Object -Maximum).Maximum
    }

    $targets = @(
        (New-Target "NFR-010" "warm launch to a responsive main window, mean of 20" "ms" 2000 $launchMs),
        (New-Target "NFR-010" "warm launch, slowest of 20" "ms" 2000 $launchMaxMs),
        (New-Target "NFR-011" "Free Rectangle overlay appears, mean of 30" "ms" 300 $freeMs),
        (New-Target "NFR-011" "Free Rectangle overlay appears, slowest of 30" "ms" 300 $freeMaxMs),
        (New-Target "NFR-011" "Preset Rectangle overlay appears, mean of 30" "ms" 300 $presetMs),
        (New-Target "NFR-011" "Preset Rectangle overlay appears, slowest of 30" "ms" 300 $presetMaxMs),
        (New-Target "NFR-012" "3840 x 2160 capture to clipboard" "ms" 1000 $captureToClipboardMs),
        (New-Target "NFR-013" "100 mixed 12 MP JPEGs to 1024 x 1024, slowest of 3" "ms" 120000 $batchMs),
        (New-Target "NFR-014" "peak heap during the batch" "MiB" 1024 $peakHeap)
    )

    $scales = @($displays | ForEach-Object { [int]$_.scalePercent } | Sort-Object -Unique)
    $negative = @($displays | Where-Object { $_.x -lt 0 -or $_.y -lt 0 }).Count -gt 0
    $goVersion = (& go version).Trim()

    # Each step compiled the working tree as it stood then, so a change during
    # the run would leave results from more than one state of the source.
    $sourceAtEnd = Get-SourceState
    $sourceUnchanged = ($sourceAtEnd.Commit -eq $sourceAtStart.Commit) -and ($sourceAtEnd.Status -eq $sourceAtStart.Status)
    Add-Check -Name "source unchanged during the run" -Passed $sourceUnchanged -Seconds 0

    $checksPassed = @($checks | Where-Object { -not $_.passed }).Count -eq 0
    $targetsMet = @($targets | Where-Object { $_.status -ne "met" }).Count -eq 0
    $passed = $checksPassed -and ((-not $ReferenceMachine) -or $targetsMet)

    $report = [ordered]@{
        schema         = 2
        recordedUtc    = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ", $invariant)
        commit         = $sourceAtStart.Commit
        dirtyTree      = [bool]$sourceAtStart.Status
        go             = $goVersion
        target         = "windows/$target"
        environment    = [ordered]@{
            os                = $os.Caption
            displayVersion    = $version.DisplayVersion
            build             = $build
            revision          = $version.UBR
            hostArchitecture  = $hostArchitecture
            windows11Client   = $windows11Client
            processor         = $processors[0].Name
            cores             = $cores
            logicalProcessors = $computer.NumberOfLogicalProcessors
            memoryGiB         = $memoryGiB
            installedGiB      = $installedGiB
            systemDiskMedia   = $systemDiskMedia
            elevated          = $elevated
            referenceMachine  = [bool]$ReferenceMachine
        }
        displays       = [ordered]@{
            connected           = $displays
            scalesPercent       = $scales
            mixedScaling        = ($scales.Count -gt 1)
            negativeCoordinates = $negative
        }
        checks         = $checks
        targets        = $targets
        peakRuntimeMiB = $peakRuntime
        passed         = $passed
    }

    $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss", $invariant)
    $reportPath = Join-Path $OutputDirectory "validation-$target-$build-$stamp.json"
    $json = $report | ConvertTo-Json -Depth 6
    [System.IO.File]::WriteAllText($reportPath, $json, (New-Object System.Text.UTF8Encoding($false)))

    Write-Host ""
    Write-Host "Windows build $build, windows/$target, scaling: $($scales -join ', ')%, mixed: $($scales.Count -gt 1), negative coordinates: $negative, elevated: $elevated"
    foreach ($check in $checks) {
        Write-Host ("{0,-6} {1}" -f $(if ($check.passed) { "pass" } else { "FAIL" }), $check.name)
    }
    foreach ($item in $targets) {
        Write-Host ("{0,-12} {1}: {2} {3} (limit {4}) - {5}" -f $item.status, $item.requirement, $item.value, $item.unit, $item.limit, $item.measure)
    }
    Write-Host "Peak runtime footprint during the batch: $peakRuntime MiB"
    Write-Output $reportPath

    if (-not $passed) {
        throw "Validation failed; see $reportPath."
    }
} finally {
    Pop-Location
}
