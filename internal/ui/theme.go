package ui

import "math"

// Color is an opaque sRGB color, 0xRRGGBB: the layout of a pixel in the
// 32-bit top-down bitmaps the theme draws into, read as a little-endian
// word.
type Color uint32

// RGB returns the color of the three channels.
func RGB(r, g, b uint8) Color {
	return Color(r)<<16 | Color(g)<<8 | Color(b)
}

func (c Color) channels() (r, g, b uint8) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}

// colorRef returns the color as a Win32 COLORREF, 0x00BBGGRR.
func (c Color) colorRef() uintptr {
	r, g, b := c.channels()
	return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16
}

// colorFromRef returns the color of a Win32 COLORREF.
func colorFromRef(ref uintptr) Color {
	return RGB(uint8(ref), uint8(ref>>8), uint8(ref>>16))
}

// blend returns the color a part alpha of the way from c to over.
func (c Color) blend(over Color, alpha float64) Color {
	if alpha <= 0 {
		return c
	}
	if alpha >= 1 {
		return over
	}
	r1, g1, b1 := c.channels()
	r2, g2, b2 := over.channels()
	mix := func(a, b uint8) uint8 {
		return uint8(float64(a) + (float64(b)-float64(a))*alpha + 0.5)
	}
	return RGB(mix(r1, r2), mix(g1, g2), mix(b1, b2))
}

