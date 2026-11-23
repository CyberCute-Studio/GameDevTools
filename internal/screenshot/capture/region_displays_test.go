package capture

import (
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// TestValidateOnDisplaysRefusesARegionOnNoDisplay pins that a rectangle no
// display covers is refused rather than captured as a black image reported as
// a success, as after a display inside an unchanged virtual desktop was
// removed (SCR-054). A rectangle that a display covers in part is still
// captured, with the uncovered part blank.
func TestValidateOnDisplaysRefusesARegionOnNoDisplay(t *testing.T) {
	displays := []display.Display{
		{DeviceName: "A", Bounds: display.Rect{Left: 0, Top: 0, Right: 1920, Bottom: 1080}},
		{DeviceName: "B", Bounds: display.Rect{Left: 1920, Top: 0, Right: 3840, Bottom: 720}},
	}
	tests := []struct {
		name   string
		region display.Rect
		refuse bool
	}{
		{name: "on one display", region: display.Rect{Left: 10, Top: 10, Right: 110, Bottom: 110}},
		{name: "across both displays", region: display.Rect{Left: 1900, Top: 10, Right: 1940, Bottom: 110}},
		{name: "partly on a display", region: display.Rect{Left: 2000, Top: 700, Right: 2100, Bottom: 800}},
		{name: "on no display", region: display.Rect{Left: 2000, Top: 800, Right: 2100, Bottom: 900}, refuse: true},
		{name: "touching a display's edge", region: display.Rect{Left: 1920, Top: 720, Right: 2000, Bottom: 800}, refuse: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateOnDisplays(displays, test.region)
			if !test.refuse {
				if err != nil {
					t.Fatalf("validateOnDisplays(%v) = %v", test.region, err)
				}
				return
			}
			assertKind(t, err, KindDisplay)
		})
	}
}
