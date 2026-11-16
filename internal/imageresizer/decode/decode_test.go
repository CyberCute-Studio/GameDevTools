package decode

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func requireKind(t *testing.T, err error, kind Kind, format Format) *Error {
	t.Helper()
	var decodeErr *Error
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v (%T), want *decode.Error", err, err)
	}
	if decodeErr.Kind != kind || decodeErr.Format != format {
		t.Fatalf("error = %v (kind %v, format %v), want kind %v, format %v", err, decodeErr.Kind, decodeErr.Format, kind, format)
	}
	return decodeErr
}

func TestInspectPNGReportsDimensionsAndAlpha(t *testing.T) {
	opaque := image.NewRGBA(image.Rect(0, 0, 7, 3))
	for index := range opaque.Pix {
		opaque.Pix[index] = 0xff
	}
	translucent := image.NewNRGBA(image.Rect(0, 0, 5, 9))
	translucent.SetNRGBA(1, 1, color.NRGBA{R: 10, G: 20, B: 30, A: 40})
	deep := image.NewNRGBA64(image.Rect(0, 0, 4, 2))
	gray := image.NewGray(image.Rect(0, 0, 3, 3))
	opaquePalette := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.NRGBA{A: 0xff}, color.NRGBA{R: 0xff, A: 0xff}})
	clearPalette := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.NRGBA{A: 0xff}, color.NRGBA{}})

	cases := []struct {
		name   string
		img    image.Image
		width  int
		height int
		alpha  bool
	}{
		{"opaque truecolor", opaque, 7, 3, false},
		{"truecolor with alpha", translucent, 5, 9, true},
		{"16-bit with alpha", deep, 4, 2, true},
		{"grayscale", gray, 3, 3, false},
		{"opaque palette", opaquePalette, 2, 2, false},
		{"palette with transparency", clearPalette, 2, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, "source.png", encodePNG(t, tc.img))
			info, err := Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			want := Info{Format: FormatPNG, Width: tc.width, Height: tc.height, Orientation: OrientationNormal, HasAlpha: tc.alpha}
			if info != want {
				t.Fatalf("Inspect = %+v, want %+v", info, want)
			}
		})
	}
}

// TestDecodePNGPreservesAlpha pins the alpha handling RSZ-028 builds on:
// translucent PNG pixels decode with their exact non-premultiplied values.
func TestDecodePNGPreservesAlpha(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(60 * x), G: uint8(60 * y), B: 200, A: uint8(x*64 + y*16)})
		}
	}
	path := writeFile(t, "alpha.png", encodePNG(t, source))
	decoded, err := Decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.HasAlpha || decoded.Width != 4 || decoded.Height != 4 {
		t.Fatalf("Decode info = %+v", decoded.Info)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			got := color.NRGBAModel.Convert(decoded.Pixels.At(x, y)).(color.NRGBA)
			want := source.NRGBAAt(x, y)
			if got.A != want.A || (want.A != 0 && got != want) {
				t.Fatalf("pixel (%d, %d) = %+v, want %+v", x, y, got, want)
			}
		}
	}
}

// TestDecodeJPEGFixtures covers the JPEG encodings the standard library
// decoder supports — baseline, progressive, and Adobe CMYK — with fixtures
// copied from the Go distribution's image test data.
func TestDecodeJPEGFixtures(t *testing.T) {
	for _, name := range []string{"baseline.jpeg", "progressive.jpeg", "cmyk.jpeg"} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, name, readFixture(t, name))
			info, err := Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			want := Info{Format: FormatJPEG, Width: 150, Height: 103, Orientation: OrientationNormal}
			if info != want {
				t.Fatalf("Inspect = %+v, want %+v", info, want)
			}
			decoded, err := Decode(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if bounds := decoded.Pixels.Bounds(); bounds != image.Rect(0, 0, 150, 103) {
				t.Fatalf("decoded bounds = %v", bounds)
			}
		})
	}
}

