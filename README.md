# GameDevTools

GameDevTools is a Windows desktop application implemented in Go. The repository contains the production project foundation under `cmd/gamedevtools` and `internal`, plus the separate **FND-01 technical spike** under `cmd/fnd01-spike`.

The production application provides a native Win32 main window with Screenshot, Image Resizer, and Settings navigation. Both feature modules include independent preset management. The Screenshot module configures its capture mode, output format, JPEG quality, and output folder, and captures a selected display, a user-drawn rectangle, or an exact preset-sized rectangle end to end. The Image Resizer module builds a deduplicated, per-file validated source list from a multi-select image picker or drag-and-drop, manages resize presets with their fitting modes, and configures the resize job's output folder, format, collision policy, JPEG quality, and JPEG transparency background; batch resizing arrives with the later Image Resizer items. The spike validates native Windows integration for the Screenshot module. See [ADR-0001](docs/adr/0001-native-windows-stack.md) for the stack decision, license inventory, verification results, and open mixed-DPI validation blocker.

## Production application

Go 1.24 or newer is required for development. No C compiler or CGO is needed. Built executables run without a Go installation or development toolchain.

Start the application shell with:

```bash
go run ./cmd/gamedevtools
```

Use the navigation controls or keyboard focus to switch between Screenshot, Image Resizer, and Settings. Each module retains its view object while another module is selected. In either feature module, use the preset fields to create a custom preset, Rename / Save to update the selected custom preset, Duplicate to copy any preset, and Delete to remove a custom preset after confirmation. Shared registration, navigation, and GUI-dispatch contracts live in `internal/shell`.

The Screenshot module also configures the capture mode (Free Rectangle, Preset Rectangle, or Full Display), the PNG/JPEG save format, JPEG quality from 1–100, the global capture shortcut, and the output folder, which defaults to `Pictures\GameDevTools\Screenshots` and is chosen through the Windows folder picker. All options persist across restarts. In Full Display mode a picker lists every connected display with its number, name where Windows provides one, native resolution, scaling, and primary status, defaulting to the display under the cursor and refreshing when the display topology changes. Start Capture hides the main window, captures the selected display at its native pixel dimensions, copies the original pixels to the clipboard, saves the timestamped file, restores the window without stealing focus, and reports the clipboard and save outcomes independently in a non-blocking notification with an Open Folder action. In Free Rectangle or Preset Rectangle mode Start Capture opens the selection session described below, where the drawn rectangle or the exact preset frame is captured and delivered the same way.

The global shortcut, `Ctrl+Alt+S` by default, starts the currently selected capture mode while another application has the foreground. It is registered on the main window, so it works even while that window is hidden behind a selection session; invoking it during a session brings the existing selection's toolbar back to the foreground instead of stacking a second overlay. A combination another application already owns is reported as a shortcut-registration conflict beside the field and in the status area, the previous combination keeps working, and a replacement typed into the shortcut field is validated, applied, and persisted without reinstalling. The shortcut is suspended while its field is being edited and released when the application exits.

The Image Resizer module accepts PNG and JPEG sources through a multi-select file picker and through files — or a folder of images — dropped anywhere on its view. Each added file's content is decoded in the background to validate it rather than trusting its extension, and the source list shows each file's name, upright dimensions once checked, and status, with the full path and a description — format, alpha channel, applied EXIF orientation, or the reason a file cannot be used — shown for the selected item; duplicates are skipped, and sources are opened read-only and never modified. Items can be removed or the whole list cleared before a job starts. The module persists its job configuration across restarts: the output folder, defaulting to `Pictures\GameDevTools\Resized` and chosen through the Windows folder picker; the output format — Keep Source Format, PNG, or JPEG; the collision policy — Rename, Skip, or Overwrite — with the selected policy's consequence restated beneath it; JPEG quality from 1–100; and the `#RRGGBB` background color transparent pixels are flattened against when JPEG output is produced. The selected preset's fitting mode is explained in plain language alongside, including the Stretch distortion warning. Start Resize stays disabled until the batch items land.

The selected module and normal window bounds persist across restarts. Only one instance runs per user session: a second launch activates the existing window — restoring it first when minimized — and exits, so global shortcuts and settings never conflict across instances. Settings use a versioned JSON file with atomic writes, migration hooks, and backup-before-recovery behavior. Diagnostic logs contain structured event metadata only and rotate by size. See [Local settings and diagnostics](docs/settings.md) for paths, schema, recovery, and redaction boundaries.

Independent Screenshot and Image Resizer preset libraries live in `internal/preset`. Built-in dimensions are immutable; custom presets have stable local identifiers, validated module-specific options, and persistent storage within their owning module's settings section. Malformed preset data uses the same backup-and-recovery flow as the rest of the settings file.

