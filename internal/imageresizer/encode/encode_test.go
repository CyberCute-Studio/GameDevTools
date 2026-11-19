package encode

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

var white = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

func pngOptions() Options { return Options{Format: decode.FormatPNG} }

func jpegOptions(background color.RGBA) Options {
	return Options{Format: decode.FormatJPEG, Quality: 90, Background: background}
}

func mustEncode(t *testing.T, img image.Image, options Options) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := Encode(&out, img, options); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return out.Bytes()
}

func requireKind(t *testing.T, err error, kind Kind) *Error {
	t.Helper()
	var encodeErr *Error
	if !errors.As(err, &encodeErr) || encodeErr.Kind != kind {
		t.Fatalf("error = %v, want a %v *Error", err, kind)
	}
	return encodeErr
}

// resizerTypes returns the same seeded picture as every image type the
// resizer produces: grayscale, opaque color, and translucent color, at 8 and
// 16 bits per channel. The translucent pictures include fully transparent
// and fully opaque pixels.
func resizerTypes(width, height int) map[string]image.Image {
	rng := rand.New(rand.NewPCG(7, 11))
	rect := image.Rect(0, 0, width, height)
	gray, gray16 := image.NewGray(rect), image.NewGray16(rect)
	rgba, rgba64 := image.NewRGBA(rect), image.NewRGBA64(rect)
	nrgba, nrgba64 := image.NewNRGBA(rect), image.NewNRGBA64(rect)
	for y := range height {
		for x := range width {
			gray.SetGray(x, y, color.Gray{Y: uint8(rng.Uint32())})
			gray16.SetGray16(x, y, color.Gray16{Y: uint16(rng.Uint32())})
			rgba.SetRGBA(x, y, color.RGBA{R: uint8(rng.Uint32()), G: uint8(rng.Uint32()), B: uint8(rng.Uint32()), A: 0xff})
			rgba64.SetRGBA64(x, y, color.RGBA64{R: uint16(rng.Uint32()), G: uint16(rng.Uint32()), B: uint16(rng.Uint32()), A: 0xffff})
			alpha := uint8(rng.Uint32())
			switch (x + y) % 3 {
			case 0:
				alpha = 0
			case 1:
				alpha = 0xff
			}
			nrgba.SetNRGBA(x, y, color.NRGBA{R: uint8(rng.Uint32()), G: uint8(rng.Uint32()), B: uint8(rng.Uint32()), A: alpha})
			nrgba64.SetNRGBA64(x, y, color.NRGBA64{R: uint16(rng.Uint32()), G: uint16(rng.Uint32()), B: uint16(rng.Uint32()), A: uint16(alpha) * 0x101})
		}
	}
	return map[string]image.Image{
		"Gray": gray, "Gray16": gray16, "RGBA": rgba, "RGBA64": rgba64, "NRGBA": nrgba, "NRGBA64": nrgba64,
	}
}

// TestPNGIsLosslessAndKeepsAlpha pins RSZ-028 and AC-R11: every image type
// the resizer produces decodes back from PNG with its exact pixels, its own
// depth, and its alpha channel.
func TestPNGIsLosslessAndKeepsAlpha(t *testing.T) {
	want := map[string]reflect.Type{
		"Gray":    reflect.TypeFor[*image.Gray](),
		"Gray16":  reflect.TypeFor[*image.Gray16](),
		"RGBA":    reflect.TypeFor[*image.RGBA](),
		"RGBA64":  reflect.TypeFor[*image.RGBA64](),
		"NRGBA":   reflect.TypeFor[*image.NRGBA](),
		"NRGBA64": reflect.TypeFor[*image.NRGBA64](),
	}
	for name, img := range resizerTypes(13, 7) {
		decoded, err := png.Decode(bytes.NewReader(mustEncode(t, img, pngOptions())))
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if got := reflect.TypeOf(decoded); got != want[name] {
			t.Fatalf("%s: decoded as %v, want %v", name, got, want[name])
		}
		bounds := img.Bounds()
		if decoded.Bounds() != bounds {
			t.Fatalf("%s: bounds = %v, want %v", name, decoded.Bounds(), bounds)
		}
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				if got, want := color.NRGBA64Model.Convert(decoded.At(x, y)), color.NRGBA64Model.Convert(img.At(x, y)); got != want {
					t.Fatalf("%s: pixel (%d, %d) = %v, want %v", name, x, y, got, want)
				}
			}
		}
	}
}