// TestProgressiveMatchesBaseline confirms the progressive fixture decodes to
// the same picture as its baseline counterpart, within JPEG tolerance.
func TestProgressiveMatchesBaseline(t *testing.T) {
	baseline, err := Decode(context.Background(), writeFile(t, "baseline.jpeg", readFixture(t, "baseline.jpeg")))
	if err != nil {
		t.Fatal(err)
	}
	progressive, err := Decode(context.Background(), writeFile(t, "progressive.jpeg", readFixture(t, "progressive.jpeg")))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []image.Point{{10, 10}, {75, 50}, {140, 95}} {
		a := color.RGBAModel.Convert(baseline.Pixels.At(point.X, point.Y)).(color.RGBA)
		b := color.RGBAModel.Convert(progressive.Pixels.At(point.X, point.Y)).(color.RGBA)
		if !near(a, b, 12) {
			t.Fatalf("pixel %v: baseline %+v, progressive %+v", point, a, b)
		}
	}
}

// TestContentDecidesFormat pins RSZ-003: the decoded content, not the
// extension, identifies the format.
func TestContentDecidesFormat(t *testing.T) {
	square := image.NewRGBA(image.Rect(0, 0, 6, 4))
	jpegAsPNG := writeFile(t, "photo.png", encodeJPEG(t, square))
	info, err := Inspect(context.Background(), jpegAsPNG)
	if err != nil || info.Format != FormatJPEG || info.Width != 6 || info.Height != 4 {
		t.Fatalf("JPEG content named .png: %+v, %v", info, err)
	}
	pngAsJPEG := writeFile(t, "art.jpg", encodePNG(t, square))
	info, err = Inspect(context.Background(), pngAsJPEG)
	if err != nil || info.Format != FormatPNG {
		t.Fatalf("PNG content named .jpg: %+v, %v", info, err)
	}
}

func TestUnrecognizedContentIsUnsupported(t *testing.T) {
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"text.png":  []byte("this is not an image at all"),
		"gif.jpg":   gifData.Bytes(),
		"short.png": []byte("BM"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Inspect(context.Background(), writeFile(t, name, content))
			requireKind(t, err, KindUnsupported, FormatUnknown)
			if !errors.Is(err, ErrUnrecognized) {
				t.Fatalf("error = %v, want ErrUnrecognized", err)
			}
		})
	}
}

func TestEmptyFileIsCorrupt(t *testing.T) {
	_, err := Inspect(context.Background(), writeFile(t, "empty.png", nil))
	requireKind(t, err, KindCorrupt, FormatUnknown)
	if !errors.Is(err, ErrEmpty) {
		t.Fatalf("error = %v, want ErrEmpty", err)
	}
}

func TestDamagedContentIsCorrupt(t *testing.T) {
	gradient := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for index := range gradient.Pix {
		gradient.Pix[index] = uint8(index * 7)
	}
	pngData := encodePNG(t, gradient)
	jpegData := encodeJPEG(t, gradient)
	badChecksum := append([]byte(nil), pngData...)
	// Flip one byte inside the first chunk after IHDR so its CRC no longer
	// matches.
	badChecksum[len(pngSignature)+25+10] ^= 0xff
	// A transfer that converted line endings turns the signature's CRLF
	// into LF, which is the damage the PNG signature is designed to reveal.
	lineEndings := append([]byte("\x89PNG\n\x1a\n"), pngData[len(pngSignature):]...)

	cases := []struct {
		name    string
		content []byte
		format  Format
	}{
		{"truncated.png", pngData[:len(pngData)/2], FormatPNG},
		{"checksum.png", badChecksum, FormatPNG},
		{"signature-only.png", pngSignature, FormatPNG},
		{"partial-signature.png", pngSignature[:4], FormatPNG},
		{"line-endings.png", lineEndings, FormatPNG},
		{"truncated.jpg", jpegData[:len(jpegData)/2], FormatJPEG},
		{"header-only.jpg", jpegData[:20], FormatJPEG},
		{"partial-signature.jpg", jpegSignature[:1], FormatJPEG},
		{"signature-only.jpg", jpegSignature, FormatJPEG},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, tc.name, tc.content)
			_, err := Inspect(context.Background(), path)
			requireKind(t, err, KindCorrupt, tc.format)
			if _, err := Decode(context.Background(), path); err == nil {
				t.Fatal("Decode accepted damaged content")
			}
		})
	}
}

