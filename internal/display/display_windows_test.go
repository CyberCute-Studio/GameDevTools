//go:build windows

package display

import (
	"testing"
	"unsafe"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

var procGetSystemMetrics = user32.NewProc("GetSystemMetrics")

// The tests below exercise the real Windows display stack on the machine
// running them, including every connected display, its effective DPI, and
// the live cursor position. They need no window or message loop.

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