// TestPNGLeavesTheImageUnchanged: only JPEG output converts the image in
// place.
func TestPNGLeavesTheImageUnchanged(t *testing.T) {
	for name, img := range resizerTypes(5, 4) {
		before := reflect.ValueOf(img).Elem().FieldByName("Pix").Bytes()
		before = append([]byte(nil), before...)
		mustEncode(t, img, pngOptions())
		if after := reflect.ValueOf(img).Elem().FieldByName("Pix").Bytes(); !bytes.Equal(before, after) {
			t.Fatalf("%s: PNG encoding changed the image", name)
		}
	}
}

func uniformNRGBA(width, height int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func near(got, want color.RGBA, tolerance int) bool {
	diff := func(a, b uint8) bool { return int(a)-int(b) <= tolerance && int(b)-int(a) <= tolerance }
	return diff(got.R, want.R) && diff(got.G, want.G) && diff(got.B, want.B)
}

// TestJPEGFlattensTransparencyAgainstTheBackground pins RSZ-028 and AC-R11:
// a fully transparent pixel becomes the background color, a translucent one
// a blend in proportion to its alpha, and an opaque one keeps its color, at
// 8 and 16 bits per channel.
func TestJPEGFlattensTransparencyAgainstTheBackground(t *testing.T) {
	blue := color.RGBA{B: 0xff, A: 0xff}
	cases := []struct {
		name       string
		pixel      color.NRGBA
		background color.RGBA
		want       color.RGBA
	}{
		{"transparent green on blue", color.NRGBA{G: 0xff}, blue, blue},
		{"transparent on white", color.NRGBA{R: 0x12, G: 0x34, B: 0x56}, white, white},
		{"half red on white", color.NRGBA{R: 0xff, A: 0x80}, white, color.RGBA{R: 0xff, G: 0x7f, B: 0x7f}},
		{"opaque red on blue", color.NRGBA{R: 0xff, A: 0xff}, blue, color.RGBA{R: 0xff}},
	}
	for _, tc := range cases {
		for _, wide := range []bool{false, true} {
			var img image.Image = uniformNRGBA(32, 32, tc.pixel)
			if wide {
				img64 := image.NewNRGBA64(img.Bounds())
				for y := range 32 {
					for x := range 32 {
						img64.Set(x, y, img.At(x, y))
					}
				}
				img = img64
			}
			decoded, err := jpeg.Decode(bytes.NewReader(mustEncode(t, img, jpegOptions(tc.background))))
			if err != nil {
				t.Fatalf("%s: decode: %v", tc.name, err)
			}
			if decoded.Bounds() != image.Rect(0, 0, 32, 32) {
				t.Fatalf("%s: bounds = %v", tc.name, decoded.Bounds())
			}
			got := color.RGBAModel.Convert(decoded.At(16, 16)).(color.RGBA)
			if !near(got, tc.want, 3) {
				t.Fatalf("%s (16-bit %v): pixel = %+v, want %+v", tc.name, wide, got, tc.want)
			}
		}
	}
}

// TestJPEGPixelsAreExact pins the in-place conversion arithmetic before the
// lossy encoder runs: each sample is flattened and rounded once, to the
// nearest 8-bit value, and 16-bit rows are packed without disturbing the
// samples still to be read, including in a sub-image whose stride is wider
// than its rows.
func TestJPEGPixelsAreExact(t *testing.T) {
	background := color.RGBA{R: 10, G: 200, B: 0xff, A: 0xff}
	nrgba := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	copy(nrgba.Pix, []uint8{
		0xff, 0x00, 0x80, 0x00, // transparent: the background
		0xff, 0x00, 0x80, 0xff, // opaque: unchanged
		0xff, 0x00, 0x80, 0x80, // half: (v×128 + bg×127 + 127) / 255
		0x00, 0xff, 0x00, 0x01,
	})
	got := jpegPixels(nrgba, background).(*image.RGBA)
	want := []uint8{
		10, 200, 0xff, 0xff,
		0xff, 0x00, 0x80, 0xff,
		(0xff*0x80 + 10*0x7f + 0x7f) / 0xff, (200*0x7f + 0x7f) / 0xff, (0x80*0x80 + 0xff*0x7f + 0x7f) / 0xff, 0xff,
		(10*0xfe + 0x7f) / 0xff, (0xff + 200*0xfe + 0x7f) / 0xff, (0xff*0xfe + 0x7f) / 0xff, 0xff,
	}
	if !bytes.Equal(got.Pix, want) {
		t.Fatalf("NRGBA flattened to %v, want %v", got.Pix, want)
	}

	// A 3 × 2 sub-image of a 5 × 3 16-bit picture, so its rows are spaced
	// wider than they are packed.
	full := image.NewNRGBA64(image.Rect(0, 0, 5, 3))
	for y := range 3 {
		for x := range 5 {
			full.SetNRGBA64(x, y, color.NRGBA64{R: uint16(0x1000*x + 0x100*y), G: 0xffff, B: 0x0080, A: 0xffff})
		}
	}
	full.SetNRGBA64(2, 2, color.NRGBA64{R: 0xffff, G: 0, B: 0, A: 0x8000})
	sub := full.SubImage(image.Rect(1, 1, 4, 3)).(*image.NRGBA64)
	packed := jpegPixels(sub, background).(*image.RGBA)
	if packed.Rect != image.Rect(1, 1, 4, 3) || packed.Stride != 12 || len(packed.Pix) != 24 {
		t.Fatalf("packed image = %v stride %d, %d bytes", packed.Rect, packed.Stride, len(packed.Pix))
	}
	for y := 1; y < 3; y++ {
		for x := 1; x < 4; x++ {
			// An opaque 16-bit sample v rounds to v / 257, the nearest 8-bit
			// value: 0x0080 to 0.
			red := 0x1000*x + 0x100*y
			wantC := color.RGBA{R: uint8((2*red + 257) / 514), G: 0xff, B: 0, A: 0xff}
			if x == 2 && y == 2 {
				// Red at alpha 0x8000 / 0xffff over (10, 200, 255), worked
				// out by hand: 132.502, 99.998, and 127.498 round to these.
				wantC = color.RGBA{R: 133, G: 100, B: 127, A: 0xff}
			}
			if c := packed.RGBAAt(x, y); c != wantC {
				t.Fatalf("pixel (%d, %d) = %+v, want %+v", x, y, c, wantC)
			}
		}
	}

	gray := image.NewGray16(image.Rect(0, 0, 3, 1))
	copy(gray.Pix, []uint8{0x00, 0x80, 0x7f, 0x80, 0xff, 0xff})
	if got := jpegPixels(gray, background).(*image.Gray).Pix; !bytes.Equal(got, []uint8{0, 0x7f, 0xff}) {
		t.Fatalf("Gray16 narrowed to %v", got)
	}

	premultiplied := image.NewRGBA(image.Rect(0, 0, 2, 1))
	copy(premultiplied.Pix, []uint8{0x40, 0x00, 0x00, 0x80, 0xff, 0xff, 0x00, 0x80})
	if got := jpegPixels(premultiplied, background).(*image.RGBA).Pix; !bytes.Equal(got, []uint8{
		(0x40*0xff + 10*0x7f + 0x7f) / 0xff, (200*0x7f + 0x7f) / 0xff, (0xff*0x7f + 0x7f) / 0xff, 0xff,
		0xff, 0xff, (0xff*0x7f + 0x7f) / 0xff, 0xff,
	}) {
		t.Fatalf("premultiplied RGBA flattened to %v", got)
	}
}

// TestJPEGKeepsGrayscale: a grayscale result is stored as a one-component
// JPEG, at 8 and 16 bits per sample.
func TestJPEGKeepsGrayscale(t *testing.T) {
	for _, name := range []string{"Gray", "Gray16"} {
		img := resizerTypes(24, 16)[name]
		decoded, err := jpeg.Decode(bytes.NewReader(mustEncode(t, img, jpegOptions(white))))
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if _, ok := decoded.(*image.Gray); !ok {
			t.Fatalf("%s: decoded as %T, want *image.Gray", name, decoded)
		}
	}
}

// TestJPEGUsesTheQuality pins RSZ-029: a higher quality spends more bytes on
// the same picture.
func TestJPEGUsesTheQuality(t *testing.T) {
	sizes := map[int]int{}
	for _, quality := range []int{MinQuality, 50, MaxQuality} {
		img := resizerTypes(64, 64)["RGBA"]
		options := jpegOptions(white)
		options.Quality = quality
		sizes[quality] = len(mustEncode(t, img, options))
	}
	if !(sizes[MinQuality] < sizes[50] && sizes[50] < sizes[MaxQuality]) {
		t.Fatalf("sizes by quality = %v, want increasing", sizes)
	}
}

// TestEncodingIsDeterministic pins that the same image and options give the
// same bytes.
func TestEncodingIsDeterministic(t *testing.T) {
	for _, options := range []Options{pngOptions(), jpegOptions(white)} {
		for name := range resizerTypes(9, 9) {
			first := mustEncode(t, resizerTypes(9, 9)[name], options)
			second := mustEncode(t, resizerTypes(9, 9)[name], options)
			if !bytes.Equal(first, second) {
				t.Fatalf("%s %v: encodings differ", name, options.Format)
			}
		}
	}
}

// TestNoMetadataIsWritten: the output carries no EXIF, so no orientation tag
// can turn the already upright pixels again (RSZ-018).
func TestNoMetadataIsWritten(t *testing.T) {
	for _, options := range []Options{pngOptions(), jpegOptions(white)} {
		out := mustEncode(t, resizerTypes(8, 8)["RGBA"], options)
		if bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte("eXIf")) {
			t.Fatalf("%v output holds EXIF metadata", options.Format)
		}
	}
}