// TestUnsupportedJPEGFeature pins that recognized JPEG data using a coding
// process the standard library does not implement — here an arithmetic
// coding SOF9 frame — is Unsupported rather than damaged.
func TestUnsupportedJPEGFeature(t *testing.T) {
	content := encodeJPEG(t, image.NewGray(image.Rect(0, 0, 8, 8)))
	index := bytes.Index(content, []byte{0xff, 0xc0})
	if index < 0 {
		t.Fatal("baseline SOF0 marker not found")
	}
	content[index+1] = 0xc9
	_, err := Inspect(context.Background(), writeFile(t, "arithmetic.jpg", content))
	requireKind(t, err, KindUnsupported, FormatJPEG)
}

// pngHeader builds a PNG signature and a valid IHDR chunk declaring the given
// dimensions, with no image data: enough for DecodeConfig.
func pngHeader(width, height uint32, bitDepth, colorType byte) []byte {
	return pngHeaderInterlaced(width, height, bitDepth, colorType, false)
}

func pngHeaderInterlaced(width, height uint32, bitDepth, colorType byte, interlaced bool) []byte {
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8], ihdr[9] = bitDepth, colorType
	if interlaced {
		ihdr[12] = 1
	}
	chunk := append([]byte("IHDR"), ihdr[:]...)
	var buffer bytes.Buffer
	buffer.Write(pngSignature)
	binary.Write(&buffer, binary.BigEndian, uint32(len(ihdr)))
	buffer.Write(chunk)
	binary.Write(&buffer, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buffer.Bytes()
}

// TestOversizedSourcesAreRejectedBeforeDecoding pins the decompression-bomb
// guard: headers whose decoding would allocate more than the memory limit
// are rejected from the header alone, counting the decoder's working
// buffers — 16-bit and interlaced PNG, a progressive JPEG's coefficients —
// and reporting upright dimensions.
func TestOversizedSourcesAreRejectedBeforeDecoding(t *testing.T) {
	cases := []struct {
		name          string
		content       []byte
		format        Format
		width, height int
	}{
		{"huge.png", pngHeader(100000, 100000, 8, 6), FormatPNG, 100000, 100000},
		{"deep.png", pngHeader(16384, 12000, 16, 6), FormatPNG, 16384, 12000},
		{"interlaced.png", pngHeaderInterlaced(16384, 12000, 8, 6, true), FormatPNG, 16384, 12000},
		// The PNG decoder itself reports these as an unsupported feature,
		// a dimension overflow.
		{"overflow.png", pngHeader(math.MaxInt32, math.MaxInt32, 8, 6), FormatPNG, math.MaxInt32, math.MaxInt32},
		{"huge.jpg", jpegWithDimensions(t, encodeJPEG(t, image.NewGray(image.Rect(0, 0, 8, 8))), 65000, 65000), FormatJPEG, 65000, 65000},
		// 2^28 pixels fit 4 bytes each, but the decoder allocates 7.5 bytes
		// per pixel for a progressive 4:2:0 frame's planes and coefficients.
		{"progressive.jpg", jpegWithDimensions(t, readFixture(t, "progressive.jpeg"), 16384, 16384), FormatJPEG, 16384, 16384},
		{"oriented.jpg", withSegments(jpegWithDimensions(t, encodeJPEG(t, image.NewGray(image.Rect(0, 0, 8, 8))), 65000, 30000), exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort)), FormatJPEG, 30000, 65000},
		// The 384 MiB of 4:2:0 planes fit, but not with the 1 GiB upright
		// RGBA copy.
		{"reoriented.jpg", withSegments(jpegWithDimensions(t, encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 16, 16))), 16384, 16384), exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort)), FormatJPEG, 16384, 16384},
		// The decoder skips an unknown chunk before IHDR, and so does the
		// scan.
		{"late-header.png", lateHeaderPNG(20000, 20000), FormatPNG, 20000, 20000},
		// A tRNS chunk makes the decoder allocate gray as 4-byte NRGBA.
		{"transparent-gray.png", append(pngHeader(20000, 20000, 8, 0), pngChunk("tRNS", []byte{0, 7})...), FormatPNG, 20000, 20000},
		// The 1 GiB of gray pixels fit, but not with the two 1 GiB rows the
		// decoder reads them through.
		{"wide-row.png", pngHeader(1<<30, 1, 8, 0), FormatPNG, 1 << 30, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Inspect(context.Background(), writeFile(t, tc.name, tc.content))
			decodeErr := requireKind(t, err, KindTooLarge, tc.format)
			if decodeErr.Width != tc.width || decodeErr.Height != tc.height {
				t.Fatalf("reported %d × %d, want %d × %d", decodeErr.Width, decodeErr.Height, tc.width, tc.height)
			}
		})
	}
	// The same 8-bit dimensions that a 16-bit source exceeds stay within
	// the limit, and so does opaque gray, which decodes at one byte per
	// pixel, and 2^28 8-bit truecolor pixels, whose rows fit the allowance;
	// with no image data after the header they are damaged rather than too
	// large. A gray source cut short before its image data, or stopped by a
	// second header, is costed as what its chunks declared, not as a
	// transparent one, since the decoder fails on it before allocating.
	for name, content := range map[string][]byte{
		"wide.png":          pngHeader(16384, 12000, 8, 6),
		"square.png":        append(pngHeader(16384, 16384, 8, 2), pngChunk("IDAT", nil)...),
		"gray.png":          append(pngHeader(20000, 20000, 8, 0), pngChunk("IDAT", nil)...),
		"cut-gray.png":      append(pngHeader(20000, 20000, 8, 0), pngChunk("tEXt", []byte("Comment\x00cut"))...),
		"second-header.png": append(pngHeader(20000, 20000, 8, 0), pngHeader(20000, 20000, 8, 0)[len(pngSignature):]...),
	} {
		_, err := Inspect(context.Background(), writeFile(t, name, content))
		requireKind(t, err, KindCorrupt, FormatPNG)
	}
}

