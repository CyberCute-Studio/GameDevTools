package display

import (
	"math"
	"testing"
)

func landscape(number int, deviceName string, left, top, right, bottom int32) Display {
	return Display{
		Number:     number,
		DeviceName: deviceName,
		Bounds:     Rect{Left: left, Top: top, Right: right, Bottom: bottom},
		WorkArea:   Rect{Left: left, Top: top, Right: right, Bottom: bottom - 40},
		DPIX:       96,
		DPIY:       96,
	}
}

func TestRectGeometry(t *testing.T) {
	r := Rect{Left: -1920, Top: -100, Right: 0, Bottom: 980}
	if got := r.Width(); got != 1920 {
		t.Fatalf("Width = %d", got)
	}
	if got := r.Height(); got != 1080 {
		t.Fatalf("Height = %d", got)
	}
	if r.Empty() {
		t.Fatal("non-empty rectangle reported empty")
	}
	if !(Rect{Left: 10, Top: 10, Right: 10, Bottom: 20}).Empty() {
		t.Fatal("zero-width rectangle not reported empty")
	}
	if !(Rect{Left: 10, Top: 10, Right: 20, Bottom: 5}).Empty() {
		t.Fatal("inverted rectangle not reported empty")
	}
}

func TestRectContains(t *testing.T) {
	r := Rect{Left: -10, Top: -10, Right: 10, Bottom: 10}
	inside := []Point{{0, 0}, {-10, -10}, {9, 9}, {-1, 5}}
	for _, p := range inside {
		if !r.Contains(p) {
			t.Fatalf("%v should be inside %v", p, r)
		}
	}
	outside := []Point{{10, 0}, {0, 10}, {-11, 0}, {0, -11}, {10, 10}}
	for _, p := range outside {
		if r.Contains(p) {
			t.Fatalf("%v should be outside %v", p, r)
		}
	}
}

func TestVirtualDesktopBounds(t *testing.T) {
	if got := VirtualDesktopBounds(nil); got != (Rect{}) {
		t.Fatalf("empty input bounds = %v", got)
	}
	displays := []Display{
		landscape(1, `\\.\DISPLAY2`, 0, 0, 3440, 1440),
		landscape(2, `\\.\DISPLAY1`, 3440, 623, 5360, 1823),
		landscape(3, `\\.\DISPLAY3`, -1920, -120, 0, 960),
	}
	want := Rect{Left: -1920, Top: -120, Right: 5360, Bottom: 1823}
	if got := VirtualDesktopBounds(displays); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
}

func TestDisplayContaining(t *testing.T) {
	displays := []Display{
		landscape(1, `\\.\DISPLAY1`, 0, 0, 1920, 1080),
		landscape(2, `\\.\DISPLAY2`, -1920, 0, 0, 1080),
	}
	item, ok := DisplayContaining(displays, Point{X: -1, Y: 500})
	if !ok || item.DeviceName != `\\.\DISPLAY2` {
		t.Fatalf("negative-coordinate lookup = %v, %v", item, ok)
	}
	item, ok = DisplayContaining(displays, Point{X: 1919, Y: 1079})
	if !ok || item.DeviceName != `\\.\DISPLAY1` {
		t.Fatalf("bottom-right lookup = %v, %v", item, ok)
	}
	if _, ok = DisplayContaining(displays, Point{X: 1920, Y: 500}); ok {
		t.Fatal("point past the exclusive right edge matched")
	}
	if _, ok = DisplayContaining(displays, Point{X: -1921, Y: 500}); ok {
		t.Fatal("point in the gap left of the secondary display matched")
	}
}

func TestNearestDisplay(t *testing.T) {
	if _, ok := NearestDisplay(nil, Point{}); ok {
		t.Fatal("empty input reported a nearest display")
	}
	displays := []Display{
		landscape(1, `\\.\DISPLAY1`, 0, 0, 1920, 1080),
		landscape(2, `\\.\DISPLAY2`, 1920, 200, 3840, 1280),
	}
	item, ok := NearestDisplay(displays, Point{X: 2000, Y: 100})
	if !ok || item.DeviceName != `\\.\DISPLAY1` {
		t.Fatalf("gap point nearest = %v", item)
	}
	item, _ = NearestDisplay(displays, Point{X: 2000, Y: 500})
	if item.DeviceName != `\\.\DISPLAY2` {
		t.Fatalf("contained point nearest = %v", item)
	}
}

func TestPrimaryDisplay(t *testing.T) {
	if _, ok := PrimaryDisplay(nil); ok {
		t.Fatal("empty input reported a primary display")
	}
	secondary := landscape(2, `\\.\DISPLAY2`, 1920, 0, 3840, 1080)
	primary := landscape(1, `\\.\DISPLAY1`, 0, 0, 1920, 1080)
	primary.Primary = true
	item, ok := PrimaryDisplay([]Display{secondary, primary})
	if !ok || item.DeviceName != `\\.\DISPLAY1` {
		t.Fatalf("primary = %v", item)
	}
	item, ok = PrimaryDisplay([]Display{secondary})
	if !ok || item.DeviceName != `\\.\DISPLAY2` {
		t.Fatalf("fallback primary = %v", item)
	}
}

