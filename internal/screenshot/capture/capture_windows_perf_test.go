//go:build windows

package capture

import (
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

// BenchmarkCaptureRectVirtualDesktop measures the NFR-012 capture half:
// reading one large region of physical pixels off the screen. NFR-012's
// target region is 3840 × 2160; the benchmark captures the whole virtual
// desktop instead so it adapts to the machine it runs on — on the
// verification environment of record the virtual desktop is larger than 4K,
// making the measurement conservative. The MP metric records the captured
// size next to the SetBytes throughput.
func BenchmarkCaptureRectVirtualDesktop(b *testing.B) {
	monitor := display.NewMonitor()
	bounds, err := monitor.VirtualDesktopBounds()
	if err != nil {
		b.Fatal(err)
	}
	pixels := int64(bounds.Width()) * int64(bounds.Height())
	b.SetBytes(pixels * 4)
	b.ResetTimer()
	// ReportMetric comes after ResetTimer, which deletes user-reported
	// metrics, so the captured size survives next to the throughput.
	b.ReportMetric(float64(pixels)/1e6, "MP")
	for i := 0; i < b.N; i++ {
		img, err := CaptureRect(monitor, bounds)
		if err != nil {
			b.Fatal(err)
		}
		if got := img.Bounds(); got.Dx() != int(bounds.Width()) || got.Dy() != int(bounds.Height()) {
			b.Fatalf("captured bounds = %v, want %v", got, bounds)
		}
	}
}