// lateHeaderPNG builds a PNG whose IHDR follows an unknown text chunk, which
// the standard library decoder skips.
func lateHeaderPNG(width, height uint32) []byte {
	text := append([]byte("tEXt"), "Comment\x00first"...)
	var buffer bytes.Buffer
	buffer.Write(pngSignature)
	binary.Write(&buffer, binary.BigEndian, uint32(len(text)-4))
	buffer.Write(text)
	binary.Write(&buffer, binary.BigEndian, crc32.ChecksumIEEE(text))
	buffer.Write(pngHeader(width, height, 8, 6)[len(pngSignature):])
	return buffer.Bytes()
}

// jpegWithDimensions rewrites the frame dimensions of a small baseline or
// progressive JPEG, leaving its image data unchanged.
func jpegWithDimensions(t *testing.T, source []byte, width, height uint16) []byte {
	t.Helper()
	content := append([]byte(nil), source...)
	index := bytes.Index(content, []byte{0xff, markerSOF0})
	if index < 0 {
		index = bytes.Index(content, []byte{0xff, markerSOF2})
	}
	if index < 0 {
		t.Fatal("SOF0 or SOF2 marker not found")
	}
	// Marker (2), length (2), precision (1), height (2), width (2).
	binary.BigEndian.PutUint16(content[index+5:], height)
	binary.BigEndian.PutUint16(content[index+7:], width)
	return content
}

func TestReadFailuresAreReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.png")
	_, err := Inspect(context.Background(), missing)
	requireKind(t, err, KindRead, FormatUnknown)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
	_, err = Inspect(context.Background(), t.TempDir())
	requireKind(t, err, KindRead, FormatUnknown)
}

