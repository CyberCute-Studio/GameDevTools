//go:build windows

package capture

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"runtime"
	"sync"
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

	pmRemove = 0x0001
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
	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")

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
		// Running DPI-unaware would virtualize every coordinate these
		// tests observe and could pass while capturing scaled regions,
		// defeating the physical-pixel contract under test.
		fmt.Fprintf(os.Stderr, "TestMain: SetProcessDpiAwarenessContext failed, aborting: %v\n", err)
		os.Exit(1)
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

// win32Message mirrors the Win32 MSG struct. The pump below never reads
// the message contents, so only the layout has to be right.
type win32Message struct {
	Hwnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var (
	windowThreadOnce sync.Once
	windowThreadJobs = make(chan func())
)

// startWindowThread launches one locked OS thread that owns every test
// window and pumps its message queue. ShowWindow delivers window messages
// synchronously through the owning thread, so a cross-thread ShowWindow
// from Hide or Restore blocks until the owner pumps. The test process has
// no message loop of its own, and a Go goroutine migrates between OS
// threads, so without this pump Hide deadlocks whenever the runtime
// schedules the caller onto a thread other than the window's creator —
// the same contract the production GUI thread satisfies by pumping.
func startWindowThread() {
	windowThreadOnce.Do(func() {
		go func() {
			runtime.LockOSThread()
			var msg win32Message
			for {
				select {
				case job := <-windowThreadJobs:
					job()
				default:
					time.Sleep(time.Millisecond)
				}
				for {
					r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
					if r == 0 {
						break
					}
					procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
					procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
				}
			}
		}()
	})
}

// runOnWindowThread runs job on the window-owning thread and blocks until
// it finishes. job must not call t.Fatal or its siblings: FailNow is only
// legal on the test's own goroutine.
func runOnWindowThread(job func()) {
	done := make(chan struct{})
	windowThreadJobs <- func() {
		job()
		close(done)
	}
	<-done
}

func colorRef(c color.RGBA) uint32 {
	return uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16
}

// createSolidWindow shows a topmost popup filled with one known color, so
// capture tests can verify real composited pixel content. The window sits
// clear of display edges, where the taskbar and rounded DWM corners would
// otherwise pollute the sampled pixels.
func createSolidWindow(t *testing.T, monitor *display.Monitor, width, height int32, fill color.RGBA) (windows.Handle, display.Rect) {
	t.Helper()
	startWindowThread()
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
	return showSolidWindow(t, region, fill), region
}

// showSolidWindow shows a topmost popup filled with one known color over
// region, in physical pixels.
func showSolidWindow(t *testing.T, region display.Rect, fill color.RGBA) windows.Handle {
	t.Helper()
	startWindowThread()
	width, height := region.Width(), region.Height()

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
	atom &= 0xffff // the ATOM is 16 bits; the rest of the return register is unspecified
	if atom == 0 {
		t.Fatalf("RegisterClassExW: %v", err)
	}
	t.Cleanup(func() { procUnregisterClassW.Call(uintptr(atom), module) })

	// The window is created, shown, and eventually destroyed on the
	// window-owning thread so that its message queue is pumped; a window
	// created on the test goroutine would be owned by an arbitrary runtime
	// thread that never pumps.
	var hwnd uintptr
	var createErr error
	runOnWindowThread(func() {
		var callErr error
		hwnd, _, callErr = procCreateWindowExW.Call(
			wsExTopmost|wsExToolWindow,
			uintptr(unsafe.Pointer(className)), 0, wsPopup,
			uintptr(region.Left), uintptr(region.Top), uintptr(width), uintptr(height),
			0, 0, module, 0,
		)
		if hwnd == 0 {
			createErr = callErr
			return
		}
		// SW_SHOWNA keeps the test window from taking activation
		// away from the user's foreground window on interactive runs; the
		// topmost popup composites either way.
		procShowWindow.Call(hwnd, swShowNA)
		procUpdateWindow.Call(hwnd)
	})
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", createErr)
	}
	t.Cleanup(func() { runOnWindowThread(func() { procDestroyWindow.Call(hwnd) }) })
	return windows.Handle(hwnd)
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

// seamSample is one pixel of a capture beside a seam of the quadrant pattern,
// and the color that belongs there.
type seamSample struct {
	x, y int
	want color.RGBA
}

// quadrantSeamSamples returns the pixels on both sides of the two seams where
// four colors, in reading order, meet at (centerX, centerY) in a width × height
// image. They are taken halfway along each seam, clear of the corners DWM may
// round, so a capture displaced or scaled by even one pixel fails.
func quadrantSeamSamples(width, height, centerX, centerY int, colors [4]color.RGBA) []seamSample {
	upper, lower := centerY/2, centerY+(height-centerY)/2
	left, right := centerX/2, centerX+(width-centerX)/2
	return []seamSample{
		{centerX - 1, upper, colors[0]}, {centerX, upper, colors[1]},
		{centerX - 1, lower, colors[2]}, {centerX, lower, colors[3]},
		{left, centerY - 1, colors[0]}, {left, centerY, colors[2]},
		{right, centerY - 1, colors[1]}, {right, centerY, colors[3]},
	}
}

func TestQuadrantSeamSamplesStraddleBothSeams(t *testing.T) {
	colors := [4]color.RGBA{{R: 1}, {R: 2}, {R: 3}, {R: 4}}
	want := []seamSample{
		{59, 20, colors[0]}, {60, 20, colors[1]},
		{59, 70, colors[2]}, {60, 70, colors[3]},
		{30, 39, colors[0]}, {30, 40, colors[2]},
		{90, 39, colors[1]}, {90, 40, colors[3]},
	}
	got := quadrantSeamSamples(120, 100, 60, 40, colors)
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// validationCaptureEnv names the file
// TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries writes for
// scripts/validate.ps1. The test runs only when it is set: it reads exact
// pixels, and the script runs packages one at a time, while a plain go test
// can run the overlay package's full-desktop tint beside it.
const validationCaptureEnv = "GAMEDEVTOOLS_VALIDATION_CAPTURE"

// capturedRegion is one region the test checked, as the validation report
// records it. Displays are picker numbers, since a report holds no monitor name.
type capturedRegion struct {
	Displays      []int  `json:"displays"`
	ScalesPercent []int  `json:"scalesPercent"`
	X             int32  `json:"x"`
	Y             int32  `json:"y"`
	Width         int32  `json:"width"`
	Height        int32  `json:"height"`
	Result        string `json:"result"`
}

// TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries is AC-S02 on the
// displays connected now. Over a preset-sized region centered on each display
// and across each edge two displays share, it shows four windows of known
// colors meeting at the region's center, on the shared edge when there is one,
// so no window spans two displays. The capture must have the region's exact
// physical size and each color on its own side of both seams, which a capture
// that is displaced or scaled does not. It writes each region, the scaling of
// the displays involved, and its result for the validation report, so a run on
// a mixed-DPI or negative-coordinate arrangement records what it covered,
// including a region it had to skip.
func TestCaptureExactPresetOnEveryDisplayAndAcrossBoundaries(t *testing.T) {
	coveragePath := os.Getenv(validationCaptureEnv)
	if coveragePath == "" {
		t.Skipf("%s is not set; scripts/validate.ps1 runs this test", validationCaptureEnv)
	}
	const presetWidth, presetHeight = 1280, 720
	// Each quadrant must be large enough that a seam's midpoint is clear of
	// DWM's rounded corners.
	const smallestQuadrant = 48
	colors := [4]color.RGBA{
		{R: 0xC9, G: 0x14, B: 0x4B, A: 0xFF},
		{R: 0x21, G: 0x9F, B: 0x53, A: 0xFF},
		{R: 0x1F, G: 0x4F, B: 0xD8, A: 0xFF},
		{R: 0xE8, G: 0xB4, B: 0x0C, A: 0xFF},
	}
	monitor := display.NewMonitor()
	displays, err := monitor.Displays()
	if err != nil {
		t.Fatal(err)
	}
	covered := []capturedRegion{}
	check := func(t *testing.T, region display.Rect, shown ...display.Display) {
		t.Helper()
		t.Cleanup(func() {
			entry := capturedRegion{X: region.Left, Y: region.Top, Width: region.Width(), Height: region.Height(), Result: "passed"}
			for _, item := range shown {
				entry.Displays = append(entry.Displays, item.Number)
				entry.ScalesPercent = append(entry.ScalesPercent, item.ScalePercent())
			}
			switch {
			case t.Failed():
				entry.Result = "failed"
			case t.Skipped():
				entry.Result = "skipped"
			}
			covered = append(covered, entry)
		})
		centerX, centerY := (region.Left+region.Right)/2, (region.Top+region.Bottom)/2
		if min(centerX-region.Left, region.Right-centerX, centerY-region.Top, region.Bottom-centerY) < smallestQuadrant {
			t.Skipf("%v is too small to compare pixels beside its seams", region)
		}
		showSolidWindow(t, display.Rect{Left: region.Left, Top: region.Top, Right: centerX, Bottom: centerY}, colors[0])
		showSolidWindow(t, display.Rect{Left: centerX, Top: region.Top, Right: region.Right, Bottom: centerY}, colors[1])
		showSolidWindow(t, display.Rect{Left: region.Left, Top: centerY, Right: centerX, Bottom: region.Bottom}, colors[2])
		showSolidWindow(t, display.Rect{Left: centerX, Top: centerY, Right: region.Right, Bottom: region.Bottom}, colors[3])
		samples := quadrantSeamSamples(int(region.Width()), int(region.Height()), int(centerX-region.Left), int(centerY-region.Top), colors)

		var img *image.RGBA
		var wrong *seamSample
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(25 * time.Millisecond) {
			captured, err := CaptureRect(monitor, region)
			if err != nil {
				t.Fatalf("CaptureRect(%v): %v", region, err)
			}
			img = captured
			if got := img.Bounds(); got.Dx() != int(region.Width()) || got.Dy() != int(region.Height()) {
				t.Fatalf("capture of %v is %d × %d, want %d × %d", region, got.Dx(), got.Dy(), region.Width(), region.Height())
			}
			wrong = nil
			for i := range samples {
				got := img.RGBAAt(samples[i].x, samples[i].y)
				if got.R != samples[i].want.R || got.G != samples[i].want.G || got.B != samples[i].want.B {
					wrong = &samples[i]
					break
				}
			}
			if wrong == nil {
				return
			}
			if time.Now().After(deadline) {
				break
			}
		}
		if uniformlyBlack(img) && os.Getenv("CI") != "" {
			t.Skipf("CI session composites no display; captures are uniformly black")
		}
		t.Fatalf("capture of %v has %v at (%d, %d), want %v", region, img.RGBAAt(wrong.x, wrong.y), wrong.x, wrong.y, wrong.want)
	}
	for _, item := range displays {
		t.Run(item.DeviceName, func(t *testing.T) {
			t.Logf("%s at (%d, %d), %d × %d, %d%% scaling", item.DeviceName, item.Bounds.Left, item.Bounds.Top, item.Bounds.Width(), item.Bounds.Height(), item.ScalePercent())
			width, height := min(presetWidth, item.Bounds.Width()), min(presetHeight, item.Bounds.Height())
			left := item.Bounds.Left + (item.Bounds.Width()-width)/2
			top := item.Bounds.Top + (item.Bounds.Height()-height)/2
			check(t, display.Rect{Left: left, Top: top, Right: left + width, Bottom: top + height}, item)
		})
	}
	for i, a := range displays {
		for _, b := range displays[i+1:] {
			region, shared := regionAcrossSharedEdge(a.Bounds, b.Bounds, presetWidth, presetHeight)
			if !shared {
				continue
			}
			t.Run(a.DeviceName+" to "+b.DeviceName, func(t *testing.T) {
				t.Logf("across %d%% and %d%% scaling: %v", a.ScalePercent(), b.ScalePercent(), region)
				check(t, region, a, b)
			})
		}
	}
	encoded, err := json.Marshal(covered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coveragePath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

// regionAcrossSharedEdge returns a region of at most width × height centered
// on the edge a and b share, inside the two displays, and whether they share
// one.
func regionAcrossSharedEdge(a, b display.Rect, width, height int32) (display.Rect, bool) {
	across := func(edge, spanStart, spanEnd, reachA, reachB, along, thick int32) (start, end, crossStart, crossEnd int32, ok bool) {
		if spanEnd <= spanStart {
			return 0, 0, 0, 0, false
		}
		half := min(along/2, reachA, reachB)
		size := min(thick, spanEnd-spanStart)
		crossStart = spanStart + (spanEnd-spanStart-size)/2
		return edge - half, edge + half, crossStart, crossStart + size, half > 0
	}
	switch {
	case a.Right == b.Left || b.Right == a.Left:
		edge, left, right := a.Right, a, b
		if b.Right == a.Left {
			edge, left, right = a.Left, b, a
		}
		x0, x1, y0, y1, ok := across(edge, max(a.Top, b.Top), min(a.Bottom, b.Bottom), left.Width(), right.Width(), width, height)
		return display.Rect{Left: x0, Top: y0, Right: x1, Bottom: y1}, ok
	case a.Bottom == b.Top || b.Bottom == a.Top:
		edge, upper, lower := a.Bottom, a, b
		if b.Bottom == a.Top {
			edge, upper, lower = a.Top, b, a
		}
		y0, y1, x0, x1, ok := across(edge, max(a.Left, b.Left), min(a.Right, b.Right), upper.Height(), lower.Height(), height, width)
		return display.Rect{Left: x0, Top: y0, Right: x1, Bottom: y1}, ok
	}
	return display.Rect{}, false
}

func TestRegionAcrossSharedEdge(t *testing.T) {
	for _, test := range []struct {
		name   string
		a, b   display.Rect
		want   display.Rect
		shared bool
	}{
		{"side by side", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Left: 1920, Right: 3840, Bottom: 1080},
			display.Rect{Left: 1280, Top: 180, Right: 2560, Bottom: 900}, true},
		{"b left of a, negative coordinates", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Left: -1920, Right: 0, Bottom: 1080},
			display.Rect{Left: -640, Top: 180, Right: 640, Bottom: 900}, true},
		{"stacked", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Top: 1080, Right: 1920, Bottom: 2160},
			display.Rect{Left: 320, Top: 720, Right: 1600, Bottom: 1440}, true},
		{"narrow neighbor limits the reach", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Left: 1920, Right: 2020, Bottom: 1080},
			display.Rect{Left: 1820, Top: 180, Right: 2020, Bottom: 900}, true},
		{"short overlap limits the height", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Left: 1920, Top: 980, Right: 3840, Bottom: 2060},
			display.Rect{Left: 1280, Top: 980, Right: 2560, Bottom: 1080}, true},
		{"touching only at a corner", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Left: 1920, Top: 1080, Right: 3840, Bottom: 2160}, display.Rect{}, false},
		{"apart", display.Rect{Right: 1920, Bottom: 1080}, display.Rect{Left: 2000, Right: 3920, Bottom: 1080}, display.Rect{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, shared := regionAcrossSharedEdge(test.a, test.b, 1280, 720)
			if shared != test.shared || (shared && got != test.want) {
				t.Fatalf("got %v, %v; want %v, %v", got, shared, test.want, test.shared)
			}
		})
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

