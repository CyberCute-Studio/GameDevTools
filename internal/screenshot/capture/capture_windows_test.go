//go:build windows

package capture

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

const (
	wsPopup        = 0x80000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procUnregisterClassW = user32.NewProc("UnregisterClassW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
)

// TestMain puts the test process into the same per-monitor DPI awareness V2
// context the production shell establishes in internal/app before any
// capture call. Without it the test binary is DPI-unaware and Windows
// virtualizes every coordinate these tests observe, so on any display not
// running at 100% scaling they would capture scaled regions instead of the
// physical pixels the package contract promises.
func TestMain(m *testing.M) {
	const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)
	proc := user32.NewProc("SetProcessDpiAwarenessContext")
	r, _, err := proc.Call(dpiAwarenessContextPerMonitorAwareV2)
	if r == 0 {
		fmt.Fprintf(os.Stderr, "TestMain: SetProcessDpiAwarenessContext failed, tests run DPI-unaware: %v\n", err)
	}
	os.Exit(m.Run())
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

var classCounter atomic.Uint32

func colorRef(c color.RGBA) uint32 {
	return uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16
}

// createSolidWindow shows a topmost popup filled with one known color, so
// capture tests can verify real composited pixel content. The window sits
// clear of display edges, where the taskbar and rounded DWM corners would
// otherwise pollute the sampled pixels.
func createSolidWindow(t *testing.T, monitor *display.Monitor, width, height int32, fill color.RGBA) (windows.Handle, display.Rect) {
	t.Helper()
	primary, err := monitor.PrimaryDisplay()
	if err != nil {
		t.Fatal(err)
	}
	if primary.Bounds.Width() < width+220 || primary.Bounds.Height() < height+220 {
		t.Skipf("primary display %v too small for a %d × %d test window", primary.Bounds, width, height)
	}
	region := display.Rect{
		Left:   primary.Bounds.Left + 110,
		Top:    primary.Bounds.Top + 110,
		Right:  primary.Bounds.Left + 110 + width,
		Bottom: primary.Bounds.Top + 110 + height,
	}

	className, err := windows.UTF16PtrFromString(fmt.Sprintf("GameDevToolsCaptureTest%d", classCounter.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	module, _, err := procGetModuleHandleW.Call(0)
	if module == 0 {
		t.Fatalf("GetModuleHandleW: %v", err)
	}
	brush, _, err := procCreateSolidBrush.Call(uintptr(colorRef(fill)))
	if brush == 0 {
		t.Fatalf("CreateSolidBrush: %v", err)
	}
	t.Cleanup(func() { procDeleteObject.Call(brush) })
	class := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   procDefWindowProcW.Addr(),
		HInstance:     windows.Handle(module),
		HbrBackground: windows.Handle(brush),
		LpszClassName: className,
	}
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		t.Fatalf("RegisterClassExW: %v", err)
	}
	t.Cleanup(func() { procUnregisterClassW.Call(uintptr(atom), module) })
	hwnd, _, err := procCreateWindowExW.Call(
		wsExTopmost|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), 0, wsPopup,
		uintptr(region.Left), uintptr(region.Top), uintptr(width), uintptr(height),
		0, 0, module, 0,
	)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(hwnd) })
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	return windows.Handle(hwnd), region
}

// insetPixelsMatch reports whether every pixel inside the image, minus a
// border that DWM corner rounding may leave unpainted, equals want.
func insetPixelsMatch(img *image.RGBA, want color.RGBA, inset int) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y + inset; y < bounds.Max.Y-inset; y++ {
		for x := bounds.Min.X + inset; x < bounds.Max.X-inset; x++ {
			offset := img.PixOffset(x, y)
			if img.Pix[offset] != want.R || img.Pix[offset+1] != want.G || img.Pix[offset+2] != want.B {
				return false
			}
		}
	}
	return true
}

// insetMatchCount counts pixels inside the inset region that equal want.
func insetMatchCount(img *image.RGBA, want color.RGBA, inset int) (matches, total int) {
	bounds := img.Bounds()
	for y := bounds.Min.Y + inset; y < bounds.Max.Y-inset; y++ {
		for x := bounds.Min.X + inset; x < bounds.Max.X-inset; x++ {
			offset := img.PixOffset(x, y)
			total++
			if img.Pix[offset] == want.R && img.Pix[offset+1] == want.G && img.Pix[offset+2] == want.B {
				matches++
			}
		}
	}
	return matches, total
}

