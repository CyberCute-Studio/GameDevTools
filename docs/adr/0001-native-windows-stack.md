# ADR-0001: Use native Win32 for the Go/Windows integration stack

- **Date:** 2026-11-10
- **Status:** Accepted for implementation, with the mixed-DPI validation blocker below
- **Requirements:** TECH-001, TECH-002, TECH-003, TECH-007

## Context

The Screenshot module depends on behavior that cannot be approximated by an ordinary GUI window: an always-on-top transparent frame, input only on its border, exact physical-pixel geometry across monitors, screen capture, clipboard images, and a system-wide hotkey. FND-01 requires these capabilities to be demonstrated before the production application is bootstrapped.

## Decision

Use Go with direct Win32 calls through `golang.org/x/sys/windows`. Use the Go standard library for in-memory images and PNG output. The production application can put shared Win32 bindings behind small native-service packages rather than spreading DLL calls through feature modules.

The spike uses:

- `SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2)` before creating windows.
- A UI goroutine locked to one OS thread so window creation, callbacks, and message retrieval use the same thread message queue.
- Physical virtual-desktop coordinates for `CreateWindowExW`, `SetWindowPos`, `GetWindowRect`, mouse movement, and capture. No logical-coordinate value is used as capture geometry.
- `WS_EX_TOPMOST | WS_EX_LAYERED`, a color key, a border-only window region, and explicit `WM_NCHITTEST` handling. The interior is unobscured and returns `HTTRANSPARENT`; the border accepts input.
- Mouse capture and physical cursor deltas for border dragging.
- `EnumDisplayMonitors`, `GetMonitorInfoW`, and `GetDpiForMonitor` for display geometry and scale reporting.
- Desktop `GetDC`, a top-down 32-bit DIB section, and `BitBlt(SRCCOPY | CAPTUREBLT)` for physical-pixel capture. The cursor is not drawn.
- `ShowWindow(SW_HIDE)` followed by `DwmFlush` before capturing an app-owned overlay rectangle.
- A bottom-up 32-bit `CF_DIB` for clipboard interoperability. Clipboard pixels come directly from the captured image, before any file compression.
- `RegisterHotKey`/`UnregisterHotKey` for global invocation and conflict detection.

## Alternatives considered

| Approach | Version reviewed | License | Decision |
|---|---|---|---|
| **Native Win32 + `golang.org/x/sys/windows`** | `x/sys v0.38.0` | BSD-3-Clause | **Selected.** Direct access to every required API; CGO is unnecessary; no runtime dependency. Higher native-integration and binding-maintenance cost is accepted. |
| Fyne | `v2.8.2` | BSD-3-Clause | Rejected. Its portable window abstraction does not expose the required border-only hit testing and native capture/clipboard contract directly. Those paths would still need Win32, while adding a larger GUI dependency and CGO toolchain requirements. |
| Gio | `v0.10.3` | MIT or Unlicense | Rejected. Custom native integration would still own the critical overlay, hotkey, capture, and clipboard behavior. The immediate-mode UI model adds complexity without removing the platform risk. |
| Wails | `v2.17.1` | MIT | Rejected. WebView2 adds a runtime/browser layer, while the Screenshot-critical behavior still requires native Go code. A browser window is not an advantage for precise physical-pixel overlays. |

License texts reviewed at the tagged sources: [`x/sys`](https://raw.githubusercontent.com/golang/sys/v0.38.0/LICENSE), [Fyne](https://raw.githubusercontent.com/fyne-io/fyne/v2.8.2/LICENSE), [Gio](https://raw.githubusercontent.com/gioui/gio/v0.10.3/LICENSE), and [Wails](https://raw.githubusercontent.com/wailsapp/wails/v2.17.1/LICENSE). The selected Go module has no third-party dependencies beyond `x/sys`; the Go toolchain and standard library use BSD-3-Clause. Release packaging must retain the required copyright and license notices.

## Verification on Windows

Verified with Go `1.26.5 windows/arm64`, without CGO, on two connected displays:

| Capability | Result |
|---|---|
| Physical display geometry | `DISPLAY1`: 1920 × 1200 at (3440,623); `DISPLAY2`: 3440 × 1440 at (0,0), primary. Both matched native monitor bounds. |
| Overlay geometry and movement | Passed on both displays with 321 × 241 physical-pixel windows; a 17 × -13 move preserved dimensions exactly. A 1 × 1 window retained its complete non-empty region. Signed 16-bit hit-test coordinate decoding was checked explicitly; a negative-coordinate display was not available. |
| Transparent, interactive overlay | A real interior mouse click reached a separate underlying Win32 window without changing overlay geometry. A real border drag moved exactly 40 × 30 physical pixels without resizing. |
| Hotkey conflict | A second registration was rejected with the required `ERROR_HOTKEY_ALREADY_REGISTERED` (1409). |
| Raw capture | A 320 × 240 capture had exact dimensions and every pixel had the expected known RGB content after the overlay was hidden. |
| Clipboard | Native `CF_DIB` round-trip passed. Windows Forms returned a 320 × 240 `Format32bppRgb` image through `Clipboard.GetImage()`. |
| PNG | The written file decoded successfully at 320 × 240. |
| Target build | Native `windows/arm64` build and `windows/amd64` CGO-free cross-build succeeded. `gofmt`, `go vet ./...`, and `go test ./...` succeeded. |

The exact reproducible commands are in the repository `README.md`. The self-test is a real interactive Windows check, not a headless unit test.

## Limitations and open blocker

- **Mixed-DPI validation blocker:** both available displays reported 96 DPI (100% scaling), so different scaling settings and a cross-DPI move were not exercised. Per-monitor V2 awareness and physical-only coordinates are implemented, but mixed-DPI behavior is not claimed as verified. Run the self-test with displays at different scaling levels before treating this validation item as complete or starting dependent Screenshot capture work.
- The development host is Windows ARM64. The initial AMD64 target was cross-compiled but not executed on this host; run the AMD64 binary on the supported Windows 10/11 matrix in later validation.
- GDI `BitBlt` demonstrates the raw capture path, not every protected, HDR, remote-session, or exclusive-fullscreen case. SCR-03 must surface capture failures and evaluate those cases; this spike does not claim protected-content support.
- The spike uses GDI, a color-keyed overlay, and synchronous capture for minimal validation. It is not the production application shell, resize implementation, final accessibility design, or atomic file-output implementation.