func TestNumberDisplays(t *testing.T) {
	displays := []Display{
		landscape(0, `\\.\DISPLAY3`, 3840, 0, 5760, 1080),
		landscape(0, `\\.\DISPLAY1`, 1920, 0, 3840, 1080),
		landscape(0, `\\.\DISPLAY2`, 0, 0, 1920, 1080),
	}
	displays[1].Primary = true
	numberDisplays(displays)
	wantOrder := []string{`\\.\DISPLAY1`, `\\.\DISPLAY2`, `\\.\DISPLAY3`}
	for i, deviceName := range wantOrder {
		if displays[i].DeviceName != deviceName {
			t.Fatalf("position %d = %s, want %s", i, displays[i].DeviceName, deviceName)
		}
		if displays[i].Number != i+1 {
			t.Fatalf("position %d number = %d", i, displays[i].Number)
		}
	}
	if !displays[0].Primary {
		t.Fatal("primary display was not sorted first")
	}
}

func TestScalePercent(t *testing.T) {
	tests := []struct {
		dpi  uint32
		want int
	}{
		{96, 100},
		{120, 125},
		{144, 150},
		{168, 175},
		{192, 200},
		{480, 500},
	}
	for _, test := range tests {
		if got := (Display{DPIX: test.dpi}).ScalePercent(); got != test.want {
			t.Fatalf("ScalePercent(%d) = %d, want %d", test.dpi, got, test.want)
		}
	}
}

func TestLogicalPhysicalConversion(t *testing.T) {
	tests := []struct {
		name     string
		logical  int32
		dpi      uint32
		physical int32
	}{
		{"100% identity", 250, 96, 250},
		{"150% scales up", 100, 144, 150},
		{"150% single pixel rounds away from zero", 1, 144, 2},
		{"150% negative single pixel rounds away from zero", -1, 144, -2},
		{"125% rounds to nearest", 50, 120, 63},
		{"200% exact double", -33, 192, -66},
		{"500% maximum supported scaling", 17, 480, 85},
		{"zero dpi falls back to the 96 reference", 42, 0, 42},
		{"extreme value avoids overflow", math.MaxInt32 - 1, 96, math.MaxInt32 - 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := LogicalToPhysical(test.logical, test.dpi); got != test.physical {
				t.Fatalf("LogicalToPhysical(%d, %d) = %d, want %d", test.logical, test.dpi, got, test.physical)
			}
		})
	}
}

func TestLogicalPhysicalRoundTrip(t *testing.T) {
	// Logical-to-physical rounding error stays below half a logical unit for
	// every supported scaling level, so converting back is exact.
	for _, dpi := range []uint32{96, 120, 144, 168, 192, 216, 240, 288, 384, 480} {
		for _, logical := range []int32{-1920, -1, 0, 1, 641, 3840} {
			physical := LogicalToPhysical(logical, dpi)
			if got := PhysicalToLogical(physical, dpi); got != logical {
				t.Fatalf("round trip at %d DPI: %d -> %d -> %d", dpi, logical, physical, got)
			}
		}
	}
	if got := PhysicalToLogical(150, 144); got != 100 {
		t.Fatalf("PhysicalToLogical(150, 144) = %d", got)
	}
	if got := PhysicalToLogical(-2, 144); got != -1 {
		t.Fatalf("PhysicalToLogical(-2, 144) = %d", got)
	}
}

func TestRectConversion(t *testing.T) {
	logical := Rect{Left: -100, Top: 10, Right: 740, Bottom: 490}
	physical := LogicalRectToPhysical(logical, 144)
	want := Rect{Left: -150, Top: 15, Right: 1110, Bottom: 735}
	if physical != want {
		t.Fatalf("LogicalRectToPhysical = %v, want %v", physical, want)
	}
	if got := PhysicalRectToLogical(physical, 144); got != logical {
		t.Fatalf("PhysicalRectToLogical = %v, want %v", got, logical)
	}
}

func TestMonitorSubscriptions(t *testing.T) {
	monitor := NewMonitor()
	var calls []string
	monitor.Subscribe(func() { calls = append(calls, "first") })
	monitor.Subscribe(func() { calls = append(calls, "second") })
	monitor.HandleDisplayChange()
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("callbacks = %v", calls)
	}
	monitor.HandleDisplayChange()
	if len(calls) != 4 {
		t.Fatalf("second notification produced %v", calls)
	}
}

func TestMonitorSubscriptionFailureIsolation(t *testing.T) {
	monitor := NewMonitor()
	calls := 0
	monitor.Subscribe(func() {
		calls++
		monitor.Subscribe(func() { calls += 100 })
	})
	monitor.HandleDisplayChange()
	if calls != 1 {
		t.Fatalf("subscription during notification leaked into the same pass: %d", calls)
	}
	monitor.HandleDisplayChange()
	if calls != 102 {
		t.Fatalf("late subscription not picked up by the next pass: %d", calls)
	}
}