// uniformlyBlack reports whether every captured pixel is black. A session
// with no composed display, such as a locked or headless CI session, reads
// back black for every capture.
func uniformlyBlack(img *image.RGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 || img.Pix[i+1] != 0 || img.Pix[i+2] != 0 {
			return false
		}
	}
	return true
}

// awaitColor captures the region until the composited window content
// appears, tolerating desktop-compositor latency. Content verification is
// impossible where Windows composites no display at all; that condition is
// only tolerated on CI, because an interactive run must fail loudly there.
func awaitColor(t *testing.T, monitor *display.Monitor, region display.Rect, want color.RGBA) *image.RGBA {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *image.RGBA
	for time.Now().Before(deadline) {
		img, err := CaptureRect(monitor, region)
		if err != nil {
			t.Fatalf("CaptureRect(%v): %v", region, err)
		}
		if insetPixelsMatch(img, want, 12) {
			return img
		}
		last = img
		time.Sleep(25 * time.Millisecond)
	}
	if last != nil && uniformlyBlack(last) && os.Getenv("CI") != "" {
		t.Skipf("CI session composites no display; captures are uniformly black")
	}
	t.Fatalf("color %v never appeared in capture of %v", want, region)
	return nil
}

func TestCaptureRectDimensionsAndContent(t *testing.T) {
	monitor := display.NewMonitor()
	want := color.RGBA{R: 0xC9, G: 0x14, B: 0x4B, A: 0xFF}
	_, region := createSolidWindow(t, monitor, 240, 180, want)
	img := awaitColor(t, monitor, region, want)
	if got := img.Bounds(); got.Dx() != int(region.Width()) || got.Dy() != int(region.Height()) {
		t.Fatalf("captured bounds = %v, want %d × %d", got, region.Width(), region.Height())
	}
}

func TestCaptureRectRejectsInvalidRegions(t *testing.T) {
	monitor := display.NewMonitor()
	displays, err := monitor.Displays()
	if err != nil {
		t.Fatal(err)
	}
	desktop := display.VirtualDesktopBounds(displays)
	regions := map[string]display.Rect{
		"empty":              {},
		"inverted":           {Left: desktop.Left + 10, Top: desktop.Top + 10, Right: desktop.Left, Bottom: desktop.Top},
		"outside left edge":  {Left: desktop.Left - 100, Top: desktop.Top, Right: desktop.Left, Bottom: desktop.Top + 10},
		"outside right edge": {Left: desktop.Right, Top: desktop.Top, Right: desktop.Right + 100, Bottom: desktop.Top + 10},
		"outside top edge":   {Left: desktop.Left, Top: desktop.Top - 100, Right: desktop.Left + 10, Bottom: desktop.Top},
		"outside bottom":     {Left: desktop.Left, Top: desktop.Bottom, Right: desktop.Left + 10, Bottom: desktop.Bottom + 100},
		"entirely outside":   {Left: desktop.Right + 10, Top: desktop.Bottom + 10, Right: desktop.Right + 110, Bottom: desktop.Bottom + 110},
	}
	for name, region := range regions {
		t.Run(name, func(t *testing.T) {
			img, err := CaptureRect(monitor, region)
			if img != nil {
				t.Fatalf("CaptureRect(%v) returned an image for an invalid region", region)
			}
			assertKind(t, err, KindValidation)
		})
	}
}

func TestCaptureDisplay(t *testing.T) {
	monitor := display.NewMonitor()
	displays, err := monitor.Displays()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range displays {
		t.Run(item.DeviceName, func(t *testing.T) {
			img, err := CaptureDisplay(monitor, item.DeviceName)
			if err != nil {
				t.Fatalf("CaptureDisplay(%s): %v", item.DeviceName, err)
			}
			if got := img.Bounds(); got.Dx() != int(item.Bounds.Width()) || got.Dy() != int(item.Bounds.Height()) {
				t.Fatalf("CaptureDisplay(%s) bounds = %v, want %d × %d at native dimensions",
					item.DeviceName, got, item.Bounds.Width(), item.Bounds.Height())
			}
		})
	}
}

