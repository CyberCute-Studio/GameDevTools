package decode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"testing"
)

// TestJPEGDecodeBytesFollowsTheDecoderLayout pins the estimate against the
// standard library decoder's allocations: one byte per plane sample padded
// to whole minimum coded units, four more per sample for a progressive
// frame's coefficients, and 4 bytes per pixel for an RGB or CMYK conversion
// and for the upright copy of a reoriented color source, but one for that of
// a grayscale source.
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
		name   string
		config image.Config
		frame  jpegFrame
		want   uint64
	}{
		{"baseline gray", config(color.GrayModel), gray, pixels},
		{"baseline 4:2:0", config(color.YCbCrModel), subsampled, subsampledPlanes},
		{"progressive 4:2:0", config(color.YCbCrModel), progressive(subsampled), 5 * subsampledPlanes},
		{"progressive 4:4:4", config(color.YCbCrModel), progressive(full), 5 * 3 * pixels},
		{"RGB conversion", config(color.RGBAModel), full, 3*pixels + 4*pixels},
		{"progressive CMYK", config(color.CMYKModel), jpegFrame{valid: true, progressive: true, mcuWidth: 8, mcuHeight: 8, samples: 4}, 5*4*pixels + 4*pixels},
		// A frame the scan missed is costed as progressive at full
		// resolution.
		{"unscanned CMYK frame", config(color.CMYKModel), jpegFrame{}, 5*4*pixels + 4*pixels},
	}
	for _, tc := range cases {
		if got := jpegDecodeBytes(tc.config, tc.frame); got != tc.want {
			t.Fatalf("%s: jpegDecodeBytes = %d, want %d", tc.name, got, tc.want)
		}
	}
	// A grayscale source stays grayscale when reoriented; any other becomes
	// RGBA.
	for _, orientation := range []Orientation{OrientationRotate90, OrientationMirrorHorizontal} {
		for model, want := range map[color.Model]uint64{color.GrayModel: pixels, color.YCbCrModel: 4 * pixels, color.CMYKModel: 4 * pixels} {
			if got := uprightCopyBytes(config(model), orientation); got != want {
				t.Fatalf("upright copy of %v for orientation %d = %d, want %d", model, orientation, got, want)
			}
		}
	}
	if got := uprightCopyBytes(config(color.YCbCrModel), OrientationNormal); got != 0 {
		t.Fatalf("upright copy for an upright source = %d, want 0", got)
	}
}

// TestPNGDecodeBytesFollowsTheDecoderPixelTypes pins the estimate against the
// image type the PNG decoder allocates for each color type: a palette or gray
// of up to 8 bits decodes to 1 byte per pixel, 16-bit gray to 2, and every
// other layout — including gray that a tRNS chunk makes transparent — to 4,
// or 8 at 16 bits per channel; an interlaced source counts its pass images
// too.
func TestPNGDecodeBytesFollowsTheDecoderPixelTypes(t *testing.T) {
	layout := func(depth, colorType byte, transparent, interlaced bool) pngLayout {
		return pngLayout{valid: true, width: 16384, height: 16384, depth: depth, colorType: colorType, transparent: transparent, interlaced: interlaced}
	}
	const pixels = 16384 * 16384
	cases := []struct {
		name   string
		layout pngLayout
		want   uint64
	}{
		{"8-bit truecolor alpha at the limit", layout(8, 6, false, false), MaxDecodedBytes},
		{"8-bit truecolor", layout(8, 2, false, false), 4 * pixels},
		{"8-bit gray with alpha", layout(8, 4, false, false), 4 * pixels},
		{"16-bit truecolor alpha", layout(16, 6, false, false), 8 * pixels},
		{"16-bit truecolor", layout(16, 2, true, false), 8 * pixels},
		{"interlaced", layout(8, 6, false, true), 2 * MaxDecodedBytes},
		{"8-bit gray", layout(8, 0, false, false), pixels},
		{"1-bit gray", layout(1, 0, false, false), pixels},
		{"16-bit gray", layout(16, 0, false, false), 2 * pixels},
		{"transparent gray", layout(8, 0, true, false), 4 * pixels},
		{"transparent 16-bit gray", layout(16, 0, true, false), 8 * pixels},
		{"palette", layout(8, 3, true, false), pixels},
		{"interlaced palette", layout(4, 3, false, true), 2 * pixels},
		{"saturates", pngLayout{valid: true, width: math.MaxInt32, height: math.MaxInt32, depth: 16, colorType: 6, interlaced: true}, math.MaxUint64},
	}
	for _, tc := range cases {
		if got := pngDecodeBytes(tc.layout); got != tc.want {
			t.Fatalf("%s: pngDecodeBytes = %d, want %d", tc.name, got, tc.want)
		}
	}
	// A PNG whose IHDR the scan could not read is costed at the costliest
	// layout: interlaced, 16 bits per channel, with alpha.
	config := image.Config{ColorModel: color.GrayModel, Width: 100, Height: 100}
	if got := decodeBytes(FormatPNG, config, header{}); got != 2*8*100*100 {
		t.Fatalf("decodeBytes without a scanned IHDR = %d", got)
	}
	scanned := header{png: pngLayout{valid: true, width: 100, height: 100, depth: 8, colorType: 0}}
	if got := decodeBytes(FormatPNG, config, scanned); got != 100*100 {
		t.Fatalf("decodeBytes for a scanned 8-bit gray IHDR = %d", got)
	}
}

