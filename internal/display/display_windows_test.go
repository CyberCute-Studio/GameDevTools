//go:build windows

package display

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"unsafe"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

var (
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

// TestMain puts the test process into the same per-monitor DPI awareness V2
// context the production shell establishes in internal/app before any
// display call. Without it the test binary is DPI-unaware and Windows
// virtualizes every coordinate these tests observe, so on any display not
// running at 100% scaling they would validate scaled coordinates instead of
// the physical pixels the package contract promises.
func TestMain(m *testing.M) {
	const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)
	r, _, err := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2)
	if r == 0 {
		// Running DPI-unaware would read every display as 100% and could pass,
		// or record that layout for the validation report, on virtualized
		// coordinates.
		fmt.Fprintf(os.Stderr, "TestMain: SetProcessDpiAwarenessContext failed, aborting: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// The tests below exercise the real Windows display stack on the machine
// running them, including every connected display, its effective DPI, and
// the live cursor position. They need no window or message loop.

// validationDisplaysEnv names the file TestRecordDisplayLayout writes for
// scripts/validate.ps1.
const validationDisplaysEnv = "GAMEDEVTOOLS_VALIDATION_DISPLAYS"

// TestRecordDisplayLayout writes the connected displays, as this
// per-monitor-aware process reads them, for the validation report. A process
// that is not DPI aware would read every display as 100%. Friendly names are
// left out of the report, since they can identify a person's hardware.
func TestRecordDisplayLayout(t *testing.T) {
	path := os.Getenv(validationDisplaysEnv)
	if path == "" {
		t.Skipf("%s is not set", validationDisplaysEnv)
	}
	displays, err := NewMonitor().Displays()
	if err != nil {
		t.Fatal(err)
	}
	type record struct {
		Number       int   `json:"number"`
		Primary      bool  `json:"primary"`
		X            int32 `json:"x"`
		Y            int32 `json:"y"`
		Width        int32 `json:"width"`
		Height       int32 `json:"height"`
		ScalePercent int   `json:"scalePercent"`
	}
	records := make([]record, 0, len(displays))
	for _, item := range displays {
		records = append(records, record{
			Number: item.Number, Primary: item.Primary,
			X: item.Bounds.Left, Y: item.Bounds.Top,
			Width: item.Bounds.Width(), Height: item.Bounds.Height(),
			ScalePercent: item.ScalePercent(),
		})
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnumerateRealDisplays(t *testing.T) {
	monitor := NewMonitor()
	displays, err := monitor.Displays()
	if err != nil {
		t.Fatal(err)
	}
	if len(displays) == 0 {
		t.Fatal("no displays enumerated")
	}
	primaryCount := 0
	seenNumbers := map[int]bool{}
	seenDevices := map[string]bool{}
	for _, item := range displays {
		if item.Number < 1 || item.Number > len(displays) || seenNumbers[item.Number] {
			t.Fatalf("invalid or duplicate display number %d", item.Number)
		}
		seenNumbers[item.Number] = true
		if item.DeviceName == "" || seenDevices[item.DeviceName] {
			t.Fatalf("invalid or duplicate device name %q", item.DeviceName)
		}
		seenDevices[item.DeviceName] = true
		if item.Bounds.Empty() {
			t.Fatalf("%s has empty bounds %v", item.DeviceName, item.Bounds)
		}
		if item.WorkArea.Empty() {
			t.Fatalf("%s has empty work area %v", item.DeviceName, item.WorkArea)
		}
		if item.DPIX < ReferenceDPI || item.DPIY < ReferenceDPI {
			t.Fatalf("%s reports unsupported effective DPI %d x %d", item.DeviceName, item.DPIX, item.DPIY)
		}
		if item.Primary {
			primaryCount++
			if item.Number != 1 {
				t.Fatalf("primary display %s numbered %d, want 1", item.DeviceName, item.Number)
			}
		}
		t.Logf("display %d: %s %q %v primary=%v dpi=%d x %d scale=%d%%",
			item.Number, item.DeviceName, item.Name, item.Bounds, item.Primary, item.DPIX, item.DPIY, item.ScalePercent())
	}
	if primaryCount != 1 {
		t.Fatalf("primary display count = %d, want 1", primaryCount)
	}
}

func TestVirtualDesktopBoundsMatchSystemMetrics(t *testing.T) {
	monitor := NewMonitor()
	bounds, err := monitor.VirtualDesktopBounds()
	if err != nil {
		t.Fatal(err)
	}
	metric := func(index int32) int32 {
		value, _, _ := procGetSystemMetrics.Call(uintptr(index))
		return int32(value)
	}
	want := Rect{
		Left:   metric(smXVirtualScreen),
		Top:    metric(smYVirtualScreen),
		Right:  metric(smXVirtualScreen) + metric(smCXVirtualScreen),
		Bottom: metric(smYVirtualScreen) + metric(smCYVirtualScreen),
	}
	if bounds != want {
		t.Fatalf("virtual desktop bounds = %v, GetSystemMetrics reports %v", bounds, want)
	}
}

func TestPrimaryDisplayMatchesSystemMetrics(t *testing.T) {
	monitor := NewMonitor()
	primary, err := monitor.PrimaryDisplay()
	if err != nil {
		t.Fatal(err)
	}
	if primary.Bounds.Left != 0 || primary.Bounds.Top != 0 {
		t.Fatalf("primary display origin = (%d, %d), want (0, 0)", primary.Bounds.Left, primary.Bounds.Top)
	}
}

func TestCursorDisplay(t *testing.T) {
	monitor := NewMonitor()
	position, err := cursorPosition()
	if err != nil {
		t.Fatal(err)
	}
	item, err := monitor.CursorDisplay()
	if err != nil {
		t.Fatal(err)
	}
	displays, err := monitor.Displays()
	if err != nil {
		t.Fatal(err)
	}
	known := false
	for _, candidate := range displays {
		if candidate.DeviceName == item.DeviceName && candidate.Number == item.Number {
			known = true
		}
	}
	if !known {
		t.Fatalf("cursor display %s not in the enumerated topology", item.DeviceName)
	}
	if containing, ok := DisplayContaining(displays, position); ok && containing.DeviceName != item.DeviceName {
		t.Fatalf("cursor at %v is on %s, CursorDisplay returned %s", position, containing.DeviceName, item.DeviceName)
	}
	t.Logf("cursor at %v resolved to display %d (%s)", position, item.Number, item.DeviceName)
}

func TestMonitorDisplayChangeCallback(t *testing.T) {
	monitor := NewMonitor()
	calls := 0
	monitor.Subscribe(func() { calls++ })
	monitor.HandleDisplayChange()
	if calls != 1 {
		t.Fatalf("callback invocations = %d, want 1", calls)
	}
}

func TestRepeatedEnumerationStaysWithinCallbackLimit(t *testing.T) {
	// The Go runtime permits 2000 distinct windows.NewCallback functions
	// per process and never releases them. Enumeration must reuse one
	// registered callback; a per-call closure would crash the process with
	// "too many callback functions" once this loop passes the limit.
	for i := 0; i < 2100; i++ {
		if _, err := rawDisplays(); err != nil {
			t.Fatalf("enumeration %d: %v", i, err)
		}
	}
}

func TestRawDisplayFriendlyNames(t *testing.T) {
	displays, err := rawDisplays()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range displays {
		if item.Number != 0 {
			t.Fatalf("raw enumeration assigned picker number %d", item.Number)
		}
		// The friendly name is legitimately empty on some drivers and
		// remote sessions; SCR-041 only requires it where Windows provides it.
		t.Logf("%s friendly name %q", item.DeviceName, item.Name)
	}
}

func TestDisplayDeviceStructSize(t *testing.T) {
	// EnumDisplayDevicesW writes by cb size; a mismatched layout would read
	// garbage names or fail outright.
	if got, want := unsafe.Sizeof(displayDevice{}), uintptr(4+2*deviceNameLength+3*2*deviceStringLength+4); got != want {
		t.Fatalf("displayDevice size = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(monitorInfoEx{}), uintptr(4+4*4+4*4+4+2*deviceNameLength); got != want {
		t.Fatalf("monitorInfoEx size = %d, want %d", got, want)
	}
}
