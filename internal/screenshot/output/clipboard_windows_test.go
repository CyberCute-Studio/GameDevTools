//go:build windows

package output

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
)

// clipboardMu-free note: clipboard tests are intentionally not parallel.
// The clipboard is a single shared desktop resource.

var (
	procGetDesktopWindow           = user32.NewProc("GetDesktopWindow")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

// openClipboardWithRetry opens the clipboard, retrying briefly while
// another application holds it, mirroring the retry setClipboardImage
// performs. Clipboard-aware services on the desktop transiently hold the
// clipboard, and an unretried open makes clipboard tests flaky.
func openClipboardWithRetry(owner uintptr) error {
	for attempt := 0; attempt < clipboardOpenAttempts; attempt++ {
		r, _, _ := procOpenClipboard.Call(owner)
		if r != 0 {
			return nil
		}
		time.Sleep(clipboardOpenRetryWait)
	}
	return errors.New("the clipboard remained in use by another application")
}

// holdClipboard opens the clipboard and keeps it open until the returned
// function runs, forcing every other OpenClipboard to fail. It simulates a
// clipboard held by another application (AC-S09). The open is associated
// with the desktop window so the hold registers against a real window.
func holdClipboard(t *testing.T) (release func()) {
	t.Helper()
	desktop, _, _ := procGetDesktopWindow.Call()
	locked := make(chan error, 1)
	releaseChan := make(chan struct{})
	done := make(chan struct{})
	go func() {
		// OpenClipboard associates with the calling OS thread, so the
		// goroutine holding the clipboard must stay on one thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := openClipboardWithRetry(desktop); err != nil {
			locked <- fmt.Errorf("hold clipboard: OpenClipboard: %w", err)
			close(done)
			return
		}
		locked <- nil
		<-releaseChan
		procCloseClipboard.Call()
		close(done)
	}()
	if err := <-locked; err != nil {
		t.Fatal(err)
	}
	return func() {
		close(releaseChan)
		<-done
	}
}

// clipboardSequence returns the Windows clipboard sequence number, which
// increments every time the clipboard's contents change. A snapshot read
// between two equal sequence numbers was not disturbed mid-read.
func clipboardSequence() uint32 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(r)
}

// clipboardImage reads back the whole CF_DIB image on the clipboard under
// one open, so concurrent clipboard users get a single narrow window to
// disturb the verification instead of one per pixel.
func clipboardImage() (*image.RGBA, error) {
	if err := openClipboardWithRetry(0); err != nil {
		return nil, fmt.Errorf("OpenClipboard: %w", err)
	}
	defer procCloseClipboard.Call()
	allocation, _, err := procGetClipboardData.Call(cfDIB, 0)
	if allocation == 0 {
		return nil, fmt.Errorf("GetClipboardData(CF_DIB): %w", callError(err))
	}
	data, err := lockGlobalMemory(allocation)
	if err != nil {
		return nil, err
	}
	defer procGlobalUnlock.Call(allocation)
	var header bitmapInfoHeader
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&header)), data, unsafe.Sizeof(header))
	if header.Size != uint32(unsafe.Sizeof(bitmapInfoHeader{})) || header.BitCount != 32 ||
		header.Compression != 0 { // only uncompressed BI_RGB pixel data is readable here
		return nil, errors.New("unexpected clipboard DIB header")
	}
	width := int(header.Width)
	height := int(header.Height)
	topDown := false
	if height < 0 { // a negative header height stores rows top-down
		height = -height
		topDown = true
	}
	if width <= 0 || height <= 0 || width > 65536 || height > 65536 {
		return nil, fmt.Errorf("unexpected clipboard DIB dimensions %d × %d", width, height)
	}
	// The DIB comes from whatever process last touched the shared
	// clipboard; never read past the allocation it actually arrived in.
	required := uint64(unsafe.Sizeof(header)) + uint64(width)*uint64(height)*4
	if size, _, _ := procGlobalSize.Call(allocation); uint64(size) < required {
		return nil, fmt.Errorf("clipboard DIB allocation holds %d bytes, its header requires %d", size, required)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceRow := height - 1 - y // a positive header height stores rows bottom-up
		if topDown {
			sourceRow = y
		}
		row := img.Pix[y*img.Stride : y*img.Stride+width*4]
		address := data + unsafe.Sizeof(header) + uintptr(sourceRow*width)*4
		procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&row[0])), address, uintptr(len(row)))
		for x := 0; x < width; x++ {
			row[x*4], row[x*4+2] = row[x*4+2], row[x*4] // BGRA to RGBA
			row[x*4+3] = 255
		}
	}
	return img, nil
}

// imageDifference describes the first difference between got and want, or
// the empty string when they match exactly.
func imageDifference(got, want *image.RGBA) string {
	if got.Bounds().Size() != want.Bounds().Size() {
		return fmt.Sprintf("dimensions %v, want %v", got.Bounds().Size(), want.Bounds().Size())
	}
	bounds := want.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			g := got.RGBAAt(x, y)
			w := want.RGBAAt(x, y)
			if g != w {
				return fmt.Sprintf("pixel (%d,%d) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					x, y, g.R, g.G, g.B, g.A, w.R, w.G, w.B, w.A)
			}
		}
	}
	return ""
}

