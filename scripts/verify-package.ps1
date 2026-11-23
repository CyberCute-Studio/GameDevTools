<#
.SYNOPSIS
Checks a portable ZIP the way a user receives it.

.DESCRIPTION
Confirms the checksum, contents, x64 architecture, version metadata, icon, and signing state, then
extracts the ZIP and runs the executable with no Go on its PATH and its per-user
folders in a temporary workspace: once as a first run and once as an upgrade
from a GameDevTools settings file. Close the application first.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Package
)

$ErrorActionPreference = "Stop"
$productName = "CyberCute Dev Tools"
$executableName = "cybercute-dev-tools.exe"
$windowWait = 30
$exitWait = 30

# GetClassLongPtrW, which the icon check calls, is only a header macro in 32-bit
# user32.dll, so a 32-bit host would stop there on a valid package.
if ([IntPtr]::Size -ne 8) {
    throw "Run this from 64-bit PowerShell: a 32-bit host cannot read the main window's icon."
}

Add-Type -Namespace PackageCheck -Name Native -MemberDefinition @'
[DllImport("user32.dll")]
public static extern IntPtr GetClassLongPtrW(IntPtr window, int index);
[DllImport("user32.dll", CharSet = CharSet.Unicode)]
public static extern IntPtr FindWindowW(string className, string windowName);
[DllImport("user32.dll")]
public static extern uint GetWindowThreadProcessId(IntPtr window, out uint processId);
[DllImport("user32.dll")]
[return: MarshalAs(UnmanagedType.Bool)]
public static extern bool IsWindowVisible(IntPtr window);
[DllImport("user32.dll")]
[return: MarshalAs(UnmanagedType.Bool)]
public static extern bool PostMessageW(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);
[DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
public static extern IntPtr LoadLibraryExW(string path, IntPtr file, uint flags);
[DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
public static extern IntPtr FindResourceW(IntPtr module, IntPtr name, IntPtr type);
[DllImport("kernel32.dll")]
[return: MarshalAs(UnmanagedType.Bool)]
public static extern bool FreeLibrary(IntPtr module);
'@

function Write-Check([string]$Name) { Write-Output "PASS  $Name" }

function Test-InstanceRunning {
    $held = $null
    try {
        if ([System.Threading.Mutex]::TryOpenExisting("Local\GameDevTools.SingleInstance", [ref]$held)) {
            $held.Dispose()
            return $true
        }
    } catch [System.UnauthorizedAccessException] {
        return $true
    }
    return $false
}

# Every entry under the folder, each file with its hash, so a shipped file
# rewritten in place shows as a change as much as a file added or removed.
function Get-FolderState([string]$Folder) {
    return @(Get-ChildItem -LiteralPath $Folder -Recurse -Force | Sort-Object FullName | ForEach-Object {
        if ($_.PSIsContainer) { $_.FullName } else { "$($_.FullName)=$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash)" }
    })
}

function New-Workspace([string]$Parent, [string]$Name) {
    $workspace = Join-Path $Parent $Name
    foreach ($folder in "Roaming", "Local", "Temp", "Profile") {
        New-Item -ItemType Directory -Force -Path (Join-Path $workspace $folder) | Out-Null
    }
    return $workspace
}

# Runs the executable until its window appears, closes it, and requires a clean
# exit. The environment holds only what Windows needs, so no Go tool is on PATH.
function Invoke-App([string]$Executable, [string]$Workspace) {
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = $Executable
    $start.WorkingDirectory = Split-Path -Parent $Executable
    $start.UseShellExecute = $false
    $start.EnvironmentVariables.Clear()
    $start.EnvironmentVariables["SystemRoot"] = $env:SystemRoot
    $start.EnvironmentVariables["SystemDrive"] = $env:SystemDrive
    $start.EnvironmentVariables["windir"] = $env:windir
    $start.EnvironmentVariables["PATH"] = Join-Path $env:SystemRoot "System32"
    $start.EnvironmentVariables["USERPROFILE"] = Join-Path $Workspace "Profile"
    $start.EnvironmentVariables["APPDATA"] = Join-Path $Workspace "Roaming"
    $start.EnvironmentVariables["LOCALAPPDATA"] = Join-Path $Workspace "Local"
    $start.EnvironmentVariables["TEMP"] = Join-Path $Workspace "Temp"
    $start.EnvironmentVariables["TMP"] = Join-Path $Workspace "Temp"

    $process = [System.Diagnostics.Process]::Start($start)
    try {
        # The main window is found by its title, as Process.MainWindowHandle can
        # name the result card, which appears at startup when another
        # application holds the global shortcut.
        $deadline = (Get-Date).AddSeconds($windowWait)
        $mainWindow = [IntPtr]::Zero
        while ($true) {
            if ($process.HasExited) { throw "The application exited with code $($process.ExitCode) before showing a window." }
            $candidate = [PackageCheck.Native]::FindWindowW([NullString]::Value, $productName)
            if ($candidate -ne [IntPtr]::Zero -and [PackageCheck.Native]::IsWindowVisible($candidate)) {
                $owner = [uint32]0
                [void][PackageCheck.Native]::GetWindowThreadProcessId($candidate, [ref]$owner)
                if ($owner -eq $process.Id) {
                    $mainWindow = $candidate
                    break
                }
            }
            if ((Get-Date) -gt $deadline) { throw "The application showed no window titled '$productName' within $windowWait seconds." }
            Start-Sleep -Milliseconds 100
        }
        # GCLP_HICON and GCLP_HICONSM: the title bar and taskbar show these.
        foreach ($index in -14, -34) {
            if ([PackageCheck.Native]::GetClassLongPtrW($mainWindow, $index) -eq [IntPtr]::Zero) {
                throw "The main window's class has no icon (index $index)."
            }
        }
        $wmClose = [uint32]0x0010
        if (-not [PackageCheck.Native]::PostMessageW($mainWindow, $wmClose, [IntPtr]::Zero, [IntPtr]::Zero)) { throw "The main window could not be asked to close." }
        if (-not $process.WaitForExit($exitWait * 1000)) { throw "The application did not exit within $exitWait seconds." }
        if ($process.ExitCode -ne 0) { throw "The application exited with code $($process.ExitCode)." }
    } finally {
        if (-not $process.HasExited) { $process.Kill() }
        $process.Dispose()
    }
}

$Package = (Resolve-Path -LiteralPath $Package).Path
if (Test-InstanceRunning) { throw "$productName is running. Close it and run this again." }

$checksumFile = "$Package.sha256"
if (-not (Test-Path -LiteralPath $checksumFile)) { throw "No checksum file at $checksumFile." }
$recorded = ((Get-Content -LiteralPath $checksumFile -Raw).Trim() -split '\s+')
$actual = (Get-FileHash -LiteralPath $Package -Algorithm SHA256).Hash.ToLowerInvariant()
if ($recorded[0] -ne $actual) { throw "The checksum does not match: recorded $($recorded[0]), actual $actual." }
if ($recorded[1] -ne (Split-Path -Leaf $Package)) { throw "The checksum file names $($recorded[1])." }
Write-Check "SHA-256 checksum"

$scratch = Join-Path ([System.IO.Path]::GetTempPath()) ("cybercute-package-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $scratch | Out-Null
try {
    $extracted = Join-Path $scratch "Extracted"
    Expand-Archive -LiteralPath $Package -DestinationPath $extracted
    $folder = Join-Path $extracted $productName
    # Names come from the ZIP rather than the extracted paths, which Windows
    # may report in a longer form than an 8.3 TEMP path gives them.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($Package)
    try {
        $found = @($zip.Entries | ForEach-Object { $_.FullName } | Sort-Object)
    } finally {
        $zip.Dispose()
    }
    $wanted = @("README.txt", "RELEASE-NOTES.txt", "THIRD-PARTY-NOTICES.txt", "USER-GUIDE.txt", $executableName) | ForEach-Object { "$productName/$_" } | Sort-Object
    if (($found -join "|") -cne ($wanted -join "|")) { throw "The ZIP holds: $($found -join ', ')." }
    Write-Check "contents"

    $executable = Join-Path $folder $executableName
    # The launch checks below pass for any build this machine can run, so an
    # arm64 executable in a ZIP named x64 needs its PE header read.
    $image = [System.IO.File]::ReadAllBytes($executable)
    if ($image.Length -lt 64 -or $image[0] -ne 0x4D -or $image[1] -ne 0x5A) { throw "The executable has no DOS header." }
    $header = [BitConverter]::ToInt32($image, 0x3C)
    if ($header -lt 0 -or $header -gt $image.Length - 6 -or
        $image[$header] -ne 0x50 -or $image[$header + 1] -ne 0x45 -or $image[$header + 2] -ne 0 -or $image[$header + 3] -ne 0) {
        throw "The executable has no PE header."
    }
    $machine = [BitConverter]::ToUInt16($image, $header + 4)
    if ($machine -ne 0x8664) { throw ("The executable's machine type is 0x{0:X4}, not x64 (0x8664)." -f $machine) }
    Write-Check "x64 executable"

    $info = [System.Diagnostics.FileVersionInfo]::GetVersionInfo($executable)
    if ($info.ProductName -cne $productName) { throw "ProductName is '$($info.ProductName)'." }
    if ($info.OriginalFilename -cne $executableName) { throw "OriginalFilename is '$($info.OriginalFilename)'." }
    if ((Split-Path -Leaf $Package) -notmatch "-$([regex]::Escape($info.ProductVersion))-windows-x64\.zip$") {
        throw "The executable's version $($info.ProductVersion) is not the one the ZIP is named for."
    }
    Write-Check "version metadata ($($info.ProductVersion))"

    # The shell gives a generic icon for a file with none, so the check reads
    # the resource: RT_GROUP_ICON (14), ID 1, loaded as data so an x64 file can
    # be inspected from any architecture.
    $module = [PackageCheck.Native]::LoadLibraryExW($executable, [IntPtr]::Zero, 0x22)
    if ($module -eq [IntPtr]::Zero) { throw "The executable could not be opened to read its icon." }
    try {
        if ([PackageCheck.Native]::FindResourceW($module, [IntPtr]1, [IntPtr]14) -eq [IntPtr]::Zero) {
            throw "The executable has no application icon resource."
        }
    } finally {
        [void][PackageCheck.Native]::FreeLibrary($module)
    }
    Write-Check "application icon"

    $signature = Get-AuthenticodeSignature -LiteralPath $executable
    if ($signature.Status -ne "NotSigned") { throw "The release is meant to be unsigned; its signature status is $($signature.Status)." }
    Write-Check "unsigned, as ADR-0002 decides"

    $before = Get-FolderState $folder

    $firstRun = New-Workspace $scratch "FirstRun"
    Invoke-App -Executable $executable -Workspace $firstRun
    $settings = Join-Path $firstRun "Roaming\$productName\settings.json"
    if (-not (Test-Path -LiteralPath $settings)) { throw "The first run wrote no settings at $settings." }
    Write-Check "first run without Go: window, clean exit, settings path"

    $upgrade = New-Workspace $scratch "Upgrade"
    $legacyFolder = Join-Path $upgrade "Roaming\GameDevTools"
    New-Item -ItemType Directory -Path $legacyFolder | Out-Null
    $legacy = Join-Path $legacyFolder "settings.json"
    $legacyText = '{"version":1,"shell":{"selectedModule":"image-resizer","window":{"x":0,"y":0,"width":1100,"height":700,"maximized":false,"hasPosition":false}}}'
    [System.IO.File]::WriteAllText($legacy, $legacyText)
    Invoke-App -Executable $executable -Workspace $upgrade
    $adopted = Join-Path $upgrade "Roaming\$productName\settings.json"
    if (-not (Test-Path -LiteralPath $adopted)) { throw "The upgrade wrote no settings at $adopted." }
    $selected = (Get-Content -LiteralPath $adopted -Raw | ConvertFrom-Json).shell.selectedModule
    if ($selected -ne "image-resizer") { throw "The upgrade selected '$selected' rather than keeping image-resizer." }
    if ([System.IO.File]::ReadAllText($legacy) -cne $legacyText) { throw "The upgrade changed the GameDevTools settings file." }
    Write-Check "upgrade keeps GameDevTools settings and leaves the old file"

    $after = Get-FolderState $folder
    if (($before -join "|") -cne ($after -join "|")) { throw "Running the application changed its extracted folder." }
    Write-Check "portable: nothing written beside the executable"
} finally {
    # A virus scanner or the x64 emulation cache can hold a program that has
    # just exited for a moment.
    for ($attempt = 0; $attempt -lt 20 -and (Test-Path -LiteralPath $scratch); $attempt++) {
        Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $scratch) { Start-Sleep -Milliseconds 250 }
    }
}
