# GameDevTools

GameDevTools is a Windows desktop application implemented in Go. The repository currently contains the **FND-01 technical spike**, not the production application.

The spike validates native Windows integration for the Screenshot module. See [ADR-0001](docs/adr/0001-native-windows-stack.md) for the stack decision, license inventory, verification results, and open validation blocker.

## Build

Go 1.24 or newer is required. No C compiler or CGO is needed.

```bash
go build -o bin/fnd01-spike.exe ./cmd/fnd01-spike
```

Cross-compile the initial 64-bit Intel/AMD target from another Windows architecture:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/fnd01-spike-amd64.exe ./cmd/fnd01-spike
```

Run formatting, static analysis, and package checks with:

```bash
gofmt -w cmd/fnd01-spike/*.go
go vet ./...
go test ./...
```

## Interactive demonstration

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

The self-test checks every connected display, exact physical overlay bounds and movement, a real click through the interior into a separate Win32 window, a real border drag, hotkey conflict reporting, capture dimensions and known RGB content, clipboard round-trip dimensions, and PNG decoding. It prints the temporary PNG path and leaves the captured image on the clipboard for interoperability checks.

To verify clipboard consumption through Windows Forms on PowerShell:

```powershell
Add-Type -AssemblyName System.Windows.Forms
$image = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -eq $image) { throw 'No interoperable clipboard image' }
Write-Output "$($image.Width) x $($image.Height), $($image.PixelFormat)"
$image.Dispose()
```

Run PowerShell in an STA session (`powershell.exe -STA`) if the host is not already configured that way.