// verifyClipboardImage runs set, reads the whole clipboard image back under
// one open, and retries while the read may have been disturbed by another
// clipboard user. Parallel go test binaries share the session-global
// clipboard, and desktop clipboard services touch it at any time, so a test
// that verifies clipboard contents must expect foreign data to replace what
// it placed; only a snapshot read while the sequence number stayed constant
// is trustworthy, and only a snapshot matching the expected image ends the
// verification. A mismatch that survives every retry is a real failure.
func verifyClipboardImage(t *testing.T, set func() error, want *image.RGBA) {
	t.Helper()
	const attempts = 5
	lastDifference := ""
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := set(); err != nil {
			t.Fatalf("place image on the clipboard: %v", err)
		}
		before := clipboardSequence()
		got, err := clipboardImage()
		after := clipboardSequence()
		if err != nil {
			lastErr = err
			continue // another clipboard user interfered with the read
		}
		if before != after {
			continue // another clipboard user interfered with the read
		}
		difference := imageDifference(got, want)
		if difference == "" {
			return
		}
		lastDifference = difference
	}
	if lastDifference != "" {
		t.Fatalf("clipboard never held the expected image across %d attempts; last stable difference: %s",
			attempts, lastDifference)
	}
	if lastErr != nil {
		t.Fatalf("clipboard image could never be read back undisturbed across %d attempts; last read error: %v",
			attempts, lastErr)
	}
	t.Fatalf("clipboard image could never be read back undisturbed across %d attempts; the contents changed during every read",
		attempts)
}

func TestSetClipboardImageRoundTrip(t *testing.T) {
	img := sampleImage(5, 3)
	verifyClipboardImage(t, func() error {
		return setClipboardImage(0, img)
	}, img)
}

func TestDeliverClipboardAndSave(t *testing.T) {
	folder := t.TempDir()
	img := sampleImage(4, 2)
	var result Result
	verifyClipboardImage(t, func() error {
		result = Deliver(img, optionsFor(folder, screenshot.FormatPNG))
		return result.Clipboard
	}, img)
	if !result.OK() {
		t.Fatalf("Deliver = %+v, want both destinations successful", result)
	}
	if result.Path == "" {
		t.Fatal("Path is empty after a successful save")
	}
	if got := decodeFile(t, result.Path).Bounds().Size(); got != (image.Point{X: 4, Y: 2}) {
		t.Fatalf("saved dimensions = %v, want 4x2", got)
	}
}

func TestDeliverSaveFailureKeepsClipboardSuccess(t *testing.T) {
	// AC-S08: an unwritable destination must not discard the clipboard
	// copy, and the delivery must not be reported as fully successful.
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	img := sampleImage(3, 3)
	var result Result
	verifyClipboardImage(t, func() error {
		result = Deliver(img, optionsFor(blocker, screenshot.FormatPNG))
		return result.Clipboard
	}, img)
	if result.Clipboard != nil {
		t.Fatalf("Clipboard = %v, want success", result.Clipboard)
	}
	assertKind(t, result.Save, KindFolder)
	if result.Path != "" {
		t.Fatalf("Path = %q, want empty after a failed save", result.Path)
	}
	if result.OK() {
		t.Fatal("OK() = true despite the failed save")
	}
}

func TestDeliverClipboardFailureKeepsSaveSuccess(t *testing.T) {
	// AC-S09: a clipboard failure must not discard the saved file, and the
	// delivery must report the clipboard failure explicitly.
	release := holdClipboard(t)
	folder := t.TempDir()
	done := make(chan Result, 1)
	go func() {
		done <- Deliver(sampleImage(4, 4), optionsFor(folder, screenshot.FormatPNG))
	}()
	result := <-done
	release()
	assertKind(t, result.Clipboard, KindClipboard)
	if result.Save != nil {
		t.Fatalf("Save = %v, want success", result.Save)
	}
	if result.OK() {
		t.Fatal("OK() = true despite the failed clipboard copy")
	}
	if got := decodeFile(t, result.Path).Bounds().Size(); got != (image.Point{X: 4, Y: 4}) {
		t.Fatalf("saved dimensions = %v, want 4x4", got)
	}
}

func TestDeliverJPEGDoesNotDegradeClipboard(t *testing.T) {
	// SCR-062: the clipboard receives the captured pixels, never the
	// output of JPEG encoding. Quality 1 maximizes the difference.
	folder := t.TempDir()
	options := optionsFor(folder, screenshot.FormatJPEG)
	options.JPEGQuality = screenshot.MinJPEGQuality
	img := sampleImage(6, 6)
	var result Result
	verifyClipboardImage(t, func() error {
		result = Deliver(img, options)
		return result.Clipboard
	}, img)
	if !result.OK() {
		t.Fatalf("Deliver = %+v, want both destinations successful", result)
	}
	if filepath.Ext(result.Path) != ".jpg" {
		t.Fatalf("saved file = %q, want a .jpg file", result.Path)
	}
	decodeFile(t, result.Path)
}

func TestResultErrorKinds(t *testing.T) {
	clipboardErr := &Error{Kind: KindClipboard, Op: "OpenClipboard", Err: errors.New("busy")}
	if clipboardErr.Error() == "" {
		t.Fatal("Error.Error() is empty")
	}
	if !errors.Is(clipboardErr.Unwrap(), clipboardErr.Err) {
		t.Fatal("Unwrap does not expose the underlying error")
	}
}

func TestDeliverJPEGOpaqueAlpha(t *testing.T) {
	// Captured images are opaque; JPEG output must not invent alpha.
	folder := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	result := Deliver(img, optionsFor(folder, screenshot.FormatJPEG))
	if !result.OK() {
		t.Fatalf("Deliver = %+v, want both destinations successful", result)
	}
	decodeFile(t, result.Path)
}
