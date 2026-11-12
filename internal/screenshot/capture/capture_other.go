//go:build !windows

package capture

import (
	"errors"
	"image"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// Screen capture is a Windows-only capability; the production application
// only runs on Windows.
func captureRegion(region display.Rect) (*image.RGBA, error) {
	return nil, errors.New("screen capture is available on Windows only")
}

// Hide is unavailable off Windows; the production application only runs on
// Windows.
func Hide(hwnd ...uintptr) (*Exclusion, error) {
	return nil, errors.New("screen capture window exclusion is available on Windows only")
}

// Restore is a no-op off Windows, where Hide never succeeds.
func (e *Exclusion) Restore() {}
