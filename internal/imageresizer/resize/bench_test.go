package resize

import (
	"context"
	"image"
	"testing"
)

// BenchmarkResize12MegapixelJPEGFit1024 measures the resize step of the
// NFR-013 reference workload: a 4000 × 3000 4:2:0 YCbCr picture, as the JPEG
// decoder returns it, fit within 1024 × 1024. Decoding and encoding are
// measured separately by later items.
func BenchmarkResize12MegapixelJPEGFit1024(b *testing.B) {
	img := image.NewYCbCr(image.Rect(0, 0, 4000, 3000), image.YCbCrSubsampleRatio420)
	for i := range img.Y {
		img.Y[i] = uint8(i * 7)
	}
	for i := range img.Cb {
		img.Cb[i], img.Cr[i] = uint8(i*3), uint8(i*5)
	}
	benchmarkResize(b, img, fit(1024, 1024))
}

// BenchmarkResizeTranslucentPNGCropToFill measures a translucent 8-bit
// source reduced with Crop to Fill.
func BenchmarkResizeTranslucentPNGCropToFill(b *testing.B) {
	benchmarkResize(b, noise(1, image.Rect(0, 0, 2048, 1536), true), crop(512, 512))
}

// BenchmarkResizeEnlarge4x measures an opaque source enlarged four times.
func BenchmarkResizeEnlarge4x(b *testing.B) {
	benchmarkResize(b, noise(2, image.Rect(0, 0, 512, 512), false), stretch(2048, 2048))
}

func benchmarkResize(b *testing.B, img image.Image, target Target) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Resize(context.Background(), img, target); err != nil {
			b.Fatal(err)
		}
	}
}
