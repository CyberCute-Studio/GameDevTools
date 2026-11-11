# GameDevTools

GameDevTools is a Windows desktop application implemented in Go. The repository contains the production project foundation under `cmd/gamedevtools` and `internal`, plus the separate **FND-01 technical spike** under `cmd/fnd01-spike`.

The production application provides a native Win32 main window with Screenshot, Image Resizer, and Settings navigation. Both feature modules include independent preset management; capture, resize, and settings behavior belongs to their later implementation items. The spike validates native Windows integration for the Screenshot module. See [ADR-0001](docs/adr/0001-native-windows-stack.md) for the stack decision, license inventory, verification results, and open mixed-DPI validation blocker.

## Production application

Go 1.24 or newer is required for development. No C compiler or CGO is needed. Built executables run without a Go installation or development toolchain.

Start the application shell with:

```bash
go run ./cmd/gamedevtools
```

Use the navigation controls or keyboard focus to switch between Screenshot, Image Resizer, and Settings. Each module retains its view object while another module is selected. In either feature module, use the preset fields to create a custom preset, Rename / Save to update the selected custom preset, Duplicate to copy any preset, and Delete to remove a custom preset after confirmation. Shared registration, navigation, and GUI-dispatch contracts live in `internal/shell`.

The selected module and normal window bounds persist across restarts. Settings use a versioned JSON file with atomic writes, migration hooks, and backup-before-recovery behavior. Diagnostic logs contain structured event metadata only and rotate by size. See [Local settings and diagnostics](docs/settings.md) for paths, schema, recovery, and redaction boundaries.

Independent Screenshot and Image Resizer preset libraries live in `internal/preset`. Built-in dimensions are immutable; custom presets have stable local identifiers, validated module-specific options, and persistent storage within their owning module's settings section. Malformed preset data uses the same backup-and-recovery flow as the rest of the settings file.

Shared Win32 controls, dimension validation, confirmations, inline notifications, centralized strings, and accessibility conventions live in `internal/ui`. See [Shared UI and accessibility conventions](docs/ui.md) for keyboard, status, theme, and contrast guidance.

Run formatting, static analysis, and unit tests from the repository root with:

```bash
go fmt ./...
go vet ./...
go test -count=1 ./...
```

Build a debug executable with symbols, optimizations disabled, and a console attached:

```powershell
./scripts/build.ps1 -Configuration Debug
```

Build the smaller Windows GUI release executable:

```powershell
./scripts/build.ps1 -Configuration Release
```

The default outputs are `bin/gamedevtools-debug.exe` and `bin/gamedevtools.exe`. Override the destination with `-OutputPath`; relative paths are resolved from the repository root.

To build the initial 64-bit Intel/AMD target from another Windows architecture:

```powershell
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
./scripts/build.ps1 -Configuration Release -OutputPath bin/gamedevtools-windows-amd64.exe
```

Windows CI runs the same formatting, static-analysis, test, and release-build checks and uploads the AMD64 executable as an artifact.

## FND-01 technical spike

Build the spike directly with:

```bash
go build -o bin/fnd01-spike.exe ./cmd/fnd01-spike
```

Cross-compile its initial 64-bit Intel/AMD target from another Windows architecture:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/fnd01-spike-amd64.exe ./cmd/fnd01-spike
```

### Interactive demonstration

```bash
go run ./cmd/fnd01-spike
```

- `Ctrl+Alt+S` shows the topmost overlay. A registration conflict is reported without terminating the program.
- Drag the 8 px border to move the overlay. Its interior is transparent and click-through.
- Arrow keys move it by one physical pixel; `Shift+Arrow` moves by ten.
- `F9` hides the overlay, captures its rectangle, writes a `CF_DIB` clipboard image, and saves `fnd01-capture.png`.
- `Esc` closes the overlay and exits.

Optional flags set the initial physical-pixel geometry and output path:

```bash
go run ./cmd/fnd01-spike -x 100 -y 100 -width 1280 -height 720 -output capture.png
```

The compiled executable accepts the same flags directly.

## Native self-test

**Warning:** the self-test moves the mouse, sends real clicks, creates temporary topmost windows, and replaces the clipboard image. Do not interact with the desktop while it runs.

```bash
go run ./cmd/fnd01-spike -self-test
```

The self-test checks every connected display, signed hit-test coordinate decoding, exact physical overlay bounds and movement, a non-empty 1 × 1 overlay region, a real click through the interior into a separate Win32 window without moving the overlay, a real border drag, the specific hotkey conflict error, capture dimensions and known RGB content at every pixel, clipboard round-trip dimensions, and PNG decoding. It prints the temporary PNG path and leaves the captured image on the clipboard for interoperability checks.

To verify clipboard consumption through Windows Forms on PowerShell:

```powershell
Add-Type -AssemblyName System.Windows.Forms
$image = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -eq $image) { throw 'No interoperable clipboard image' }
Write-Output "$($image.Width) x $($image.Height), $($image.PixelFormat)"
$image.Dispose()
```

Run PowerShell in an STA session (`powershell.exe -STA`) if the host is not already configured that way.
