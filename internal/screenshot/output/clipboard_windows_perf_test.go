//go:build windows

package output

import (
	"testing"
)

// BenchmarkSetClipboardImage4K measures the NFR-012 clipboard half at the
// requirement's exact dimensions: placing a 3840 × 2160 opaque image on the
// Windows clipboard as CF_DIB, including the BGRA conversion. The image is
// generated once outside the timer; each iteration measures only the
// clipboard delivery.
func BenchmarkSetClipboardImage4K(b *testing.B) {
	img := sampleImage(3840, 2160)
	b.SetBytes(int64(len(img.Pix)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := setClipboardImage(0, img); err != nil {
			b.Fatal(err)
		}
	}
}
