package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// PaintProgressBar answers a window's WM_PAINT by drawing a progress bar in
// the rectangle of its client area at x, y, width, and height, filled to
// fraction, from 0 to 1: a track in the text fields' colors, outlined as they
// are, and its filled part in the focus color, which contrasts at least 3:1
// with the track in the dark theme and under a contrast theme alike (WCAG 2.1
// SC 1.4.11). The window's procedure passes its messages to ThemeMessage,
// which erases the rest of the client area. The bar carries no text, so the
// window must say the same progress in words.
func PaintProgressBar(hwnd windows.Handle, x, y, width, height int32, fraction float64) {
	var paint themePaintStruct
	dc, _, _ := procThemeBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procThemeEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
	if width <= 0 || height <= 0 {
		return
	}
	p := CurrentPalette()
	border := max(1, scaleFor(hwnd, 1))
	outer := themeRect{Left: x, Top: y, Right: x + width, Bottom: y + height}
	inner := themeRect{Left: x + border, Top: y + border, Right: x + width - border, Bottom: y + height - border}
	fill := func(area themeRect, color Color) {
		if area.width() > 0 && area.height() > 0 {
			procThemeFillRect.Call(dc, uintptr(unsafe.Pointer(&area)), brush(color))
		}
	}
	fill(outer, p.FieldBorder)
	fill(inner, p.Field)
	fraction = min(max(fraction, 0), 1)
	done := inner
	done.Right = inner.Left + int32(float64(inner.width())*fraction+0.5)
	fill(done, p.Focus)
}