// pngChunk frames chunk data with its length and checksum.
func pngChunk(kind string, data []byte) []byte {
	body := append([]byte(kind), data...)
	var buffer bytes.Buffer
	binary.Write(&buffer, binary.BigEndian, uint32(len(data)))
	buffer.Write(body)
	binary.Write(&buffer, binary.BigEndian, crc32.ChecksumIEEE(body))
	return buffer.Bytes()
}

// TestScanPNGFindsTransparencyBeforeTheImageData pins the scan the PNG
// estimate relies on: it reads the IHDR layout wherever the decoder finds it,
// notes a tRNS chunk before the first IDAT, and assumes one when it ends
// before reaching the image data.
func TestScanPNGFindsTransparencyBeforeTheImageData(t *testing.T) {
	ihdr := pngHeaderInterlaced(300, 200, 8, 0, true)
	text := pngChunk("tEXt", []byte("Comment\x00a note"))
	transparency := pngChunk("tRNS", []byte{0, 7})
	data := pngChunk("IDAT", []byte{1, 2, 3})
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	cases := []struct {
		name    string
		content []byte
		want    pngLayout
	}{
		{"opaque", join(ihdr, text, data, transparency), pngLayout{valid: true, width: 300, height: 200, depth: 8, colorType: 0, interlaced: true}},
		{"transparent", join(ihdr, text, transparency, data), pngLayout{valid: true, width: 300, height: 200, depth: 8, colorType: 0, interlaced: true, transparent: true}},
		{"late header", join(pngSignature, text, ihdr[len(pngSignature):], data), pngLayout{valid: true, width: 300, height: 200, depth: 8, colorType: 0, interlaced: true}},
		{"no image data", join(ihdr, text), pngLayout{valid: true, width: 300, height: 200, depth: 8, colorType: 0, interlaced: true, transparent: true}},
		{"no header", join(pngSignature, text, data), pngLayout{}},
		{"zero width", join(pngHeader(0, 200, 8, 0), data), pngLayout{}},
	}
	for _, tc := range cases {
		if got := scanPNG(bufio.NewReader(bytes.NewReader(tc.content))); got != tc.want {
			t.Fatalf("%s: scanPNG = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// jpegSegment frames a marker segment's payload with its length.
func jpegSegment(marker byte, payload []byte) []byte {
	segment := []byte{0xff, marker, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))
	return append(segment, payload...)
}

// beforeEOI inserts marker segments just before a JPEG's end-of-image
// marker, after its last scan.
func beforeEOI(jpegData []byte, segments ...[]byte) []byte {
	result := append([]byte(nil), jpegData[:len(jpegData)-2]...)
	for _, segment := range segments {
		result = append(result, segment...)
	}
	return append(result, jpegData[len(jpegData)-2:]...)
}

// withComponentIDs rewrites the three component identifiers of a baseline
// JPEG's frame header and of the scan header that selects them.
func withComponentIDs(t *testing.T, jpegData []byte, ids string) []byte {
	t.Helper()
	content := append([]byte(nil), jpegData...)
	frame := bytes.Index(content, []byte{0xff, markerSOF0})
	scan := bytes.Index(content, []byte{0xff, markerSOS})
	if frame < 0 || content[frame+9] != 3 || scan < 0 || content[scan+4] != 3 {
		t.Fatal("three-component SOF0 frame and scan not found")
	}
	for component := 0; component < 3; component++ {
		// Frame: marker (2), length (2), precision (1), height (2), width
		// (2), count (1), then identifier, sampling, and table per
		// component. Scan: marker (2), length (2), count (1), then
		// selector and tables per component.
		content[frame+10+3*component] = ids[component]
		content[scan+5+2*component] = ids[component]
	}
	return content
}

// TestJPEGConvertsToRGBFollowsTheDecoder pins the walk against the standard
// library decoder itself: whatever the JFIF and Adobe segments before or
// after the scan and the component identifiers say, the walk predicts the
// RGBA conversion exactly when the decoder makes it.
func TestJPEGConvertsToRGBFollowsTheDecoder(t *testing.T) {
	source := encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	jfif := jpegSegment(markerAPP0, []byte("JFIF\x00\x01\x02\x00\x00\x01\x00\x01\x00\x00"))
	otherAPP0 := jpegSegment(markerAPP0, []byte("AVI1\x00\x00\x00\x00"))
	adobe := func(transform byte) []byte {
		return jpegSegment(markerAPP14, []byte{'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, transform})
	}
	cases := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"YCbCr", source, false},
		{"Adobe RGB before the scan", withSegments(source, adobe(0)), true},
		{"Adobe RGB after the scan", beforeEOI(source, adobe(0)), true},
		{"Adobe YCbCr after the scan", beforeEOI(withSegments(source, adobe(0)), adobe(1)), false},
		{"JFIF", withSegments(source, jfif, adobe(0)), false},
		{"JFIF cleared after the scan", beforeEOI(withSegments(source, jfif), otherAPP0, adobe(0)), true},
		// DecodeConfig stops at the frame header of a file that opened as
		// JFIF, before these segments.
		{"JFIF cleared after the frame header", beforeScan(t, withSegments(source, jfif), bytes.Join([][]byte{otherAPP0, adobe(0)}, nil)), true},
		{"JFIF after the scan", beforeEOI(withSegments(source, adobe(0)), jfif), false},
		{"RGB components", withComponentIDs(t, source, "RGB"), true},
		{"RGB components under JFIF", withSegments(withComponentIDs(t, source, "RGB"), jfif), false},
	}
	for _, tc := range cases {
		decoded, err := jpeg.Decode(bytes.NewReader(tc.content))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, converted := decoded.(*image.RGBA); converted != tc.want {
			t.Fatalf("%s: the decoder returned %T, but the case expects conversion %v", tc.name, decoded, tc.want)
		}
		if got, err := jpegConvertsToRGB(bufio.NewReader(bytes.NewReader(tc.content))); got != tc.want || err != nil {
			t.Fatalf("%s: jpegConvertsToRGB = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	// A walk that cannot reach the end-of-image marker reports why, as the
	// decoder fails on the same data.
	truncatedSource := source[:len(source)-2]
	if _, err := jpeg.Decode(bytes.NewReader(truncatedSource)); err == nil {
		t.Fatal("the decoder accepted a JPEG without its end-of-image marker")
	}
	if _, err := jpegConvertsToRGB(bufio.NewReader(bytes.NewReader(truncatedSource))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("jpegConvertsToRGB without the end of the image = %v, want io.ErrUnexpectedEOF", err)
	}
	shortSegment := withSegments(source, []byte{0xff, markerAPP14, 0, 1})
	if _, err := jpegConvertsToRGB(bufio.NewReader(bytes.NewReader(shortSegment))); !errors.Is(err, errShortSegment) {
		t.Fatalf("jpegConvertsToRGB with a short segment length = %v, want errShortSegment", err)
	}
}

// TestTruncatedJPEGInTheConversionBandIsDamaged pins that a source whose
// planes fit the limit but whose RGB conversion would not, and whose data
// ends before the end-of-image marker, is reported as damaged — as the
// decoder would report it — rather than as too large.
func TestTruncatedJPEGInTheConversionBandIsDamaged(t *testing.T) {
	// 4:2:0 planes of 20000 × 20000 pixels take 600 MB; the conversion
	// would add 1.6 GB.
	source := jpegWithDimensions(t, encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 16, 16))), 20000, 20000)
	_, err := Inspect(context.Background(), writeFile(t, "truncated.jpg", source[:len(source)-2]))
	requireKind(t, err, KindCorrupt, FormatJPEG)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
	}
}

// TestLateRGBConversionCountsTowardTheLimit pins that an Adobe segment after
// the first scan, which DecodeConfig never reads, still counts the RGBA
// conversion it causes: the source is rejected before any pixel memory is
// allocated, although its planes alone fit the limit.
func TestLateRGBConversionCountsTowardTheLimit(t *testing.T) {
	// 4:2:0 planes of 20000 × 20000 pixels take 600 MB; the conversion
	// would add 1.6 GB.
	source := jpegWithDimensions(t, encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 16, 16))), 20000, 20000)
	adobe := jpegSegment(markerAPP14, []byte{'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, 0})
	_, err := Inspect(context.Background(), writeFile(t, "late-adobe.jpg", beforeEOI(source, adobe)))
	decodeErr := requireKind(t, err, KindTooLarge, FormatJPEG)
	if decodeErr.Width != 20000 || decodeErr.Height != 20000 {
		t.Fatalf("reported %d × %d, want 20000 × 20000", decodeErr.Width, decodeErr.Height)
	}
}