func TestCaptureDisplayDisconnected(t *testing.T) {
	monitor := display.NewMonitor()
	img, err := CaptureDisplay(monitor, `\\.\DISPLAY9999`)
	if img != nil {
		t.Fatal("CaptureDisplay returned an image for a disconnected display")
	}
	assertKind(t, err, KindDisplay)

	img, err = CaptureDisplay(monitor, "")
	if img != nil {
		t.Fatal("CaptureDisplay returned an image for an empty device name")
	}
	assertKind(t, err, KindValidation)
}

func TestCaptureSpanningDisplayBoundary(t *testing.T) {
	monitor := display.NewMonitor()
	displays, err := monitor.Displays()
	if err != nil {
		t.Fatal(err)
	}
	if len(displays) < 2 {
		t.Skip("a rectangle spanning displays needs at least two connected displays")
	}
	center := func(r display.Rect) display.Point {
		return display.Point{X: (r.Left + r.Right) / 2, Y: (r.Top + r.Bottom) / 2}
	}
	a, b := center(displays[0].Bounds), center(displays[1].Bounds)
	region := display.Rect{Left: a.X, Top: a.Y, Right: b.X, Bottom: b.Y}
	if region.Right < region.Left {
		region.Left, region.Right = region.Right, region.Left
	}
	if region.Bottom < region.Top {
		region.Top, region.Bottom = region.Bottom, region.Top
	}
	region.Left -= 16
	region.Top -= 16
	region.Right += 16
	region.Bottom += 16
	img, err := CaptureRect(monitor, region)
	if err != nil {
		t.Fatalf("CaptureRect spanning %v: %v", region, err)
	}
	if got := img.Bounds(); got.Dx() != int(region.Width()) || got.Dy() != int(region.Height()) {
		t.Fatalf("spanning capture bounds = %v, want %d × %d", got, region.Width(), region.Height())
	}
}

func TestHideExcludesWindowFromCapture(t *testing.T) {
	monitor := display.NewMonitor()
	want := color.RGBA{R: 0x21, G: 0x9F, B: 0x53, A: 0xFF}
	hwnd, region := createSolidWindow(t, monitor, 240, 180, want)
	awaitColor(t, monitor, region, want)

	exclusion, err := Hide(uintptr(hwnd))
	if err != nil {
		t.Fatalf("Hide: %v", err)
	}
	img, err := CaptureRect(monitor, region)
	if err != nil {
		exclusion.Restore()
		t.Fatalf("CaptureRect while hidden: %v", err)
	}
	matches, total := insetMatchCount(img, want, 12)
	if total > 0 && matches*20 >= total {
		t.Fatalf("hidden window still visible in capture: %d of %d inset pixels match %v", matches, total, want)
	}

	exclusion.Restore()
	if r, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); r == 0 {
		t.Fatal("Restore did not show the window again")
	}
	awaitColor(t, monitor, region, want)
}

func TestCaptureConcurrentCallsDoNotDeadlock(t *testing.T) {
	monitor := display.NewMonitor()
	primary, err := monitor.PrimaryDisplay()
	if err != nil {
		t.Fatal(err)
	}
	region := display.Rect{
		Left:   primary.Bounds.Left,
		Top:    primary.Bounds.Top,
		Right:  primary.Bounds.Left + 64,
		Bottom: primary.Bounds.Top + 64,
	}
	const workers = 6
	const capturesEach = 4
	done := make(chan error, workers*capturesEach)
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < capturesEach; i++ {
				img, err := CaptureRect(monitor, region)
				if err != nil {
					done <- err
					return
				}
				if got := img.Bounds(); got.Dx() != 64 || got.Dy() != 64 {
					done <- fmt.Errorf("concurrent capture bounds = %v, want 64 × 64", got)
					return
				}
				done <- nil
			}
		}()
	}
	remaining := workers * capturesEach
	timeout := time.After(60 * time.Second)
	for remaining > 0 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent capture failed: %v", err)
			}
			remaining--
		case <-timeout:
			t.Fatal("concurrent captures did not finish within 60s; suspected deadlock")
		}
	}
}

func TestBitmapInfoStructLayout(t *testing.T) {
	// CreateDIBSection reads the header by size; a mismatched layout would
	// allocate a corrupt bitmap or fail outright.
	if got := unsafe.Sizeof(bitmapInfoHeader{}); got != 40 {
		t.Fatalf("bitmapInfoHeader size = %d, want 40", got)
	}
}
