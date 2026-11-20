package overlay

import "math"

// Opaque premultiplied BGRA pixels for the ring's fallback colors.
const (
	blackPixel = uint32(0xff000000)
	whitePixel = uint32(0xffffffff)
)

// twoToneContrast is the contrast two colors need between them for one of
// them to contrast at least 3:1 with any background, the WCAG 2.1 target for
// the essential boundary the selection ring is (UX-007): against a
// background between them, the better of the two contrasts is at worst the
// square root of their own, so 9:1 guarantees 3:1, as WCAG technique C40,
// "Creating a two-color focus indicator", explains.
const twoToneContrast = 9

// ringColors chooses the colors of the selection ring and its handles from
// the Windows colors, given as COLORREFs (0x00BBGGRR). The ring is drawn
// over whatever is on screen, so no single color can stand out from every
// background, and it is drawn in two — a core with a one-pixel edge of the
// other color on each side, as each handle is a core-colored square edged
// in the other. The first pair that contrasts at least twoToneContrast is
// used, so the ring follows the theme the user chose:
//
//   - the highlight as the core, edged in the highlight text, as in the
//     Windows 10 high-contrast themes;
//   - otherwise the window text and window background, the lighter as the
//     core — black and white in the default theme, whose blue highlight and
//     white highlight text contrast only about 4.5:1, and each Windows 11
//     contrast theme's own text and background, as none of their highlight
//     pairs reaches 9:1;
//   - otherwise, for a custom theme that has neither, white edged in black.
//
// Both pixels are opaque premultiplied BGRA.
func ringColors(highlight, highlightText, window, windowText uint32) (core, edge uint32) {
	if contrastRatio(highlight, highlightText) >= twoToneContrast {
		return opaquePixel(highlight), opaquePixel(highlightText)
	}
	if contrastRatio(window, windowText) >= twoToneContrast {
		lighter, darker := window, windowText
		if relativeLuminance(windowText) > relativeLuminance(window) {
			lighter, darker = windowText, window
		}
		return opaquePixel(lighter), opaquePixel(darker)
	}
	return whitePixel, blackPixel
}

// opaquePixel converts a COLORREF into an opaque premultiplied BGRA pixel.
func opaquePixel(color uint32) uint32 {
	r := color & 0xff
	g := (color >> 8) & 0xff
	b := (color >> 16) & 0xff
	return 0xff000000 | r<<16 | g<<8 | b
}

// contrastRatio returns the WCAG 2.1 contrast ratio between two COLORREFs,
// from 1 for equal colors to 21 for black and white.
func contrastRatio(a, b uint32) float64 {
	lighter, darker := relativeLuminance(a), relativeLuminance(b)
	if darker > lighter {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

// relativeLuminance returns a COLORREF's WCAG 2.1 relative luminance: the
// sRGB channels linearized and weighted for the eye's sensitivity.
func relativeLuminance(color uint32) float64 {
	channel := func(value uint32) float64 {
		c := float64(value&0xff) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(color) + 0.7152*channel(color>>8) + 0.0722*channel(color>>16)
}
