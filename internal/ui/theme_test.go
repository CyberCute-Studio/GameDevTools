package ui

import (
	"math"
	"testing"
)

func TestContrastRatioMatchesWCAG(t *testing.T) {
	for _, test := range []struct {
		a, b Color
		want float64
	}{
		{RGB(0, 0, 0), RGB(0xff, 0xff, 0xff), 21},
		{RGB(0xff, 0xff, 0xff), RGB(0xff, 0xff, 0xff), 1},
		// WCAG's own example of the AA threshold for normal text.
		{RGB(0x76, 0x76, 0x76), RGB(0xff, 0xff, 0xff), 4.54},
	} {
		if got := ContrastRatio(test.a, test.b); math.Abs(got-test.want) > 0.01 {
			t.Errorf("ContrastRatio(%06x, %06x) = %.3f, want %.2f", test.a, test.b, got, test.want)
		}
		if ContrastRatio(test.a, test.b) != ContrastRatio(test.b, test.a) {
			t.Errorf("ContrastRatio(%06x, %06x) depends on the order", test.a, test.b)
		}
	}
}

// TestDarkPaletteContrast pins WCAG 2.1 AA for every pair of colors the dark
// theme draws together: 4.5:1 for text (SC 1.4.3), and 3:1 for what tells a
// field or a radio button from the background, for the keyboard focus
// indicator, and for the selected navigation item's marker (SC 1.4.11). A
// disabled control's text is exempt, as WCAG makes it.
func TestDarkPaletteContrast(t *testing.T) {
	p := DarkPalette
	type pair struct {
		name       string
		foreground Color
		background Color
		minimum    float64
	}
	var pairs []pair
	text := func(name string, foreground Color, backgrounds ...Color) {
		for _, background := range backgrounds {
			pairs = append(pairs, pair{name, foreground, background, 4.5})
		}
	}
	graphic := func(name string, foreground Color, backgrounds ...Color) {
		for _, background := range backgrounds {
			pairs = append(pairs, pair{name, foreground, background, 3})
		}
	}
	text("text", p.Text, p.Window, p.Sidebar, p.Field, p.Control, p.ControlHover, p.ControlPressed, p.Selected, p.Hover)
	text("muted text", p.TextMuted, p.Window, p.Sidebar, p.Field, p.Hover, p.Selected)
	text("selected text", p.SelectedText, p.Selected)
	text("accent text", p.AccentText, p.Accent, p.AccentHover, p.AccentPressed)
	text("success", p.Success, p.Window, p.Sidebar)
	text("warning", p.Warning, p.Window, p.Sidebar)
	text("error", p.Error, p.Window, p.Sidebar)
	graphic("field outline", p.FieldBorder, p.Window, p.Sidebar, p.Field)
	graphic("checked radio button", p.Accent, p.Window)
	graphic("focus indicator", p.Focus, p.Window, p.Sidebar, p.Field, p.Control, p.ControlHover, p.Selected, p.Hover)
	graphic("focus indicator on the primary button", p.AccentText, p.Accent, p.AccentHover, p.AccentPressed)
	for _, test := range pairs {
		if got := ContrastRatio(test.foreground, test.background); got < test.minimum {
			t.Errorf("%s %06x on %06x: contrast %.2f, want at least %.1f", test.name, test.foreground, test.background, got, test.minimum)
		}
	}
}

func TestNoticeStyleFollowsSeverity(t *testing.T) {
	for kind, want := range map[NoticeKind]TextStyle{
		NoticeInfo: TextBody, NoticeSuccess: TextSuccess, NoticeWarning: TextWarning, NoticeError: TextError,
	} {
		if got := noticeStyle(kind); got != want {
			t.Errorf("noticeStyle(%d) = %d, want %d", kind, got, want)
		}
	}
	p := DarkPalette
	for style, want := range map[TextStyle]Color{
		TextBody: p.Text, TextMuted: p.TextMuted, TextLabel: p.TextMuted,
		TextSuccess: p.Success, TextWarning: p.Warning, TextError: p.Error,
	} {
		if got := p.textColor(style); got != want {
			t.Errorf("textColor(%d) = %06x, want %06x", style, got, want)
		}
	}
}

func TestColorRefRoundTrips(t *testing.T) {
	color := RGB(0x12, 0x34, 0x56)
	if ref := color.colorRef(); ref != 0x563412 {
		t.Fatalf("colorRef = %#x, want 0x563412", ref)
	}
	if back := colorFromRef(color.colorRef()); back != color {
		t.Fatalf("colorFromRef = %06x, want %06x", back, color)
	}
}

func TestShapeCoverageAntialiasesItsEdge(t *testing.T) {
	box := shape{x: 2, y: 2, width: 10, height: 6, radius: 3}
	if got := box.coverage(7, 5); got != 1 {
		t.Errorf("center coverage = %v, want 1", got)
	}
	if got := box.coverage(20, 20); got != 0 {
		t.Errorf("far coverage = %v, want 0", got)
	}
	// A pixel centered on a straight edge is half covered.
	if got := box.coverage(7, 2); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("edge coverage = %v, want 0.5", got)
	}
	// The rounded corner leaves the rectangle's own corner pixel uncovered.
	if got := box.coverage(2.5, 2.5); got != 0 {
		t.Errorf("corner coverage = %v, want 0", got)
	}
}

func TestCanvasDrawsAnOutlinedBox(t *testing.T) {
	canvas := newCanvas(12, 8)
	background, border, fill := RGB(0, 0, 0), RGB(0xff, 0, 0), RGB(0, 0, 0xff)
	canvas.fill(background)
	canvas.filledBox(shape{width: 12, height: 8, radius: 3}, fill, border, 1)
	at := func(x, y int) Color { return canvas.pixels[y*canvas.width+x] }
	if got := at(6, 4); got != fill {
		t.Errorf("inside = %06x, want the fill %06x", got, fill)
	}
	if got := at(6, 0); got != border {
		t.Errorf("top edge = %06x, want the border %06x", got, border)
	}
	if got := at(0, 0); got != background {
		t.Errorf("corner = %06x, want the background %06x", got, background)
	}
	// A blended corner pixel lies between the background and the border.
	if got := at(1, 1); got == background || got == border {
		t.Errorf("corner edge = %06x, want a blend", got)
	}
	ring := newCanvas(10, 10)
	ring.fill(background)
	ring.strokeShape(shape{width: 10, height: 10, radius: 5}, 2, fill)
	if got := ring.pixels[5*10+5]; got != background {
		t.Errorf("a ring's inside = %06x, want it left as it was", got)
	}
	if got := ring.pixels[5*10]; got == background {
		t.Error("a ring's edge was left unpainted")
	}
}
