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

	cases := []struct {
		name    string
		content []byte
		format  Format
	}{
		{"truncated.png", pngData[:len(pngData)/2], FormatPNG},
		{"checksum.png", badChecksum, FormatPNG},
		{"signature-only.png", pngSignature, FormatPNG},
		{"partial-signature.png", pngSignature[:4], FormatPNG},
		{"truncated.jpg", jpegData[:len(jpegData)/2], FormatJPEG},
		{"header-only.jpg", jpegData[:20], FormatJPEG},
		{"partial-signature.jpg", jpegSignature[:2], FormatJPEG},
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
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8], ihdr[9] = bitDepth, colorType
	chunk := append([]byte("IHDR"), ihdr[:]...)
	var buffer bytes.Buffer
	buffer.Write(pngSignature)
	binary.Write(&buffer, binary.BigEndian, uint32(len(ihdr)))
	buffer.Write(chunk)
	binary.Write(&buffer, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buffer.Bytes()
}

// TestOversizedSourcesAreRejectedBeforeDecoding pins the decompression-bomb
// guard: declared dimensions above the memory limit are rejected from the
// header alone, and 16-bit sources count double.
func TestOversizedSourcesAreRejectedBeforeDecoding(t *testing.T) {
	cases := []struct {
		name          string
		content       []byte
		format        Format
		width, height int
	}{
		{"huge.png", pngHeader(100000, 100000, 8, 6), FormatPNG, 100000, 100000},
		{"deep.png", pngHeader(16384, 12000, 16, 6), FormatPNG, 16384, 12000},
		{"huge.jpg", jpegWithDimensions(t, 65000, 65000), FormatJPEG, 65000, 65000},
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
	// the limit; with no image data after the header they are damaged
	// rather than too large.
	_, err := Inspect(context.Background(), writeFile(t, "wide.png", pngHeader(16384, 12000, 8, 6)))
	requireKind(t, err, KindCorrupt, FormatPNG)
}

// jpegWithDimensions rewrites the SOF0 frame dimensions of a small baseline
// JPEG, leaving its image data unchanged.
func jpegWithDimensions(t *testing.T, width, height uint16) []byte {
	t.Helper()
	content := encodeJPEG(t, image.NewGray(image.Rect(0, 0, 8, 8)))
	index := bytes.Index(content, []byte{0xff, 0xc0})
	if index < 0 {
		t.Fatal("baseline SOF0 marker not found")
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