func TestEncodeRejectsInvalidRequests(t *testing.T) {
	small := image.NewRGBA(image.Rect(0, 0, 2, 2))
	cases := []struct {
		name    string
		img     image.Image
		options Options
		want    error
	}{
		{"unknown format", small, Options{Format: decode.FormatUnknown}, ErrFormat},
		{"quality zero", small, Options{Format: decode.FormatJPEG, Quality: 0, Background: white}, ErrQuality},
		{"quality over 100", small, Options{Format: decode.FormatJPEG, Quality: 101, Background: white}, ErrQuality},
		{"translucent background", small, Options{Format: decode.FormatJPEG, Quality: 90, Background: color.RGBA{R: 0xff, A: 0x80}}, ErrBackground},
		{"nil image", nil, pngOptions(), ErrEmptyImage},
		{"empty image", image.NewRGBA(image.Rect(3, 3, 3, 9)), jpegOptions(white), ErrEmptyImage},
		// The pixels are never read: the dimensions are checked first.
		{"too wide for JPEG", &image.Gray{Rect: image.Rect(0, 0, MaxJPEGDimension+1, 1)}, jpegOptions(white), ErrTooLargeForJPEG},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		err := Encode(&out, tc.img, tc.options)
		requireKind(t, err, KindInvalid)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: %d bytes were written", tc.name, out.Len())
		}
	}
	// PNG ignores the JPEG settings.
	if err := (Options{Format: decode.FormatPNG, Quality: -4}).Validate(); err != nil {
		t.Fatalf("PNG options with an unused quality: %v", err)
	}
}

