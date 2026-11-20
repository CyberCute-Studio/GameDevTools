package overlay

import (
	"math"
	"testing"
)

// colorref builds a Windows COLORREF (0x00BBGGRR) from its channels.
func colorref(r, g, b uint32) uint32 {
	return r | g<<8 | b<<16
}

func TestContrastRatioFollowsWCAG(t *testing.T) {
	for _, check := range []struct {
		name string
		a, b uint32
		want float64
	}{
		{"black on white", colorref(0, 0, 0), colorref(255, 255, 255), 21},
		{"a color on itself", colorref(0, 120, 215), colorref(0, 120, 215), 1},
		// WCAG's own example of the lightest gray that passes 4.5:1 on white.
		{"#767676 on white", colorref(0x76, 0x76, 0x76), colorref(255, 255, 255), 4.54},
		{"either order", colorref(255, 255, 255), colorref(0x76, 0x76, 0x76), 4.54},
	} {
		if got := contrastRatio(check.a, check.b); math.Abs(got-check.want) > 0.01 {
			t.Errorf("%s: contrast %.3f, want %.2f", check.name, got, check.want)
		}
	}
}

// ringSource names the pair of Windows colors ringColors takes the ring's
// colors from.
type ringSource int

const (
	fromHighlight ringSource = iota
	fromWindow
	fromFallback
)

// ringThemes are the highlight, highlight-text, window, and window-text
// colors of the default Windows theme and of the four Windows 11 contrast
// themes, as their .theme files in %SystemRoot%\Resources\Ease of Access
// Themes set them; of the Windows 10 High Contrast Black theme; and of a
// custom theme of grays, in which neither pair contrasts enough.
var ringThemes = []struct {
	name                     string
	highlight, highlightText uint32
	window, windowText       uint32
	source                   ringSource
}{
	{"default", colorref(0, 120, 215), colorref(255, 255, 255), colorref(255, 255, 255), colorref(0, 0, 0), fromWindow},
	{"Aquatic (hcblack)", colorref(142, 227, 240), colorref(38, 59, 80), colorref(32, 32, 32), colorref(255, 255, 255), fromWindow},
	{"Desert (hcwhite)", colorref(144, 57, 9), colorref(255, 245, 227), colorref(255, 250, 239), colorref(61, 61, 61), fromWindow},
	{"Dusk (hc1)", colorref(161, 191, 222), colorref(33, 45, 59), colorref(45, 50, 54), colorref(255, 255, 255), fromWindow},
	{"Night sky (hc2)", colorref(214, 180, 253), colorref(43, 43, 43), colorref(0, 0, 0), colorref(255, 255, 255), fromWindow},
	{"Windows 10 High Contrast Black", colorref(26, 235, 255), colorref(0, 0, 0), colorref(0, 0, 0), colorref(255, 255, 255), fromHighlight},
	{"custom grays", colorref(128, 128, 128), colorref(64, 64, 64), colorref(160, 160, 160), colorref(80, 80, 80), fromFallback},
}

// TestRingColorsStandOutOnAnyBackground pins UX-007 for the selection ring
// and its handles, essential boundaries drawn over whatever is on screen:
// for every theme, one of their two colors contrasts at least 3:1 with any
// background, checked over a lattice of colors that includes every gray,
// and they come from the theme's own colors wherever a pair of them
// contrasts enough to guarantee it.
func TestRingColorsStandOutOnAnyBackground(t *testing.T) {
	var backgrounds []uint32
	for r := uint32(0); r <= 255; r += 17 {
		for g := uint32(0); g <= 255; g += 17 {
			for b := uint32(0); b <= 255; b += 17 {
				backgrounds = append(backgrounds, colorref(r, g, b))
			}
		}
	}
	for gray := uint32(0); gray <= 255; gray++ {
		backgrounds = append(backgrounds, colorref(gray, gray, gray))
	}
	for _, theme := range ringThemes {
		core, edge := ringColors(theme.highlight, theme.highlightText, theme.window, theme.windowText)
		var wantCore, wantEdge uint32
		switch theme.source {
		case fromHighlight:
			wantCore, wantEdge = opaquePixel(theme.highlight), opaquePixel(theme.highlightText)
		case fromWindow:
			wantCore, wantEdge = opaquePixel(theme.window), opaquePixel(theme.windowText)
			if relativeLuminance(theme.windowText) > relativeLuminance(theme.window) {
				wantCore, wantEdge = wantEdge, wantCore
			}
		default:
			wantCore, wantEdge = whitePixel, blackPixel
		}
		if core != wantCore || edge != wantEdge {
			t.Errorf("%s: ring core %#08x edge %#08x, want %#08x and %#08x", theme.name, core, edge, wantCore, wantEdge)
		}
		coreColor, edgeColor := colorrefOf(core), colorrefOf(edge)
		for _, background := range backgrounds {
			if best := max(contrastRatio(coreColor, background), contrastRatio(edgeColor, background)); best < 3 {
				t.Errorf("%s: on %06x the ring's better color contrasts only %.2f:1", theme.name, background, best)
				break
			}
		}
	}
}

// colorrefOf converts an opaque BGRA pixel back into a COLORREF.
func colorrefOf(pixel uint32) uint32 {
	return colorref(pixel>>16&0xff, pixel>>8&0xff, pixel&0xff)
}

func TestOpaquePixelReordersChannels(t *testing.T) {
	if got := opaquePixel(colorref(0x12, 0x34, 0x56)); got != 0xff123456 {
		t.Fatalf("opaquePixel = %#08x, want 0xff123456", got)
	}
}