func TestCancellation(t *testing.T) {
	path := writeFile(t, "source.png", encodePNG(t, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Inspect(ctx, path)
	requireKind(t, err, KindCanceled, FormatUnknown)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	// A cancellation observed between reads is reported as such, not as
	// damaged content, even though the decoder sees a failed read.
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	readCtx, readCancel := context.WithCancel(context.Background())
	source := &sourceReader{ctx: readCtx, file: file}
	readCancel()
	if _, err := source.Read(make([]byte, 8)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read after cancel = %v, want context.Canceled", err)
	}
	if classified := source.classify(FormatPNG, errors.New("png: invalid format")); classified.Kind != KindCanceled {
		t.Fatalf("classify after cancel = %v, want KindCanceled", classified.Kind)
	}
}

// scriptedFile serves content like an open file and lets a test act at an
// exact pass of a decode. The signature sniff and the header scan share the
// first pass; the header parse, the conversion walk when it runs, and the
// decode each start with a seek, so seeks counts the passes begun after the
// first.
type scriptedFile struct {
	*bytes.Reader
	seeks int
	// failing makes the first read in pass failPass that starts at or past
	// offset failFrom fail, once.
	failing  bool
	failPass int
	failFrom int64
	failed   bool
	// afterRead runs after every successful read.
	afterRead func(seeks int)
}

var errScriptedRead = errors.New("the specified network name is no longer available")

func (f *scriptedFile) Seek(offset int64, whence int) (int64, error) {
	f.seeks++
	return f.Reader.Seek(offset, whence)
}

func (f *scriptedFile) Read(buffer []byte) (int, error) {
	if f.failing && !f.failed && f.seeks == f.failPass && f.Size()-int64(f.Len()) >= f.failFrom {
		f.failed = true
		return 0, errScriptedRead
	}
	n, err := f.Reader.Read(buffer)
	if f.afterRead != nil {
		f.afterRead(f.seeks)
	}
	return n, err
}

// TestReadFailureDuringTheScanIsReported pins that a transient read failure
// while the header is scanned fails the source as unreadable rather than as
// damaged or too large. Otherwise a JPEG scan would see no orientation, the
// later passes would read fine, and the source would be listed as Ready with
// its stored rather than upright dimensions (RSZ-018). Each header runs past
// the first buffer, so the failing read comes after the signature was
// sniffed.
func TestReadFailureDuringTheScanIsReported(t *testing.T) {
	comment := jpegSegment(0xfe, make([]byte, 40000))
	oriented := withSegments(encodeJPEG(t, image.NewGray(image.Rect(0, 0, 40, 30))), comment, comment, exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort))
	gray := bytes.Join([][]byte{pngHeader(20000, 20000, 8, 0), pngChunk("tEXt", make([]byte, 2*readBufferSize)), pngChunk("IDAT", nil)}, nil)

	info, _, err := decodeSource(&sourceReader{ctx: context.Background(), file: &scriptedFile{Reader: bytes.NewReader(oriented)}}, false)
	if err != nil || info.Width != 30 || info.Height != 40 {
		t.Fatalf("intact read = %+v, %v; want 30 × 40", info, err)
	}
	// Without image data the gray source is damaged, not too large.
	_, _, err = decodeSource(&sourceReader{ctx: context.Background(), file: &scriptedFile{Reader: bytes.NewReader(gray)}}, false)
	requireKind(t, err, KindCorrupt, FormatPNG)

	for format, content := range map[Format][]byte{FormatJPEG: oriented, FormatPNG: gray} {
		file := &scriptedFile{Reader: bytes.NewReader(content), failing: true, failFrom: readBufferSize}
		_, _, err := decodeSource(&sourceReader{ctx: context.Background(), file: file}, false)
		requireKind(t, err, KindRead, format)
		if !errors.Is(err, errScriptedRead) {
			t.Fatalf("%v: error = %v, want the read failure", format, err)
		}
	}
}

// TestCancellationAfterTheLastRead pins that a decode cancelled once the
// decoder has read everything it needs still reports the cancellation,
// instead of going on to the orientation transform.
func TestCancellationAfterTheLastRead(t *testing.T) {
	content := withSegments(encodeJPEG(t, image.NewGray(image.Rect(0, 0, 40, 30))), exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := &scriptedFile{Reader: bytes.NewReader(content), afterRead: func(seeks int) {
		// The decode pass reads the whole small file at once.
		if seeks == 2 {
			cancel()
		}
	}}
	info, pixels, err := decodeSource(&sourceReader{ctx: ctx, file: file}, true)
	requireKind(t, err, KindCanceled, FormatJPEG)
	if pixels != nil || info != (Info{}) {
		t.Fatalf("cancelled decode returned %+v with pixels %v", info, pixels != nil)
	}
}

// TestInspectReleasesLargeDecodes pins NFR-014 across back-to-back checks:
// after a decode that allocated a large amount Inspect returns the discarded
// memory at once, while small sources, sources rejected from their header,
// and sources that fail before the decoder allocates their pixels skip the
// forced collection.
func TestInspectReleasesLargeDecodes(t *testing.T) {
	releases := 0
	previous := releaseMemory
	releaseMemory = func() { releases++ }
	t.Cleanup(func() { releaseMemory = previous })

	if _, err := Inspect(context.Background(), writeFile(t, "small.png", encodePNG(t, image.NewRGBA(image.Rect(0, 0, 8, 8))))); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), writeFile(t, "huge.png", pngHeader(100000, 100000, 8, 6))); err == nil {
		t.Fatal("Inspect accepted an oversized source")
	}
	// The header declares just over 64 MiB of gray pixels, but the empty
	// image data fails before the decoder allocates them.
	_, err := Inspect(context.Background(), writeFile(t, "empty.png", append(pngHeader(8192, 8200, 8, 0), pngChunk("IDAT", nil)...)))
	requireKind(t, err, KindCorrupt, FormatPNG)
	// A rotated 4096 × 4096 gray JPEG decodes into a 16 MiB plane, and
	// Inspect never makes its upright copy.
	rotated := withSegments(encodeJPEG(t, image.NewGray(image.Rect(0, 0, 4096, 4096))), exifSegment(binary.BigEndian, uint32(OrientationRotate90), tiffTypeShort))
	if _, err := Inspect(context.Background(), writeFile(t, "rotated.jpg", rotated)); err != nil {
		t.Fatal(err)
	}
	if releases != 0 {
		t.Fatalf("%d releases for small, rejected, failed, and rotated sources, want 0", releases)
	}
	// A two-color palette encodes at one bit per pixel and decodes at one
	// byte per pixel, so the file stays small while its decoded pixels pass
	// the threshold.
	large := image.NewPaletted(image.Rect(0, 0, 8192, 8200), color.Palette{color.Black, color.White})
	if _, err := Inspect(context.Background(), writeFile(t, "large.png", encodePNG(t, large))); err != nil {
		t.Fatal(err)
	}
	if releases != 1 {
		t.Fatalf("%d releases after a large source, want 1", releases)
	}
}