type failingWriter struct {
	budget int
	err    error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if len(p) > f.budget {
		n := f.budget
		f.budget = 0
		return n, f.err
	}
	f.budget -= len(p)
	return len(p), nil
}

// TestEncodeReportsWriteFailures: a failure of the writer is reported as a
// write failure wrapping the writer's own error, whatever the encoder makes
// of it, for both formats.
func TestEncodeReportsWriteFailures(t *testing.T) {
	diskFull := errors.New("disk full")
	for _, options := range []Options{pngOptions(), jpegOptions(white)} {
		for _, budget := range []int{0, 100} {
			err := Encode(&failingWriter{budget: budget, err: diskFull}, resizerTypes(64, 64)["NRGBA"], options)
			requireKind(t, err, KindWrite)
			if !errors.Is(err, diskFull) {
				t.Fatalf("%v after %d bytes: error = %v, want it to wrap the writer's", options.Format, budget, err)
			}
		}
	}
}

// panickyBounds is an image whose Bounds method panics.
type panickyBounds struct{ image.Image }

func (panickyBounds) Bounds() image.Rectangle { panic("no bounds") }

// TestEncodeContainsPanics pins NFR-021: a malformed image whose pixel slice
// is shorter than its bounds, a nil image of a concrete type, or one whose
// Bounds method panics fails its own result rather than crashing the caller.
func TestEncodeContainsPanics(t *testing.T) {
	for _, options := range []Options{pngOptions(), jpegOptions(white)} {
		for _, img := range []image.Image{
			&image.NRGBA{Pix: make([]uint8, 7), Stride: 16, Rect: image.Rect(0, 0, 4, 4)},
			&image.Gray16{Pix: make([]uint8, 3), Stride: 8, Rect: image.Rect(0, 0, 4, 4)},
			(*image.RGBA)(nil),
			panickyBounds{},
		} {
			err := Encode(&bytes.Buffer{}, img, options)
			requireKind(t, err, KindFailed)
			if !errors.Is(err, ErrUnexpected) {
				t.Fatalf("%v %T: error = %v, want ErrUnexpected", options.Format, img, err)
			}
		}
	}
}

