package decode

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// TestJPEGDecodeBytesFollowsTheDecoderLayout pins the estimate against the
// standard library decoder's allocations: one byte per plane sample padded
// to whole minimum coded units, four more per sample for a progressive
// frame's coefficients, and 4 bytes per pixel for an RGB or CMYK conversion
// and for the upright copy of a reoriented source.
func TestJPEGDecodeBytesFollowsTheDecoderLayout(t *testing.T) {
	config := func(model color.Model) image.Config {
		return image.Config{ColorModel: model, Width: 1000, Height: 1000}
	}
	gray := jpegFrame{valid: true, mcuWidth: 8, mcuHeight: 8, samples: 1}
	full := jpegFrame{valid: true, mcuWidth: 8, mcuHeight: 8, samples: 3}
	subsampled := jpegFrame{valid: true, mcuWidth: 16, mcuHeight: 16, samples: 6}
	progressive := func(frame jpegFrame) jpegFrame {
		frame.progressive = true
		return frame
	}
	const pixels = 1000 * 1000
	// 1000 pixels need 63 units of 16, padded to 1008.
	const subsampledPlanes = 63 * 63 * 64 * 6
	cases := []struct {
		name        string
		config      image.Config
		frame       jpegFrame
		orientation Orientation
		want        uint64
	}{
		{"baseline gray", config(color.GrayModel), gray, OrientationNormal, pixels},
		{"baseline 4:2:0", config(color.YCbCrModel), subsampled, OrientationNormal, subsampledPlanes},
		{"progressive 4:2:0", config(color.YCbCrModel), progressive(subsampled), OrientationNormal, 5 * subsampledPlanes},
		{"progressive 4:4:4", config(color.YCbCrModel), progressive(full), OrientationNormal, 5 * 3 * pixels},
		{"RGB conversion", config(color.RGBAModel), full, OrientationNormal, 3*pixels + 4*pixels},
		{"progressive CMYK", config(color.CMYKModel), jpegFrame{valid: true, progressive: true, mcuWidth: 8, mcuHeight: 8, samples: 4}, OrientationNormal, 5*4*pixels + 4*pixels},
		{"rotated", config(color.GrayModel), gray, OrientationRotate90, pixels + 4*pixels},
		{"mirrored", config(color.GrayModel), gray, OrientationMirrorHorizontal, pixels + 4*pixels},
		// A frame the scan missed is costed as progressive at full
		// resolution.
		{"unscanned CMYK frame", config(color.CMYKModel), jpegFrame{}, OrientationNormal, 5*4*pixels + 4*pixels},
	}
	for _, tc := range cases {
		if got := jpegDecodeBytes(tc.config, tc.frame, tc.orientation); got != tc.want {
			t.Fatalf("%s: jpegDecodeBytes = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestPNGDecodeBytes(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		deep          bool
		interlaced    bool
		want          uint64
	}{
		{"8-bit at the limit", 16384, 16384, false, false, MaxDecodedBytes},
		{"16-bit", 16384, 16384, true, false, 2 * MaxDecodedBytes},
		{"interlaced", 16384, 16384, false, true, 2 * MaxDecodedBytes},
		{"saturates", math.MaxInt32, math.MaxInt32, true, true, math.MaxUint64},
	}
	for _, tc := range cases {
		if got := pngDecodeBytes(tc.width, tc.height, tc.deep, tc.interlaced); got != tc.want {
			t.Fatalf("%s: pngDecodeBytes = %d, want %d", tc.name, got, tc.want)
		}
	}
	// A PNG whose IHDR the scan could not read is costed as interlaced.
	config := image.Config{ColorModel: color.NRGBAModel, Width: 100, Height: 100}
	if got := decodeBytes(FormatPNG, config, header{}); got != 2*4*100*100 {
		t.Fatalf("decodeBytes without a scanned IHDR = %d", got)
	}
	if got := decodeBytes(FormatPNG, config, header{png: pngLayout{valid: true}}); got != 4*100*100 {
		t.Fatalf("decodeBytes for a non-interlaced IHDR = %d", got)
	}
}