// TestFileSystemFailureIsNotDamage pins that a read that fails part-way is
// reported as a read failure, even though the decoder reports it as a
// format error or truncation.
func TestFileSystemFailureIsNotDamage(t *testing.T) {
	diskErr := errors.New("the device is not ready")
	source := &sourceReader{ctx: context.Background(), readErr: diskErr}
	classified := source.classify(FormatJPEG, errors.New("unexpected EOF"))
	if classified.Kind != KindRead || classified.Format != FormatJPEG || !errors.Is(classified, diskErr) {
		t.Fatalf("classify with a read failure = %+v", classified)
	}
	unsupported := (&sourceReader{ctx: context.Background()}).classify(FormatPNG, png.UnsupportedError("bit depth"))
	if unsupported.Kind != KindUnsupported {
		t.Fatalf("classify(png.UnsupportedError) = %v, want KindUnsupported", unsupported.Kind)
	}
}

// TestSourcesAreOpenedReadOnly pins RSZ-009: a read-only source decodes, and
// neither its content nor its modification time changes.
func TestSourcesAreOpenedReadOnly(t *testing.T) {
	content := encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	path := writeFile(t, "locked.jpg", content)
	old := time.Now().Add(-time.Hour).Round(0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if _, err := Inspect(context.Background(), path); err != nil {
		t.Fatalf("Inspect(read-only) = %v", err)
	}
	if _, err := Decode(context.Background(), path); err != nil {
		t.Fatalf("Decode(read-only) = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, content) {
		t.Fatal("source content changed")
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stat.ModTime().Equal(old) {
		t.Fatalf("source modification time changed: %v -> %v", old, stat.ModTime())
	}
}

func TestErrorPresentation(t *testing.T) {
	cause := errors.New("boom")
	cases := []struct {
		err  *Error
		want string
	}{
		{&Error{Kind: KindRead, Err: cause}, "read source: boom"},
		{&Error{Kind: KindCorrupt, Format: FormatPNG, Err: cause}, "decode PNG: boom"},
		{&Error{Kind: KindUnsupported, Err: ErrUnrecognized}, "decode source: content is not PNG or JPEG image data"},
		{&Error{Kind: KindTooLarge, Format: FormatJPEG, Err: cause}, "decode JPEG: boom"},
		{&Error{Kind: KindCanceled, Err: context.Canceled}, "decode canceled: context canceled"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Fatalf("Error() = %q, want %q", got, tc.want)
		}
		if !errors.Is(tc.err, tc.err.Err) {
			t.Fatalf("%v does not unwrap to its cause", tc.err)
		}
	}
}

func near(a, b color.RGBA, tolerance int) bool {
	within := func(x, y uint8) bool {
		difference := int(x) - int(y)
		return difference <= tolerance && difference >= -tolerance
	}
	return within(a.R, b.R) && within(a.G, b.G) && within(a.B, b.B) && within(a.A, b.A)
}
