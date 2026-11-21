package output

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
)

// sampleImage builds an opaque image with a distinct color per row, so
// tests can detect row-orientation and channel-order mistakes.
func sampleImage(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*37 + y*11) % 256),
				G: uint8((x*13 + y*53) % 256),
				B: uint8((x*91 + y*7) % 256),
				A: 255,
			})
		}
	}
	return img
}

func optionsFor(folder string, format screenshot.ImageFormat) screenshot.CaptureOptions {
	return screenshot.CaptureOptions{
		Mode:         screenshot.ModeFreeRectangle,
		Format:       format,
		JPEGQuality:  screenshot.DefaultJPEGQuality,
		OutputFolder: folder,
		Shortcut:     screenshot.DefaultShortcut(),
	}
}

func assertKind(t *testing.T, err error, want Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want kind %v", want)
	}
	var outputErr *Error
	if !errors.As(err, &outputErr) {
		t.Fatalf("error = %T (%v), want *Error with kind %v", err, err, want)
	}
	if outputErr.Kind != want {
		t.Fatalf("error kind = %v, want %v (error: %v)", outputErr.Kind, want, err)
	}
}

func decodeFile(t *testing.T, path string) image.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode saved file: %v", err)
	}
	switch filepath.Ext(path) {
	case ".png":
		if format != "png" {
			t.Fatalf("saved %s decoded as %s, want png", path, format)
		}
	case ".jpg":
		if format != "jpeg" {
			t.Fatalf("saved %s decoded as %s, want jpeg", path, format)
		}
	default:
		t.Fatalf("saved file %s has unexpected extension", path)
	}
	return decoded
}

// tempFiles returns the leftover temporary files inside folder, which must
// always be empty after a save completes or fails (SCR-066).
func tempFiles(t *testing.T, folder string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(folder, ".gamedevtools-*.tmp"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	return matches
}

func TestEncodePNG(t *testing.T) {
	img := sampleImage(7, 5)
	var buffer bytes.Buffer
	if err := encode(&buffer, img, optionsFor(`C:\unused`, screenshot.FormatPNG)); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: 7, Y: 5}) {
		t.Fatalf("decoded dimensions = %v, want 7x5", got)
	}
	for y := 0; y < 5; y++ {
		for x := 0; x < 7; x++ {
			want := img.RGBAAt(x, y)
			r, g, b, a := decoded.At(x, y).RGBA()
			if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != 255 {
				t.Fatalf("pixel (%d,%d) = (%d,%d,%d,%d), want (%d,%d,%d,255)",
					x, y, r>>8, g>>8, b>>8, a>>8, want.R, want.G, want.B)
			}
		}
	}
}

func TestEncodeJPEG(t *testing.T) {
	img := sampleImage(9, 4)
	options := optionsFor(`C:\unused`, screenshot.FormatJPEG)
	options.JPEGQuality = screenshot.MinJPEGQuality
	var buffer bytes.Buffer
	if err := encode(&buffer, img, options); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("decode JPEG: %v", err)
	}
	if got := decoded.Bounds().Size(); got != (image.Point{X: 9, Y: 4}) {
		t.Fatalf("decoded dimensions = %v, want 9x4", got)
	}
}

func TestEncodeInvalidFormat(t *testing.T) {
	options := optionsFor(`C:\unused`, screenshot.ImageFormat("bmp"))
	assertKind(t, encode(io.Discard, sampleImage(2, 2), options), KindValidation)
}

func TestSaveWritesTimestampedFile(t *testing.T) {
	folder := t.TempDir()
	now := time.Date(2026, time.November, 12, 15, 30, 45, 123_000_000, time.Local)
	path, err := save(sampleImage(6, 3), optionsFor(folder, screenshot.FormatPNG), now)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	wantName := "Screenshot_20261112_153045123.png"
	if filepath.Base(path) != wantName {
		t.Fatalf("saved filename = %q, want %q", filepath.Base(path), wantName)
	}
	if filepath.Dir(path) != folder {
		t.Fatalf("saved folder = %q, want %q", filepath.Dir(path), folder)
	}
	if got := decodeFile(t, path).Bounds().Size(); got != (image.Point{X: 6, Y: 3}) {
		t.Fatalf("decoded dimensions = %v, want 6x3", got)
	}
	if leftovers := tempFiles(t, folder); len(leftovers) > 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestSaveCreatesMissingFolder(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "nested", "Screenshots")
	now := time.Date(2026, time.November, 12, 8, 0, 0, 0, time.Local)
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), now)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decodeFile(t, path)
}

