# GameDevTools

GameDevTools is a Windows desktop application implemented in Go. The repository contains the production project foundation under `cmd/gamedevtools` and `internal`, plus the separate **FND-01 technical spike** under `cmd/fnd01-spike`.

The production application provides a native Win32 main window with Screenshot, Image Resizer, and Settings navigation. Both feature modules include independent preset management, and the Screenshot module configures its capture mode, output format, JPEG quality, and output folder, and captures a selected display end to end from the main window; rectangle capture and resize behavior belong to their later implementation items. The spike validates native Windows integration for the Screenshot module. See [ADR-0001](docs/adr/0001-native-windows-stack.md) for the stack decision, license inventory, verification results, and open mixed-DPI validation blocker.

## Production application

Go 1.24 or newer is required for development. No C compiler or CGO is needed. Built executables run without a Go installation or development toolchain.

Start the application shell with:

```bash
go run ./cmd/gamedevtools
```

Use the navigation controls or keyboard focus to switch between Screenshot, Image Resizer, and Settings. Each module retains its view object while another module is selected. In either feature module, use the preset fields to create a custom preset, Rename / Save to update the selected custom preset, Duplicate to copy any preset, and Delete to remove a custom preset after confirmation. Shared registration, navigation, and GUI-dispatch contracts live in `internal/shell`.

The Screenshot module also configures the capture mode (Free Rectangle, Preset Rectangle, or Full Display), the PNG/JPEG save format, JPEG quality from 1–100, and the output folder, which defaults to `Pictures\GameDevTools\Screenshots` and is chosen through the Windows folder picker. All options persist across restarts. In Full Display mode a picker lists every connected display with its number, name where Windows provides one, native resolution, scaling, and primary status, defaulting to the display under the cursor and refreshing when the display topology changes. Start Capture hides the main window, captures the selected display at its native pixel dimensions, copies the original pixels to the clipboard, saves the timestamped file, restores the window without stealing focus, and reports the clipboard and save outcomes independently in a non-blocking notification with an Open Folder action. In the rectangle modes Start Capture opens the selection overlay session described below; drawing the selection and capturing it belong to later Screenshot items.

The selected module and normal window bounds persist across restarts. Settings use a versioned JSON file with atomic writes, migration hooks, and backup-before-recovery behavior. Diagnostic logs contain structured event metadata only and rotate by size. See [Local settings and diagnostics](docs/settings.md) for paths, schema, recovery, and redaction boundaries.

Independent Screenshot and Image Resizer preset libraries live in `internal/preset`. Built-in dimensions are immutable; custom presets have stable local identifiers, validated module-specific options, and persistent storage within their owning module's settings section. Malformed preset data uses the same backup-and-recovery flow as the rest of the settings file.

Shared Win32 controls, dimension validation, confirmations, inline notifications, centralized strings, and accessibility conventions live in `internal/ui`. See [Shared UI and accessibility conventions](docs/ui.md) for keyboard, status, theme, and contrast guidance.

Display topology and physical-pixel coordinate services live in `internal/display`. The package enumerates connected displays with their device names, friendly monitor names where Windows provides them, native physical-pixel bounds, effective DPI, and primary status; assigns deterministic picker numbers (primary first); resolves the display under the cursor; computes virtual-desktop bounds with negative-coordinate support; converts explicitly between logical 96-DPI and physical-pixel measurements; and forwards `WM_DISPLAYCHANGE` notifications to subscribers through the shell window.

Raw screen capture lives in `internal/screenshot/capture`. The package captures one connected display at its current native pixel dimensions or an arbitrary virtual-desktop rectangle, including rectangles spanning monitors, and returns opaque physical-pixel RGBA images from off the GUI thread. The cursor is never part of the output, and app-owned surfaces are excluded by hiding them and flushing the desktop compositor before capture. Failures are typed so invalid geometry, a disconnected display, and native graphics failures are reported distinctly; a failed capture never returns a blank image as success. Content the compositor excludes from capture, such as DRM-protected video or windows that opt out of capture, composites as black without an error, which the GDI capture path cannot distinguish from a genuinely black screen.

Screenshot output lives in `internal/screenshot/output`. The package delivers one captured image to both destinations — the Windows clipboard as a `CF_DIB` image and a PNG or JPEG file in the configured output folder — and reports the two outcomes independently, so a clipboard failure never discards a saved file and a save failure never discards a successful clipboard copy. The clipboard always receives the original captured pixels, never the output of JPEG encoding. Files are named `Screenshot_yyyyMMdd_HHmmssfff` with a numeric suffix when the name already exists, are encoded into a temporary file in the destination folder, and are finalized only after encoding succeeds, so existing files are never overwritten and a failed write leaves no partial image.

Full Display capture sessions live in `internal/screenshot/execute`. The pipeline hides the main window, captures the selected display through `internal/screenshot/capture`, restores the window, and delivers the image through `internal/screenshot/output` — synchronously off the GUI thread, with the Screenshot view posting the completion through the shell dispatcher. The window is restored on every path, including hide and capture failures.

Rectangle selection sessions live in `internal/screenshot/overlay`. Starting Free Rectangle or Preset Rectangle capture hides the main window, covers the whole virtual desktop with a translucent black topmost overlay, and floats a topmost toolbar with Capture and Cancel commands over the cursor display. Only one session can be active at a time. The overlay never activates, so it cannot rise above the toolbar, while the toolbar is activated to take keyboard focus: Escape and Cancel end the session without producing output, Tab and Enter work through the shell's message filter, and the Capture command stays disabled until a selection exists — selection drawing arrives with the Free Rectangle item. The session windows are unowned popups, so cancelling, completing, and exiting the application all destroy them explicitly and restore the main window; a failed start restores the window before reporting the error, and the always-visible tint guarantees no invisible input-capturing surface can be left behind.

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
