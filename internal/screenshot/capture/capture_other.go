//go:build !windows

package capture

import (
	"errors"
	"image"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// Screen capture is a Windows-only capability; the production application
// only runs on Windows. The error is typed KindNative so callers classify
// it the same way as a native Windows capture failure.
func captureRegion(region display.Rect) (*image.RGBA, error) {
	return nil, kindError(KindNative, "capture",
		errors.New("screen capture is available on Windows only"))
}

// Hide is unavailable off Windows; the production application only runs on
// Windows.
func Hide(hwnd ...uintptr) (*Exclusion, error) {
	return nil, kindError(KindNative, "hide windows",
		errors.New("screen capture window exclusion is available on Windows only"))
}

// Restore is a no-op off Windows, where Hide never succeeds.
func (e *Exclusion) Restore() {}