func TestSaveAppendsNumericSuffixOnCollision(t *testing.T) {
	folder := t.TempDir()
	now := time.Date(2026, time.November, 12, 15, 30, 45, 123_000_000, time.Local)
	base := filepath.Join(folder, "Screenshot_20261112_153045123.png")
	original := []byte("do not overwrite me")
	if err := os.WriteFile(base, original, 0o644); err != nil {
		t.Fatalf("seed colliding file: %v", err)
	}
	second := filepath.Join(folder, "Screenshot_20261112_153045123_2.png")
	if err := os.WriteFile(second, []byte("second"), 0o644); err != nil {
		t.Fatalf("seed second file: %v", err)
	}

	path, err := save(sampleImage(4, 4), optionsFor(folder, screenshot.FormatPNG), now)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	want := filepath.Join(folder, "Screenshot_20261112_153045123_3.png")
	if path != want {
		t.Fatalf("saved path = %q, want %q", path, want)
	}
	contents, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("read original file: %v", err)
	}
	if !bytes.Equal(contents, original) {
		t.Fatalf("existing file was modified: %q", contents)
	}
	decodeFile(t, path)
	if leftovers := tempFiles(t, folder); len(leftovers) > 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestSaveConsecutiveSavesProduceDistinctFiles(t *testing.T) {
	folder := t.TempDir()
	now := time.Date(2026, time.November, 12, 15, 30, 45, 123_000_000, time.Local)
	options := optionsFor(folder, screenshot.FormatPNG)
	first, err := save(sampleImage(2, 2), options, now)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := save(sampleImage(2, 2), options, now)
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if first == second {
		t.Fatalf("consecutive saves produced the same path %q", first)
	}
	if !strings.HasPrefix(filepath.Base(second), strings.TrimSuffix(filepath.Base(first), ".png")+"_") {
		t.Fatalf("second save %q is not the suffixed form of %q", second, first)
	}
	decodeFile(t, first)
	decodeFile(t, second)
}

func TestSaveRejectsFolderBlockedByFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	_, err := save(sampleImage(2, 2), optionsFor(blocker, screenshot.FormatPNG), time.Now())
	assertKind(t, err, KindFolder)
}

// TestSaveRefusesAFolderWindowsReadsTwoWays pins that a folder whose last name
// ends in a space, which Windows drops there but keeps once a file follows it,
// fails before anything is created, rather than leaving a stray folder behind
// and failing the write, and that one ending in a single period, which Windows
// drops in both places, still saves.
func TestSaveRefusesAFolderWindowsReadsTwoWays(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows reads a name differently by where it falls in a path")
	}
	root := t.TempDir()
	_, err := save(sampleImage(2, 2), optionsFor(filepath.Join(root, "Shots")+" ", screenshot.FormatPNG), time.Now())
	assertKind(t, err, KindFolder)
	if !errors.Is(err, errFolderReadTwoWays) {
		t.Fatalf("save = %v, want %v", err, errFolderReadTwoWays)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("the refused save left %v (read error %v)", entries, err)
	}
	path, err := save(sampleImage(2, 2), optionsFor(filepath.Join(root, "Dotted")+".", screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save to a folder ending in a period: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "Dotted", filepath.Base(path))); err != nil || info.IsDir() {
		t.Fatalf("the saved file is not in the folder Windows resolved: %v", err)
	}
}

