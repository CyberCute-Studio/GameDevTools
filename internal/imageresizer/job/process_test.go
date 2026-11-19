package job

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
)

var (
	red   = color.RGBA{R: 0xff, A: 0xff}
	green = color.RGBA{G: 0xff, A: 0xff}
	blue  = color.RGBA{B: 0xff, A: 0xff}
	white = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

func writeSource(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// halfTransparent is a 64 × 32 picture whose left half is fully transparent
// green and whose right half is opaque red.
func halfTransparent() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			c := color.NRGBA{G: 0xff}
			if x >= 32 {
				c = color.NRGBA{R: 0xff, A: 0xff}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// quadrants is a picture whose quadrants are red (top left), green (top
// right), blue (bottom left), and white (bottom right).
func quadrants(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	halfWidth, halfHeight := width/2, height/2
	draw.Draw(img, image.Rect(0, 0, halfWidth, halfHeight), image.NewUniform(red), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(halfWidth, 0, width, halfHeight), image.NewUniform(green), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, halfHeight, halfWidth, height), image.NewUniform(blue), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(halfWidth, halfHeight, width, height), image.NewUniform(white), image.Point{}, draw.Src)
	return img
}

// withOrientation inserts an APP1 EXIF segment holding the orientation
// directly after a JPEG's SOI marker.
func withOrientation(jpegData []byte, orientation uint16) []byte {
	tiff := []byte{
		'M', 'M', 0, 42, 0, 0, 0, 8, // big-endian header, IFD0 at offset 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, byte(orientation >> 8), byte(orientation), 0, 0, // Orientation, SHORT, 1 value
		0, 0, 0, 0, // no further IFDs
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := append([]byte{0xff, 0xe1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}, payload...)
	return append(append(append([]byte(nil), jpegData[:2]...), segment...), jpegData[2:]...)
}

// planOne plans one source into a new output folder and returns its item.
func planOne(t *testing.T, source string, content decode.Format, format output.Format, policy output.Collision, out string) output.Item {
	t.Helper()
	plan, err := output.Prepare(context.Background(), output.Request{
		Sources:          []output.Source{{Path: source, Format: content}},
		Folder:           out,
		Format:           format,
		Collision:        policy,
		ConfirmOverwrite: policy == output.CollisionOverwrite,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return plan.Items[0]
}

func settings(width, height int, mode preset.ResizeMode, background color.RGBA) Settings {
	return Settings{Target: resize.Target{Width: width, Height: height, Mode: mode}, JPEGQuality: 90, Background: background}
}

func decodeResult(t *testing.T, path string) *decode.Image {
	t.Helper()
	decoded, err := decode.Decode(context.Background(), path)
	if err != nil {
		t.Fatalf("decode the result %s: %v", path, err)
	}
	return decoded
}

func near(got color.Color, want color.RGBA, tolerance int) bool {
	c := color.RGBAModel.Convert(got).(color.RGBA)
	diff := func(a, b uint8) bool { return int(a)-int(b) <= tolerance && int(b)-int(a) <= tolerance }
	return diff(c.R, want.R) && diff(c.G, want.G) && diff(c.B, want.B) && diff(c.A, want.A)
}

func assertEmpty(t *testing.T, folder string) {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s holds %d entries, want none", folder, len(entries))
	}
}

// TestProcessKeepsPNGAlpha pins AC-R11 end to end: a translucent PNG resized
// to PNG keeps its alpha, at the Fit dimensions (AC-R02), and the source is
// unchanged (NFR-020).
func TestProcessKeepsPNGAlpha(t *testing.T) {
	content := encodePNG(t, halfTransparent())
	source := writeSource(t, "sprite.png", content)
	out := filepath.Join(t.TempDir(), "out")
	item := planOne(t, source, decode.FormatPNG, output.FormatKeepSource, output.CollisionRename, out)
	path, err := Process(context.Background(), item, settings(32, 32, preset.ResizeFit, white))
	if err != nil || path != filepath.Join(out, "sprite.png") {
		t.Fatalf("Process = %q, %v", path, err)
	}
	result := decodeResult(t, path)
	if result.Format != decode.FormatPNG || result.Width != 32 || result.Height != 16 || !result.HasAlpha {
		t.Fatalf("result = %+v, want a 32 × 16 PNG with alpha", result.Info)
	}
	if !near(result.Pixels.At(4, 8), color.RGBA{}, 0) || !near(result.Pixels.At(28, 8), red, 0) {
		t.Fatalf("result pixels = %v and %v", result.Pixels.At(4, 8), result.Pixels.At(28, 8))
	}
	if after, _ := os.ReadFile(source); !bytes.Equal(after, content) {
		t.Fatal("the source changed")
	}
}

// TestProcessFlattensForJPEG pins AC-R11's JPEG half: the transparent half
// takes the job's background color, and the result is a .jpg.
func TestProcessFlattensForJPEG(t *testing.T) {
	source := writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))
	out := t.TempDir()
	item := planOne(t, source, decode.FormatPNG, output.FormatJPEG, output.CollisionRename, out)
	path, err := Process(context.Background(), item, settings(64, 32, preset.ResizeStretch, blue))
	if err != nil || filepath.Base(path) != "sprite.jpg" {
		t.Fatalf("Process = %q, %v", path, err)
	}
	result := decodeResult(t, path)
	if result.Format != decode.FormatJPEG || result.Width != 64 || result.Height != 32 || result.HasAlpha {
		t.Fatalf("result = %+v, want an opaque 64 × 32 JPEG", result.Info)
	}
	if !near(result.Pixels.At(8, 16), blue, 8) || !near(result.Pixels.At(56, 16), red, 8) {
		t.Fatalf("result pixels = %v and %v, want blue and red", result.Pixels.At(8, 16), result.Pixels.At(56, 16))
	}
}

// TestProcessOutputIsUpright pins AC-R12 end to end: a JPEG whose EXIF
// orientation turns it a quarter clockwise is resized from its upright
// dimensions, and its quadrants land upright in the result, which carries no
// orientation of its own.
func TestProcessOutputIsUpright(t *testing.T) {
	// Stored 96 × 64; orientation 6 makes it 64 × 96 upright, its stored
	// left column becoming the top row.
	source := writeSource(t, "photo.jpg", withOrientation(encodeJPEG(t, quadrants(96, 64)), 6))
	out := t.TempDir()
	item := planOne(t, source, decode.FormatJPEG, output.FormatKeepSource, output.CollisionRename, out)
	path, err := Process(context.Background(), item, settings(32, 64, preset.ResizeFit, white))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	result := decodeResult(t, path)
	if result.Width != 32 || result.Height != 48 || result.Orientation != decode.OrientationNormal {
		t.Fatalf("result = %+v, want an upright 32 × 48 JPEG without orientation", result.Info)
	}
	corners := map[image.Point]color.RGBA{{8, 12}: blue, {24, 12}: red, {8, 36}: white, {24, 36}: green}
	for point, want := range corners {
		if got := result.Pixels.At(point.X, point.Y); !near(got, want, 40) {
			t.Fatalf("pixel %v = %v, want %v", point, got, want)
		}
	}
}

// TestProcessReplacesTheConfirmedFile: a confirmed Overwrite job replaces the
// existing destination with the resized result.
func TestProcessReplacesTheConfirmedFile(t *testing.T) {
	source := writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))
	out := t.TempDir()
	destination := filepath.Join(out, "sprite.png")
	if err := os.WriteFile(destination, []byte("earlier result"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := planOne(t, source, decode.FormatPNG, output.FormatKeepSource, output.CollisionOverwrite, out)
	if item.Action != output.ActionReplace {
		t.Skipf("planned %v (%v): replacing needs file identities, read on Windows only", item.Action, item.Reason)
	}
	if _, err := Process(context.Background(), item, settings(16, 16, preset.ResizeCropToFill, white)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result := decodeResult(t, destination); result.Width != 16 || result.Height != 16 {
		t.Fatalf("result = %+v", result.Info)
	}
}

// TestProcessReportsEachStepsFailure: the failing step's own error is
// returned, and nothing is left in the output folder (AC-R06, AC-R14).
func TestProcessReportsEachStepsFailure(t *testing.T) {
	valid := encodePNG(t, halfTransparent())
	cases := []struct {
		name     string
		content  []byte
		settings Settings
		check    func(t *testing.T, err error)
	}{
		{"corrupt source", []byte("\x89PNG\r\n\x1a\nnot really"), settings(8, 8, preset.ResizeFit, white), func(t *testing.T, err error) {
			var decodeErr *decode.Error
			if !errors.As(err, &decodeErr) || decodeErr.Kind != decode.KindCorrupt {
				t.Fatalf("error = %v, want a corrupt-source decode error", err)
			}
		}},
		{"invalid target", valid, settings(0, 8, preset.ResizeFit, white), func(t *testing.T, err error) {
			var resizeErr *resize.Error
			if !errors.As(err, &resizeErr) || resizeErr.Kind != resize.KindInvalid {
				t.Fatalf("error = %v, want an invalid-target resize error", err)
			}
		}},
		{"invalid quality", valid, Settings{Target: resize.Target{Width: 8, Height: 8, Mode: preset.ResizeFit}, Background: white}, func(t *testing.T, err error) {
			var encodeErr *encode.Error
			if !errors.As(err, &encodeErr) || encodeErr.Kind != encode.KindInvalid || !errors.Is(err, encode.ErrQuality) {
				t.Fatalf("error = %v, want an invalid-quality encode error", err)
			}
		}},
	}
	for _, tc := range cases {
		source := writeSource(t, "sprite.png", tc.content)
		out := t.TempDir()
		item := planOne(t, source, decode.FormatPNG, output.FormatJPEG, output.CollisionRename, out)
		path, err := Process(context.Background(), item, tc.settings)
		if path != "" {
			t.Fatalf("%s: Process saved %q", tc.name, path)
		}
		tc.check(t, err)
		assertEmpty(t, out)
	}
}

// TestProcessChecksSettingsBeforeReading: invalid settings fail before the
// source is read, so a missing source is not what is reported.
func TestProcessChecksSettingsBeforeReading(t *testing.T) {
	out := t.TempDir()
	item := planOne(t, filepath.Join(t.TempDir(), "missing.png"), decode.FormatPNG, output.FormatJPEG, output.CollisionRename, out)
	bad := settings(8, 8, preset.ResizeFit, color.RGBA{R: 0xff})
	_, err := Process(context.Background(), item, bad)
	if !errors.Is(err, encode.ErrBackground) {
		t.Fatalf("error = %v, want the background error", err)
	}
	_, err = Process(context.Background(), item, settings(8, 8, preset.ResizeFit, white))
	var decodeErr *decode.Error
	if !errors.As(err, &decodeErr) || decodeErr.Kind != decode.KindRead {
		t.Fatalf("error = %v, want the missing source's read error", err)
	}
}

// TestProcessChecksTheReplacedFileBeforeReading: a confirmed Overwrite item
// whose existing file was marked read-only after planning fails with
// ErrReadOnly before its source is read, so a missing source is not what is
// reported, and the file is kept.
func TestProcessChecksTheReplacedFileBeforeReading(t *testing.T) {
	source := writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))
	out := t.TempDir()
	destination := filepath.Join(out, "sprite.png")
	if err := os.WriteFile(destination, []byte("earlier result"), 0o644); err != nil {
		t.Fatal(err)
	}
	item := planOne(t, source, decode.FormatPNG, output.FormatKeepSource, output.CollisionOverwrite, out)
	if item.Action != output.ActionReplace {
		t.Skipf("planned %v (%v): replacing needs file identities, read on Windows only", item.Action, item.Reason)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(destination, 0o644)
	path, err := Process(context.Background(), item, settings(8, 8, preset.ResizeFit, white))
	if path != "" || !errors.Is(err, output.ErrReadOnly) {
		t.Fatalf("Process = %q, %v, want ErrReadOnly before the source is read", path, err)
	}
	if content, readErr := os.ReadFile(destination); readErr != nil || string(content) != "earlier result" {
		t.Fatalf("the read-only file holds %q, %v", content, readErr)
	}
}

// TestProcessObservesCancellation: a cancelled context fails the item with an
// error errors.Is reports as the context's, and saves nothing.
func TestProcessObservesCancellation(t *testing.T) {
	source := writeSource(t, "sprite.png", encodePNG(t, halfTransparent()))
	out := t.TempDir()
	item := planOne(t, source, decode.FormatPNG, output.FormatKeepSource, output.CollisionRename, out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path, err := Process(ctx, item, settings(8, 8, preset.ResizeFit, white))
	if path != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("Process = %q, %v, want the cancellation", path, err)
	}
	assertEmpty(t, out)
}

// TestProcessRejectsItemsThatSaveNothing: a skipped, failed, or deferred item
// is the batch's to settle, and Process refuses it before reading anything.
func TestProcessRejectsItemsThatSaveNothing(t *testing.T) {
	out := t.TempDir()
	item := planOne(t, filepath.Join(t.TempDir(), "missing.png"), decode.FormatPNG, output.FormatKeepSource, output.CollisionRename, out)
	for _, action := range []output.Action{output.ActionSkip, output.ActionFail, output.ActionDefer} {
		item.Action = action
		_, err := Process(context.Background(), item, settings(8, 8, preset.ResizeFit, white))
		var outputErr *output.Error
		if !errors.As(err, &outputErr) || outputErr.Kind != output.KindValidation || !errors.Is(err, output.ErrNotWritten) {
			t.Fatalf("%v: error = %v, want ErrNotWritten", action, err)
		}
	}
	// An item no plan gives, without a destination, is refused before its
	// source is read: a missing source would otherwise fail first.
	_, err := Process(context.Background(), output.Item{Source: item.Source}, settings(8, 8, preset.ResizeFit, white))
	var outputErr *output.Error
	if !errors.As(err, &outputErr) || outputErr.Kind != output.KindValidation {
		t.Fatalf("zero item: error = %v, want a validation failure", err)
	}
	assertEmpty(t, out)
}