// TestJPEGReadsOtherImageTypes: an image type the resizer never produces is
// read through At and flattened like the others, a nil color reading as
// opaque black.
func TestJPEGReadsOtherImageTypes(t *testing.T) {
	palette := color.Palette{color.NRGBA{R: 0xff, A: 0xff}, color.NRGBA{}}
	paletted := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	paletted.Pix[1] = 1
	got := jpegPixels(paletted, color.RGBA{G: 0xff, A: 0xff}).(*image.RGBA).Pix
	if want := []uint8{0xff, 0, 0, 0xff, 0, 0xff, 0, 0xff}; !bytes.Equal(got, want) {
		t.Fatalf("paletted flattened to %v, want %v", got, want)
	}
	nilPalette := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{nil})
	if got := jpegPixels(nilPalette, white).(*image.RGBA).Pix; !bytes.Equal(got, []uint8{0, 0, 0, 0xff}) {
		t.Fatalf("nil color flattened to %v, want opaque black", got)
	}
}

func TestErrorPresentation(t *testing.T) {
	err := &Error{Kind: KindWrite, Format: decode.FormatJPEG, Err: errors.New("disk full")}
	if got := err.Error(); got != "encode JPEG: disk full" {
		t.Fatalf("Error() = %q", got)
	}
	for kind, want := range map[Kind]string{KindInvalid: "invalid", KindWrite: "write", KindFailed: "failed", Kind(9): "Kind(9)"} {
		if got := kind.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", kind, got, want)
		}
	}
}