// luminance returns the WCAG 2.1 relative luminance of the color.
func (c Color) luminance() float64 {
	linear := func(channel uint8) float64 {
		value := float64(channel) / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	r, g, b := c.channels()
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// ContrastRatio returns the WCAG 2.1 contrast ratio of two colors, from 1 to
// 21, the same whichever is given first.
func ContrastRatio(a, b Color) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Palette is the set of colors the application draws its windows in.
type Palette struct {
	// Window is the content background, and Sidebar the navigation's, with
	// Divider the line between them.
	Window, Sidebar, Divider Color
	// Field fills text fields, lists, and drop-down lists, and FieldBorder
	// outlines them. The outline is what tells a field from the background,
	// so it contrasts at least 3:1 with both (WCAG 2.1 SC 1.4.11).
	Field, FieldBorder Color
	// Control fills a button, with its pointer and pressed states, and
	// ControlBorder outlines it; a button's text tells it from the
	// background, so its outline need not contrast.
	Control, ControlHover, ControlPressed, ControlBorder Color
	// Selected fills the selected navigation item and the selected rows of
	// a list, with SelectedText their text, and Hover a navigation item
	// under the pointer.
	Selected, SelectedText, Hover Color
	// Text, TextMuted, and TextDisabled are the text colors: content, the
	// secondary text of descriptions and field labels, and an unavailable
	// control's.
	Text, TextMuted, TextDisabled Color
	// Accent fills the primary button and a checked radio button, in the
	// pointer and pressed states, with AccentText the text on it.
	Accent, AccentHover, AccentPressed, AccentText Color
	// AccentDisabled fills an unavailable primary button.
	AccentDisabled Color
	// Focus draws the keyboard focus indicator and the selected navigation
	// item's marker.
	Focus Color
	// Success, Warning, and Error color a notice of that severity, which
	// its text names as well, so color is never the only indication.
	Success, Warning, Error Color
	// HighContrast is whether the palette holds the colors of a Windows
	// contrast theme, which the controls are then left to draw themselves
	// in.
	HighContrast bool
}

// DarkPalette is the application's own palette: a dark theme in the violet
// of its icon. TestDarkPaletteContrast pins the WCAG 2.1 AA contrast of every
// pair of colors drawn together.
var DarkPalette = Palette{
	Window:         RGB(0x18, 0x18, 0x1d),
	Sidebar:        RGB(0x11, 0x11, 0x15),
	Divider:        RGB(0x26, 0x26, 0x2e),
	Field:          RGB(0x1f, 0x1f, 0x26),
	FieldBorder:    RGB(0x6c, 0x6c, 0x7c),
	Control:        RGB(0x2a, 0x2a, 0x33),
	ControlHover:   RGB(0x34, 0x34, 0x3f),
	ControlPressed: RGB(0x22, 0x22, 0x2a),
	ControlBorder:  RGB(0x3c, 0x3c, 0x48),
	Selected:       RGB(0x2b, 0x25, 0x45),
	SelectedText:   RGB(0xee, 0xee, 0xf3),
	Hover:          RGB(0x22, 0x22, 0x2a),
	Text:           RGB(0xee, 0xee, 0xf3),
	TextMuted:      RGB(0xa3, 0xa3, 0xb2),
	TextDisabled:   RGB(0x6a, 0x6a, 0x78),
	Accent:         RGB(0x7c, 0x3a, 0xed),
	AccentHover:    RGB(0x6d, 0x28, 0xd9),
	AccentPressed:  RGB(0x5b, 0x21, 0xb6),
	AccentText:     RGB(0xff, 0xff, 0xff),
	AccentDisabled: RGB(0x2e, 0x29, 0x42),
	Focus:          RGB(0xb3, 0x9d, 0xfb),
	Success:        RGB(0x4a, 0xde, 0x80),
	Warning:        RGB(0xfb, 0xbf, 0x24),
	Error:          RGB(0xf8, 0x8f, 0x8f),
}

// TextStyle is the role of a label's text, which chooses its color.
type TextStyle uint8

const (
	// TextBody is content: values, titles, and the controls' own text.
	TextBody TextStyle = iota
	// TextMuted is secondary text, such as a description or a hint.
	TextMuted
	// TextLabel names the field below it.
	TextLabel
	// TextSuccess, TextWarning, and TextError are notices of that severity.
	TextSuccess
	TextWarning
	TextError
)

// textColor returns the color the palette draws text of the style in.
func (p Palette) textColor(style TextStyle) Color {
	switch style {
	case TextMuted, TextLabel:
		return p.TextMuted
	case TextSuccess:
		return p.Success
	case TextWarning:
		return p.Warning
	case TextError:
		return p.Error
	}
	return p.Text
}

// noticeStyle returns the text style of a notice of the kind.
func noticeStyle(kind NoticeKind) TextStyle {
	switch kind {
	case NoticeSuccess:
		return TextSuccess
	case NoticeWarning:
		return TextWarning
	case NoticeError:
		return TextError
	}
	return TextBody
}

// canvas is a top-down 32-bit pixel buffer, one Color per pixel, that the
// theme draws its shapes into, antialiased, before GDI draws text over them.
// GDI cannot antialias a shape, and a rounded corner or a radio button's
// circle drawn by GDI is visibly stepped.
type canvas struct {
	width, height int
	pixels        []Color
}

func newCanvas(width, height int) *canvas {
	return &canvas{width: width, height: height, pixels: make([]Color, width*height)}
}

// fill paints every pixel.
func (c *canvas) fill(color Color) {
	for i := range c.pixels {
		c.pixels[i] = color
	}
}

// shape is a rectangle with rounded corners, in pixels, which may be
// fractional. A radius of half the smaller side makes a circle or a pill.
type shape struct {
	x, y, width, height, radius float64
}

// inset returns the shape shrunk by amount on every side, its radius by the
// same, so a border drawn as the shape and its inset is the same width all
// the way round.
func (s shape) inset(amount float64) shape {
	return shape{
		x: s.x + amount, y: s.y + amount,
		width: max(0, s.width-2*amount), height: max(0, s.height-2*amount),
		radius: max(0, s.radius-amount),
	}
}

// coverage returns how much of the pixel whose center is (px, py) the shape
// covers, from 0 to 1, from the signed distance of the center to the shape's
// edge: half a pixel inside is fully covered, half a pixel outside not at
// all, which antialiases the edge over one pixel.
func (s shape) coverage(px, py float64) float64 {
	if s.width <= 0 || s.height <= 0 {
		return 0
	}
	radius := min(s.radius, s.width/2, s.height/2)
	halfWidth, halfHeight := s.width/2-radius, s.height/2-radius
	qx := math.Abs(px-(s.x+s.width/2)) - halfWidth
	qy := math.Abs(py-(s.y+s.height/2)) - halfHeight
	outside := math.Hypot(max(qx, 0), max(qy, 0))
	distance := outside + min(max(qx, qy), 0) - radius
	return min(1, max(0, 0.5-distance))
}

// bounds returns the pixel rows and columns the shape can touch, clipped to
// the canvas.
func (c *canvas) bounds(s shape) (x0, y0, x1, y1 int) {
	x0 = max(0, int(math.Floor(s.x)))
	y0 = max(0, int(math.Floor(s.y)))
	x1 = min(c.width, int(math.Ceil(s.x+s.width)))
	y1 = min(c.height, int(math.Ceil(s.y+s.height)))
	return x0, y0, x1, y1
}

// fillShape paints the shape in color over what is there, blending its
// edge.
func (c *canvas) fillShape(s shape, color Color) {
	x0, y0, x1, y1 := c.bounds(s)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if alpha := s.coverage(float64(x)+0.5, float64(y)+0.5); alpha > 0 {
				i := y*c.width + x
				c.pixels[i] = c.pixels[i].blend(color, alpha)
			}
		}
	}
}

// strokeShape paints a ring width wide inside the shape's edge in color over
// what is there, leaving the inside as it was.
func (c *canvas) strokeShape(s shape, width float64, color Color) {
	inner := s.inset(width)
	x0, y0, x1, y1 := c.bounds(s)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			if alpha := s.coverage(px, py) - inner.coverage(px, py); alpha > 0 {
				i := y*c.width + x
				c.pixels[i] = c.pixels[i].blend(color, alpha)
			}
		}
	}
}

// filledBox paints a shape filled in fill and outlined in border, width
// wide, over what is there: the outline as the whole shape, then the fill as
// its inset, so their edges blend.
func (c *canvas) filledBox(s shape, fill, border Color, width float64) {
	if width <= 0 || border == fill {
		c.fillShape(s, fill)
		return
	}
	c.fillShape(s, border)
	c.fillShape(s.inset(width), fill)
}