func TestHideKeepsPreviouslyHiddenWindowHidden(t *testing.T) {
	monitor := display.NewMonitor()
	want := color.RGBA{R: 0x77, G: 0x33, B: 0x11, A: 0xFF}
	hwnd, _ := createSolidWindow(t, monitor, 240, 180, want)

	// Hide the window outside the Exclusion, as a second capture session
	// or an already-hidden main window would. Restore must not resurrect
	// a window it did not hide.
	procShowWindow.Call(uintptr(hwnd), swHide)
	exclusion, err := Hide(uintptr(hwnd))
	if err != nil {
		t.Fatalf("Hide: %v", err)
	}
	exclusion.Restore()
	if r, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); r != 0 {
		t.Fatal("Restore showed a window that was already hidden before Hide")
	}
}

// TestRestoreKeepsAMaximizedWindowMaximized covers a capture from a maximized
// main window, which SW_SHOWNOACTIVATE brought back at its normal size, so
// closing the application then saved it as not maximized (SET-002).
func TestRestoreKeepsAMaximizedWindowMaximized(t *testing.T) {
	const (
		gwlStyle   = ^uintptr(15) // -16 as a uintptr
		wsMaximize = 0x01000000
	)
	isZoomed := user32.NewProc("IsZoomed")
	getStyle := user32.NewProc("GetWindowLongPtrW")
	setStyle := user32.NewProc("SetWindowLongPtrW")
	// The window is marked maximized where it is, off every display: really
	// maximizing it would cover the primary display, topmost, while capture
	// tests in other packages read it.
	region := display.Rect{Left: -22000, Top: -22000, Right: -21760, Bottom: -21820}
	hwnd := showSolidWindow(t, region, color.RGBA{R: 0x10, G: 0x60, B: 0xA0, A: 0xFF})
	style, _, _ := getStyle.Call(uintptr(hwnd), gwlStyle)
	setStyle.Call(uintptr(hwnd), gwlStyle, style|wsMaximize)
	if zoomed, _, _ := isZoomed.Call(uintptr(hwnd)); zoomed == 0 {
		t.Fatal("the test window is not marked maximized")
	}

	exclusion, err := Hide(uintptr(hwnd))
	if err != nil {
		t.Fatalf("Hide: %v", err)
	}
	exclusion.Restore()
	if visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); visible == 0 {
		t.Fatal("Restore did not show the window again")
	}
	if zoomed, _, _ := isZoomed.Call(uintptr(hwnd)); zoomed == 0 {
		t.Fatal("Restore brought a maximized window back at its normal size")
	}
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

