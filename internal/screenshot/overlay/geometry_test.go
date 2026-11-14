package overlay

import (
	"math"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

func TestDrawRectNormalizesDragCorners(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: -100, Right: 1920, Bottom: 1080}
	tests := []struct {
		name    string
		anchor  display.Point
		current display.Point
		want    display.Rect
	}{
		{
			name:    "drag down and right includes the release pixel",
			anchor:  display.Point{X: 10, Y: 20},
			current: display.Point{X: 810, Y: 620},
			want:    display.Rect{Left: 10, Top: 20, Right: 811, Bottom: 621},
		},
		{
			name:    "drag up and left includes the release pixel",
			anchor:  display.Point{X: 810, Y: 620},
			current: display.Point{X: 10, Y: 20},
			want:    display.Rect{Left: 10, Top: 20, Right: 811, Bottom: 621},
		},
		{
			name:    "horizontal stroke grows to one pixel tall",
			anchor:  display.Point{X: 100, Y: 100},
			current: display.Point{X: 400, Y: 100},
			want:    display.Rect{Left: 100, Top: 100, Right: 401, Bottom: 101},
		},
		{
			name:    "stationary press grows to one by one",
			anchor:  display.Point{X: -5, Y: -5},
			current: display.Point{X: -5, Y: -5},
			want:    display.Rect{Left: -5, Top: -5, Right: -4, Bottom: -4},
		},
		{
			name:    "drag past the desktop edge is clamped",
			anchor:  display.Point{X: 0, Y: 0},
			current: display.Point{X: 99999, Y: -99999},
			want:    display.Rect{Left: 0, Top: -100, Right: 1920, Bottom: 1},
		},
		{
			name:    "one-pixel drag in the far corner still fits",
			anchor:  display.Point{X: 1919, Y: 1079},
			current: display.Point{X: 1919, Y: 1079},
			want:    display.Rect{Left: 1919, Top: 1079, Right: 1920, Bottom: 1080},
		},
		{
			name:    "corner-to-corner drag covers the whole desktop",
			anchor:  display.Point{X: -1920, Y: -100},
			current: display.Point{X: 1919, Y: 1079},
			want:    desktop,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := drawRect(test.anchor, test.current, desktop); got != test.want {
				t.Fatalf("drawRect() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHitZoneClassifiesHandlesInteriorAndOutside(t *testing.T) {
	selection := display.Rect{Left: 100, Top: 100, Right: 300, Bottom: 200}
	const grab = 10
	tests := []struct {
		name string
		p    display.Point
		want zone
	}{
		{"north-west handle", display.Point{X: 100, Y: 100}, zoneNW},
		{"north handle", display.Point{X: 200, Y: 100}, zoneN},
		{"south-east handle", display.Point{X: 300, Y: 200}, zoneSE},
		{"west handle", display.Point{X: 100, Y: 150}, zoneW},
		{"handle reach beyond its visual size", display.Point{X: 104, Y: 196}, zoneSW},
		{"interior", display.Point{X: 200, Y: 150}, zoneInterior},
		{"outside", display.Point{X: 50, Y: 50}, zoneOutside},
		{"just past the south-east handle", display.Point{X: 320, Y: 220}, zoneOutside},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hitZone(selection, test.p, grab); got != test.want {
				t.Fatalf("hitZone() = %d, want %d", got, test.want)
			}
		})
	}

	tiny := display.Rect{Left: 40, Top: 40, Right: 41, Bottom: 41}
	if got := hitZone(tiny, display.Point{X: 40, Y: 40}, grab); got == zoneOutside || got == zoneInterior {
		t.Fatalf("hitZone on a 1 × 1 selection = %d, want a handle so it stays adjustable", got)
	}
	// A selection smaller than the full grab reach keeps an interior strip
	// to drag: the handle squares shrink instead of covering it whole
	// (SCR-012).
	small := display.Rect{Left: 100, Top: 100, Right: 112, Bottom: 112}
	if got := hitZone(small, display.Point{X: 103, Y: 103}, grab); got != zoneInterior {
		t.Fatalf("hitZone inside a small selection = %d, want the movable interior", got)
	}
	if got := hitZone(small, display.Point{X: 100, Y: 100}, grab); got != zoneNW {
		t.Fatalf("hitZone on a small selection's corner = %d, want the north-west handle", got)
	}
	if got := hitZone(display.Rect{}, display.Point{X: 40, Y: 40}, grab); got != zoneOutside {
		t.Fatalf("hitZone without a selection = %d, want outside", got)
	}
}

func TestMoveSelectionPreservesDimensionsAndStaysInside(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: 0, Right: 1920, Bottom: 1080}
	selection := display.Rect{Left: 100, Top: 100, Right: 900, Bottom: 500}

	moved := moveSelection(selection, -2000, 60, desktop)
	want := display.Rect{Left: -1900, Top: 160, Right: -1100, Bottom: 560}
	if moved != want {
		t.Fatalf("moveSelection() = %v, want %v", moved, want)
	}

	clamped := moveSelection(selection, 99999, -99999, desktop)
	if clamped.Right != desktop.Right || clamped.Top != desktop.Top {
		t.Fatalf("moveSelection() = %v, want clamped to the desktop's right and top edges", clamped)
	}
	if clamped.Width() != selection.Width() || clamped.Height() != selection.Height() {
		t.Fatalf("moveSelection() changed dimensions: %v", clamped)
	}

	// A selection as large as the desktop has nowhere to go.
	full := display.Rect{Left: desktop.Left, Top: desktop.Top, Right: desktop.Right, Bottom: desktop.Bottom}
	if got := moveSelection(full, 40, 40, desktop); got != full {
		t.Fatalf("moveSelection(full desktop) = %v, want unchanged %v", got, full)
	}
}

func TestResizeSelectionAnchorsOppositeEdges(t *testing.T) {
	desktop := display.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}
	selection := display.Rect{Left: 100, Top: 100, Right: 300, Bottom: 300}

	tests := []struct {
		name string
		zone zone
		p    display.Point
		want display.Rect
	}{
		{"east edge follows the pointer", zoneE, display.Point{X: 499, Y: 1000}, display.Rect{Left: 100, Top: 100, Right: 500, Bottom: 300}},
		{"north-west corner moves both anchored edges", zoneNW, display.Point{X: 50, Y: 40}, display.Rect{Left: 50, Top: 40, Right: 300, Bottom: 300}},
		{"south edge drags down", zoneS, display.Point{X: 1, Y: 449}, display.Rect{Left: 100, Top: 100, Right: 300, Bottom: 450}},
		{"east edge past the anchor pins one pixel", zoneE, display.Point{X: 50, Y: 150}, display.Rect{Left: 100, Top: 100, Right: 101, Bottom: 300}},
		{"north edge below the anchor pins one pixel", zoneN, display.Point{X: 200, Y: 900}, display.Rect{Left: 100, Top: 299, Right: 300, Bottom: 300}},
		{"pointer clamped into the desktop", zoneSE, display.Point{X: 99999, Y: 99999}, display.Rect{Left: 100, Top: 100, Right: 1920, Bottom: 1080}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resizeSelection(selection, test.zone, test.p, desktop); got != test.want {
				t.Fatalf("resizeSelection() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSizedSelectionAppliesTypedDimensions(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: 0, Right: 1920, Bottom: 1080}
	selection := display.Rect{Left: 100, Top: 100, Right: 300, Bottom: 300}

	got, ok := sizedSelection(selection, 1280, 720, desktop)
	if !ok || got != (display.Rect{Left: 100, Top: 100, Right: 1380, Bottom: 820}) {
		t.Fatalf("sizedSelection() = (%v, %v), want the selection resized at its top-left corner", got, ok)
	}
	if _, ok := sizedSelection(selection, 0, 720, desktop); ok {
		t.Fatal("sizedSelection accepted a zero width")
	}
	if _, ok := sizedSelection(selection, -5, 720, desktop); ok {
		t.Fatal("sizedSelection accepted a negative width")
	}
	if _, ok := sizedSelection(selection, 5000, 720, desktop); ok {
		t.Fatal("sizedSelection accepted a width that reaches past the desktop edge")
	}
	if _, ok := sizedSelection(selection, 200, 2000, desktop); ok {
		t.Fatal("sizedSelection accepted a height that reaches past the desktop edge")
	}
	// ParseDimension accepts values beyond int32; a direct int32 conversion
	// would wrap 2^32 to zero and 2^32+100 to a fitting 100, so those must
	// be rejected before the conversion.
	if _, ok := sizedSelection(selection, 1<<32, 720, desktop); ok {
		t.Fatal("sizedSelection accepted a width that wraps int32 to zero")
	}
	if _, ok := sizedSelection(selection, 1<<32+100, 720, desktop); ok {
		t.Fatal("sizedSelection accepted a width that wraps int32 to a fitting value")
	}
	// ParseDimension accepts values up to int64's maximum; adding one to the
	// selection's origin overflows even int64 and wraps into a bogus
	// rectangle, so the fit check must run before any addition.
	if _, ok := sizedSelection(selection, math.MaxInt64, 720, desktop); ok {
		t.Fatal("sizedSelection accepted a width that overflows int64 addition")
	}
	if _, ok := sizedSelection(selection, 720, math.MaxInt64-50, desktop); ok {
		t.Fatal("sizedSelection accepted a height that overflows int64 addition")
	}
}

func TestToolbarPlacementWithoutSelectionAnchorsToCursorDisplay(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: -200, Right: 3840, Bottom: 1080}
	tests := []struct {
		name   string
		anchor display.Rect
		wantX  int32
		wantY  int32
	}{
		{
			name:   "centered above the cursor display work area",
			anchor: display.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040},
			wantX:  (1920 - 480) / 2,
			wantY:  16,
		},
		{
			name:   "negative-coordinate display",
			anchor: display.Rect{Left: -1920, Top: -200, Right: 0, Bottom: 880},
			wantX:  -1920 + (1920-480)/2,
			wantY:  -184,
		},
		{
			name:   "clamped into the desktop",
			anchor: display.Rect{Left: 3800, Top: -200, Right: 3840, Bottom: -100},
			wantX:  3840 - 480,
			wantY:  -184,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y := toolbarPlacement(desktop, test.anchor, display.Rect{}, 480, 100, 16)
			if x != test.wantX || y != test.wantY {
				t.Fatalf("toolbarPlacement() = (%d, %d), want (%d, %d)", x, y, test.wantX, test.wantY)
			}
		})
	}
}

func TestToolbarPlacementFollowsTheSelection(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: -200, Right: 3840, Bottom: 1080}
	anchor := display.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1040}

	tests := []struct {
		name      string
		selection display.Rect
		wantX     int32
		wantY     int32
	}{
		{
			name:      "centered below the selection",
			selection: display.Rect{Left: 100, Top: 100, Right: 900, Bottom: 400},
			wantX:     100 + (800-480)/2,
			wantY:     416,
		},
		{
			name:      "above the selection when there is no room below",
			selection: display.Rect{Left: 100, Top: 600, Right: 900, Bottom: 1040},
			wantX:     260,
			wantY:     600 - 16 - 100,
		},
		{
			name:      "wide selection clamps into the desktop",
			selection: display.Rect{Left: 3500, Top: 100, Right: 3840, Bottom: 400},
			wantX:     3840 - 480,
			wantY:     416,
		},
		{
			name:      "negative-coordinate selection",
			selection: display.Rect{Left: -1900, Top: -180, Right: -1100, Bottom: 20},
			wantX:     -1900 + (800-480)/2,
			wantY:     36,
		},
		{
			name:      "full-desktop selection keeps the toolbar reachable",
			selection: desktop,
			wantX:     -1920 + (5760-480)/2,
			wantY:     -200,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y := toolbarPlacement(desktop, anchor, test.selection, 480, 100, 16)
			if x != test.wantX || y != test.wantY {
				t.Fatalf("toolbarPlacement() = (%d, %d), want (%d, %d)", x, y, test.wantX, test.wantY)
			}
		})
	}
}
