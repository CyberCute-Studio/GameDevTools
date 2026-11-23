package display

import "testing"

func TestRectOverlaps(t *testing.T) {
	base := Rect{Left: 0, Top: 0, Right: 100, Bottom: 100}
	tests := []struct {
		name  string
		other Rect
		want  bool
	}{
		{name: "inside", other: Rect{Left: 10, Top: 10, Right: 20, Bottom: 20}, want: true},
		{name: "around", other: Rect{Left: -10, Top: -10, Right: 200, Bottom: 200}, want: true},
		{name: "one shared pixel", other: Rect{Left: 99, Top: 99, Right: 200, Bottom: 200}, want: true},
		{name: "touches the right edge", other: Rect{Left: 100, Top: 0, Right: 200, Bottom: 100}},
		{name: "touches the bottom edge", other: Rect{Left: 0, Top: 100, Right: 100, Bottom: 200}},
		{name: "touches the left edge", other: Rect{Left: -100, Top: 0, Right: 0, Bottom: 100}},
		{name: "apart", other: Rect{Left: 300, Top: 300, Right: 400, Bottom: 400}},
		{name: "empty inside", other: Rect{Left: 10, Top: 10, Right: 10, Bottom: 20}},
		{name: "inverted", other: Rect{Left: 20, Top: 20, Right: 10, Bottom: 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := base.Overlaps(test.other); got != test.want {
				t.Fatalf("%v.Overlaps(%v) = %v, want %v", base, test.other, got, test.want)
			}
			if got := test.other.Overlaps(base); got != test.want {
				t.Fatalf("%v.Overlaps(%v) = %v, want %v", test.other, base, got, test.want)
			}
		})
	}
}