func TestConvertDIB(t *testing.T) {
	// Two pixels of top-down 32-bit BGRA data: blue then a color with a
	// nonzero source alpha byte, which the capture contract discards.
	source := []byte{
		0x30, 0x20, 0x10, 0x00,
		0xC0, 0xA0, 0x80, 0x7F,
	}
	img := convertDIB(unsafe.Pointer(&source[0]), 2, 1)
	if got := img.Bounds(); got.Dx() != 2 || got.Dy() != 1 {
		t.Fatalf("converted bounds = %v, want 2 × 1", got)
	}
	want := []byte{0x10, 0x20, 0x30, 0xFF, 0x80, 0xA0, 0xC0, 0xFF}
	for i, b := range want {
		if img.Pix[i] != b {
			t.Fatalf("pixel byte %d = %#x, want %#x (full image % x)", i, img.Pix[i], b, img.Pix)
		}
	}
}

// TestKeptHiddenLastsFromHideToRestore pins the record the application's
// window procedure reads to refuse showing a window a capture hides: it starts
// as Hide hides the window and ends as its Exclusion restores it, once however
// often Restore runs, and two exclusions of one window keep it until both have
// restored it.
func TestKeptHiddenLastsFromHideToRestore(t *testing.T) {
	region := display.Rect{Left: -22000, Top: -22000, Right: -21760, Bottom: -21820}
	hwnd := uintptr(showSolidWindow(t, region, color.RGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xFF}))
	if KeptHidden(hwnd) {
		t.Fatal("a window no capture hid counts as kept hidden")
	}
	first, err := Hide(hwnd)
	if err != nil {
		t.Fatal(err)
	}
	if !KeptHidden(hwnd) {
		t.Fatal("Hide did not record the window it hid")
	}
	second, err := Hide(hwnd)
	if err != nil {
		first.Restore()
		t.Fatal(err)
	}
	first.Restore()
	first.Restore()
	if !KeptHidden(hwnd) {
		second.Restore()
		t.Fatal("restoring one exclusion twice released the other's record")
	}
	second.Restore()
	if KeptHidden(hwnd) {
		t.Fatal("the record outlived every exclusion")
	}
}