// TestSaveCreatesAFolderThroughANameWindowsWouldShorten pins that a missing
// folder through a name ending in a space is created under that name, with
// no stray folder without the space beside it.
func TestSaveCreatesAFolderThroughANameWindowsWouldShorten(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows reads a name differently by where it falls in a path")
	}
	root := t.TempDir()
	folder := root + `\Photos \Shots`
	path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decodeFile(t, path)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "Photos " {
		t.Fatalf("the output root holds %v, want only the folder %q", entries, "Photos ")
	}
}

// TestSaveReachesAFolderWhoseOwnNameEndsInASpaceOrPeriods pins that a real
// folder of such a name, as the picker returns for one ending in a space,
// keeps receiving captures, with no stray folder without those characters
// beside it.
func TestSaveReachesAFolderWhoseOwnNameEndsInASpaceOrPeriods(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows reads a name differently by where it falls in a path")
	}
	root := t.TempDir()
	names := []string{"Shots ", "v2.."}
	for _, name := range names {
		folder := root + `\` + name
		if err := os.Mkdir(`\\?\`+folder, 0o755); err != nil {
			t.Fatal(err)
		}
		path, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
		if err != nil {
			t.Fatalf("save to %q: %v", folder, err)
		}
		if !strings.HasPrefix(path, folder+`\`) {
			t.Fatalf("saved to %q, want a file in %q", path, folder)
		}
		decodeFile(t, path)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(names) {
		t.Fatalf("the output root holds %v, want only %q", entries, names)
	}
}

// TestSaveRefusesAFolderItWouldReachAsItsNeighbor pins that a folder whose
// name ends in a space and then a period is refused, rather than having its
// capture land in the folder of the same name without the period.
func TestSaveRefusesAFolderItWouldReachAsItsNeighbor(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows reads a name differently by where it falls in a path")
	}
	root := t.TempDir()
	for _, name := range []string{"x .", "x "} {
		if err := os.Mkdir(`\\?\`+root+`\`+name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, err := save(sampleImage(2, 2), optionsFor(root+`\x .`, screenshot.FormatPNG), time.Now())
	if !errors.Is(err, errFolderReadTwoWays) {
		t.Fatalf("save = %v, want %v", err, errFolderReadTwoWays)
	}
	for _, name := range []string{"x .", "x "} {
		if entries, err := os.ReadDir(`\\?\` + root + `\` + name); err != nil || len(entries) != 0 {
			t.Fatalf("the refused save left %v in %q (read error %v)", entries, name, err)
		}
	}
}

// TestSaveReportsAnUnavailableFolderAsUnavailable pins that a folder through a
// name ending in a space, on a drive that is gone, fails with the reason
// rather than as a path to replace, since it works again once the drive is
// back.
func TestSaveReportsAnUnavailableFolderAsUnavailable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has drive letters")
	}
	for letter := 'Z'; letter >= 'D'; letter-- {
		if _, err := os.Stat(string(letter) + `:\`); err == nil {
			continue
		}
		folder := string(letter) + `:\Art\Shots `
		_, err := save(sampleImage(2, 2), optionsFor(folder, screenshot.FormatPNG), time.Now())
		assertKind(t, err, KindFolder)
		if errors.Is(err, errFolderReadTwoWays) {
			t.Fatalf("save = %v, want the drive's own failure", err)
		}
		if text := err.Error(); strings.Contains(text, `\\?\`) {
			t.Fatalf("the failure reads %q, want the folder as written", text)
		}
		return
	}
	t.Skip("every drive letter is in use")
}

// TestSaveReportsAFolderBehindABrokenLinkAsUnavailable pins that a folder
// through a junction whose target is gone fails with that failure rather than
// as a path to replace, and that the save recreates nothing behind the link.
func TestSaveReportsAFolderBehindABrokenLinkAsUnavailable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has directory junctions")
	}
	root := t.TempDir()
	target := root + `\target`
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := root + `\link`
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("the volume cannot hold a junction: %v: %s", err, output)
	}
	t.Cleanup(func() { os.Remove(link) })
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	_, err := save(sampleImage(2, 2), optionsFor(link+`\Shots `, screenshot.FormatPNG), time.Now())
	assertKind(t, err, KindFolder)
	if errors.Is(err, errFolderReadTwoWays) {
		t.Fatalf("save = %v, want the broken link's own failure", err)
	}
	if text := err.Error(); strings.Contains(text, `\\?\`) {
		t.Fatalf("the failure reads %q, want the folder as written", text)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the junction's target after the save: %v, want it still missing", err)
	}
}

// TestSaveCreatesAMissingFolderThroughADevicePath pins that a \\.\ folder
// with no name Windows would shorten is created as written.
func TestSaveCreatesAMissingFolderThroughADevicePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has device paths")
	}
	root := t.TempDir()
	path, err := save(sampleImage(2, 2), optionsFor(`\\.\`+root+`\New\Shots`, screenshot.FormatPNG), time.Now())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	decodeFile(t, path)
	if info, err := os.Stat(filepath.Join(root, "New", "Shots", filepath.Base(path))); err != nil || info.IsDir() {
		t.Fatalf("the saved file is not in the created folder: %v", err)
	}
}

// TestSaveFolderFailureNamesTheFolderAsWritten pins that a folder created
// through its extended-length spelling is named as the user knows it when
// that fails.
func TestSaveFolderFailureNamesTheFolderAsWritten(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has extended-length paths")
	}
	root := t.TempDir()
	if err := os.WriteFile(`\\?\`+root+`\Blocked `, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := save(sampleImage(2, 2), optionsFor(root+`\Blocked \Inner`, screenshot.FormatPNG), time.Now())
	assertKind(t, err, KindFolder)
	if text := err.Error(); strings.Contains(text, `\\?\`) || !strings.Contains(text, root+`\Blocked `) {
		t.Fatalf("the failure reads %q, want the folder as written", text)
	}
	// A folder stored with forward slashes, failing two names above its end.
	slashed := filepath.ToSlash(root) + "/Blocked "
	_, err = save(sampleImage(2, 2), optionsFor(slashed+"/In/Deep", screenshot.FormatPNG), time.Now())
	assertKind(t, err, KindFolder)
	if text := err.Error(); strings.Contains(text, `\\?\`) || !strings.Contains(text, slashed) {
		t.Fatalf("the failure reads %q, want the folder as written", text)
	}
}

func TestSaveEncodeFailureLeavesNoFiles(t *testing.T) {
	folder := t.TempDir()
	failing := func(writer io.Writer, img *image.RGBA, options screenshot.CaptureOptions) error {
		// Write a partial image before failing, as a real encoder error
		// mid-stream would.
		if _, err := writer.Write([]byte("partial")); err != nil {
			t.Fatalf("write partial data: %v", err)
		}
		return kindError(KindEncode, "encode PNG", errors.New("simulated encoder failure"))
	}
	now := time.Date(2026, time.November, 12, 15, 30, 45, 123_000_000, time.Local)
	_, err := saveWithEncoder(sampleImage(4, 4), optionsFor(folder, screenshot.FormatPNG), now, failing)
	assertKind(t, err, KindEncode)
	entries, readErr := os.ReadDir(folder)
	if readErr != nil {
		t.Fatalf("read output folder: %v", readErr)
	}
	if len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("failed save left files behind: %v", names)
	}
}

func TestDeliverRejectsInvalidRequest(t *testing.T) {
	folder := t.TempDir()
	options := optionsFor(folder, screenshot.FormatPNG)
	tests := []struct {
		name    string
		img     *image.RGBA
		options screenshot.CaptureOptions
	}{
		{name: "nil image", img: nil, options: options},
		{name: "empty image", img: image.NewRGBA(image.Rectangle{}), options: options},
		{name: "invalid options", img: sampleImage(2, 2), options: screenshot.CaptureOptions{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Deliver(test.img, test.options)
			if result.Path != "" {
				t.Fatalf("Path = %q, want empty", result.Path)
			}
			assertKind(t, result.Clipboard, KindValidation)
			assertKind(t, result.Save, KindValidation)
			if result.OK() {
				t.Fatal("OK() = true for a rejected request")
			}
		})
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("read output folder: %v", err)
	}
	if len(entries) > 0 {
		t.Fatalf("rejected request created files in %s", folder)
	}
}