Source decoding lives in `internal/imageresizer/decode`. The package identifies PNG and JPEG content by its file signature, so a renamed file is decoded as what it actually contains; decodes it with the Go standard library decoders, which cover baseline and progressive JPEG (including CMYK and grayscale) and every standard PNG color type and bit depth; and applies the JPEG EXIF orientation — all eight standard values — so reported dimensions and pixels are upright. Failures are typed, so unreadable files, unrecognized content or unsupported coding features such as arithmetic-coded or 12-bit JPEG, damaged or truncated data, and oversized sources are reported distinctly. A source whose decoding would need more than 1 GiB of pixel memory — the decoded image at the decoder's pixel size for its color type, plus the decoder's working buffers, such as an interlaced PNG's pass images, a PNG's row buffers beyond a small allowance, a progressive JPEG's coefficients, or a JPEG's RGB conversion, and the upright copy of a reoriented JPEG — is rejected before anything is allocated. Metadata other than the normalized orientation is neither read nor preserved. The Image Resizer view runs these content checks one file at a time on a background goroutine, returning a large check's pixel memory before the next one starts, and hands the results to the GUI thread in batches through the shell dispatcher; a check abandoned because its source was removed is not waited for, so until it stops its memory adds to the next check's.

Resize geometry and resampling live in `internal/imageresizer/resize`. `Plan` computes the output dimensions from the upright source dimensions and the preset: Stretch produces exactly the preset dimensions; Fit scales uniformly by the smaller of the width and height ratios, rounds the other dimension to the nearest pixel (halves up), and clamps it to at least one pixel, so a 1000 × 500 source fits a 512 × 512 preset as 512 × 256; and Crop to Fill scales uniformly by the larger ratio, rounding the same way, then crops the overflowing axis equally from both sides, taking an odd leftover pixel from the right or bottom, so the same source scales to 1024 × 512 and keeps the centered 512 × 512. Every mode enlarges as readily as it reduces. Targets are validated with the preset library's own rules — 1 to 16,384 pixels per axis and at most 100 megapixels — and sources wider or taller than 1,048,576 pixels are rejected as too large, which keeps the filter's working memory bounded whatever the source's shape. `Resize` resamples with a separable Catmull-Rom cubic filter, widened when reducing so every source pixel contributes, filtering premultiplied alpha so transparent pixels lend no color to their neighbors, storing a pixel whose alpha rounds to zero as transparent black, and clamping the filter's overshoot; the stored sRGB values are filtered directly rather than in linear light, and JPEG YCbCr and CMYK pixels are converted to RGB exactly as the decoder converts a reoriented JPEG, so reorienting a picture does not change the colors the filter starts from. Crop to Fill resamples only the region it keeps. The output keeps the source's kind of pixels — grayscale, opaque color, or translucent color, at 8 or 16 bits per channel — and resizing to the source's own size changes no straight-alpha 8-bit, grayscale, or opaque pixel, except that a fully transparent pixel comes back as transparent black whatever color it stored. Results are bit-identical for the same source and preset on every 64-bit platform, the only kind the package builds for: sample positions are exact ratios, filter weights are quantized to fixed point from float64 arithmetic that cannot fuse into platform-dependent multiply-adds, each within one unit of its exact value, and pixels are filtered in integer arithmetic. The filter streams the source a row at a time, resampling rows or columns first according to a cost estimate that weighs how costly each kind of source pixel is to read, so beyond the output image it needs a few megabytes for typical sources and under 128 MiB in the worst case, and a context cancels it between source rows. Writing the output and running batches arrive with the later Image Resizer items.

Shared Win32 controls, dimension validation, confirmations, inline notifications, centralized strings, and accessibility conventions live in `internal/ui`. See [Shared UI and accessibility conventions](docs/ui.md) for keyboard, status, theme, and contrast guidance.

Display topology and physical-pixel coordinate services live in `internal/display`. The package enumerates connected displays with their device names, friendly monitor names where Windows provides them, native physical-pixel bounds, effective DPI, and primary status; assigns deterministic picker numbers (primary first); resolves the display under the cursor; computes virtual-desktop bounds with negative-coordinate support; converts explicitly between logical 96-DPI and physical-pixel measurements; and forwards display-topology (`WM_DISPLAYCHANGE`) and scaling (`WM_DPICHANGED`) notifications to subscribers through the shell window.

