//go:build windows

package overlay

import (
	"runtime"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// benchmarkOverlayAppear measures NFR-011: the time from starting a
// rectangle capture session to the selection surface and toolbar being on
// screen. Begin is synchronous — it creates the native windows, renders the
// initial surface, and shows both before returning — so its duration is the
// overlay-appearance time, excluding only the main-window hide, which the
// fake below replaces with a no-op. The goroutine is locked to its OS
// thread because window creation and destruction must share one thread.
func benchmarkOverlayAppear(b *testing.B, mode Mode, size display.Point) {
	runtime.LockOSThread()
	b.Cleanup(runtime.UnlockOSThread)
	controller := NewController(display.NewMonitor())
	controller.Hide = func(...uintptr) (func(), error) { return func() {}, nil }
	b.Cleanup(controller.Shutdown)
	if mode == ModePresetRectangle {
		bounds, err := display.NewMonitor().VirtualDesktopBounds()
		if err != nil {
			b.Fatal(err)
		}
		if size.X > bounds.Width() || size.Y > bounds.Height() {
			b.Skipf("virtual desktop %v is too small for a %d × %d frame", bounds, size.X, size.Y)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StartTimer()
		err := controller.Begin(0, 0, mode, size, Events{})
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		controller.Cancel()
	}
}

// BenchmarkOverlayAppearFreeRectangle times the full-virtual-desktop tint
// overlay, the larger of the two surfaces.
func BenchmarkOverlayAppearFreeRectangle(b *testing.B) {
	benchmarkOverlayAppear(b, ModeFreeRectangle, display.Point{})
}

// BenchmarkOverlayAppearPresetRectangle times AC-S01's 1280 × 720 exact
// preset frame.
func BenchmarkOverlayAppearPresetRectangle(b *testing.B) {
	benchmarkOverlayAppear(b, ModePresetRectangle, display.Point{X: 1280, Y: 720})
}