Raw screen capture lives in `internal/screenshot/capture`. The package captures one connected display at its current native pixel dimensions or an arbitrary virtual-desktop rectangle, including rectangles spanning monitors, and returns opaque physical-pixel RGBA images from off the GUI thread. The cursor is never part of the output, and app-owned surfaces are excluded by hiding them and flushing the desktop compositor before capture. Failures are typed so invalid geometry, a disconnected display, and native graphics failures are reported distinctly, and each kind is presented with its recovery path: a display that disconnected or a layout that changed mid-capture asks for a fresh display selection, and a native graphics failure names the failed operation and suggests switching a full-screen exclusive application or game to windowed mode. A failed capture never returns a blank image as success. Content the compositor excludes from capture, such as DRM-protected video or windows that opt out of capture, composites as black without an error, which the GDI capture path cannot distinguish from a genuinely black screen.

Screenshot output lives in `internal/screenshot/output`. The package delivers one captured image to both destinations — the Windows clipboard as a `CF_DIB` image and a PNG or JPEG file in the configured output folder — and reports the two outcomes independently, so a clipboard failure never discards a saved file and a save failure never discards a successful clipboard copy. The clipboard always receives the original captured pixels, never the output of JPEG encoding. Files are named `Screenshot_yyyyMMdd_HHmmssfff` with a numeric suffix when the name already exists, are encoded into a temporary file in the destination folder, and are finalized only after encoding succeeds, so existing files are never overwritten and a failed write leaves no partial image.

Capture execution sessions live in `internal/screenshot/execute`. The Full Display pipeline hides the main window, captures the selected display through `internal/screenshot/capture`, restores it, and delivers the image through `internal/screenshot/output` — synchronously off the GUI thread, with the Screenshot view posting the completion through the shell dispatcher. The rectangle pipeline captures the region an overlay session selected while the main window was already hidden: it flushes the desktop compositor so the just-destroyed session windows cannot linger in the frame, reads the pixels, restores the window the session passed in, and delivers the same way. Windows are restored on every path, including hide and capture failures.

Rectangle selection sessions live in `internal/screenshot/overlay`. Starting Free Rectangle or Preset Rectangle capture hides the main window and floats a topmost toolbar with Capture and Cancel commands near the selection. Only one session can be active at a time. The session surface never activates, so it cannot rise above the toolbar, while the toolbar is activated to take keyboard focus: Escape and Cancel end the session without producing output, Tab and Enter work through the shell's message filter, and the Capture command stays disabled until a selection exists. Cancelling, session-creation failure, and application exit all destroy the session windows explicitly and restore the main window, and every input-capturing surface is visible while it intercepts input, so no invisible input-capturing surface can be left behind. If the display topology changes mid-session — a display disconnects, or the resolution or layout changes — the session is revalidated against the new virtual desktop: a selection that still fits keeps its exact dimensions and is moved back inside while the tint overlay or preset frame and the toolbar are resized and repositioned, and a selection that can no longer fit ends the session with an explanatory warning instead of leaving a stale overlay behind. The Capture command destroys the session windows first and the capture pipeline flushes the desktop compositor, then reads the rectangle's pixels off the GUI thread while the main window is still hidden, restores the window, and delivers the image through `internal/screenshot/output` — so no session visual can reach the output.

In a Free Rectangle session the whole virtual desktop is covered with a dimmed topmost overlay. The user drags on the dimmed surface to draw the selection — its interior stays visually clear, painted at the minimum alpha so it keeps capturing the pointer input that moves and resizes it, while the live width and height show in the toolbar — then drags the interior to move it, drags the eight handles to resize it, or types exact dimensions; the toolbar follows the selection without covering it where practical and stays inside the virtual desktop on every display arrangement. The overlay renders through per-pixel alpha compositing: the desktop stays dimmed, the selection interior is painted at the minimum alpha so it stays visually clear, and the frame ring is painted outside the rectangle that will be captured, so even the handle squares astride the selection's edges cannot reach the output.

In a Preset Rectangle session the selected preset instead floats as an exact-size topmost frame above the desktop, centered on the cursor display. The frame's interior is fully transparent and click-through — clicks reach the applications beneath while the frame keeps its position and exact physical-pixel dimensions — and its opaque border ring is the drag handle that moves the frame, including across display boundaries, without ever resizing it; there are no resize handles. The toolbar shows the live width, height, and virtual-desktop X/Y coordinates; arrow keys nudge the frame by one physical pixel (Shift for ten), and typing exact dimensions resizes the active frame in place as an ad hoc edit that never modifies the saved preset. A preset larger than the entire virtual desktop is rejected with a validation message before the session opens, while a rectangle larger than one individual display is allowed whenever it fits the virtual desktop. The frame renders through the same per-pixel alpha compositing, with its border ring painted outside the rectangle that will be captured.

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

The Screenshot module's acceptance and performance verification is recorded in [Screenshot verification and acceptance results](docs/screenshot-verification.md): the repeatable test matrix for every Screenshot acceptance scenario, the NFR-011 overlay-appearance and NFR-012 capture-to-clipboard benchmarks, the environment of record the results were measured on, and the scenarios still gated on mixed-DPI hardware.

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
