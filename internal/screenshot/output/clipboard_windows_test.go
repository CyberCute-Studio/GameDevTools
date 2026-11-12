//go:build windows

package output

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
)

// clipboardMu-free note: clipboard tests are intentionally not parallel.
// The clipboard is a single shared desktop resource.

var procGetDesktopWindow = user32.NewProc("GetDesktopWindow")

// holdClipboard opens the clipboard and keeps it open until the returned
// function runs, forcing every other window's OpenClipboard to fail. It
// simulates a clipboard held by another application (AC-S09). The open is
// associated with the desktop window: opening with a NULL handle only
// associates with the calling process and would not conflict with the
// NULL-handle open Deliver performs.
func holdClipboard(t *testing.T) (release func()) {
	t.Helper()
	desktop, _, _ := procGetDesktopWindow.Call()
	locked := make(chan struct{})
	releaseChan := make(chan struct{})
	done := make(chan struct{})
	go func() {
		// OpenClipboard associates with the calling OS thread, so the
		// goroutine holding the clipboard must stay on one thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		r, _, err := procOpenClipboard.Call(desktop)
		if r == 0 {
			close(locked)
			t.Errorf("hold clipboard: OpenClipboard: %v", callError(err))
			close(done)
			return
		}
		close(locked)
		<-releaseChan
		procCloseClipboard.Call()
		close(done)
	}()
	<-locked
	return func() {
		close(releaseChan)
		<-done
	}
}

func emptyClipboardForTest(t *testing.T) {
	t.Helper()
	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		t.Fatalf("OpenClipboard: %v", callError(err))
	}
	defer procCloseClipboard.Call()
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		t.Fatalf("EmptyClipboard: %v", callError(err))
	}
}

func TestSetClipboardImageRoundTrip(t *testing.T) {
	img := sampleImage(5, 3)
	if err := setClipboardImage(0, img); err != nil {
		t.Fatalf("setClipboardImage: %v", err)
	}
	width, height, err := clipboardImageSize()
	if err != nil {
		t.Fatalf("read clipboard image size: %v", err)
	}
	if width != 5 || height != 3 {
		t.Fatalf("clipboard image size = %dx%d, want 5x3", width, height)
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 5; x++ {
			r, g, b, a, err := clipboardPixel(x, y)
			if err != nil {
				t.Fatalf("read clipboard pixel (%d,%d): %v", x, y, err)
			}
			want := img.RGBAAt(x, y)
			if r != want.R || g != want.G || b != want.B || a != 255 {
				t.Fatalf("clipboard pixel (%d,%d) = (%d,%d,%d,%d), want (%d,%d,%d,255)",
					x, y, r, g, b, a, want.R, want.G, want.B)
			}
		}
	}
}

func TestDeliverClipboardAndSave(t *testing.T) {
	folder := t.TempDir()
	result := Deliver(sampleImage(4, 2), optionsFor(folder, screenshot.FormatPNG))
	if !result.OK() {
		t.Fatalf("Deliver = %+v, want both destinations successful", result)
	}
	if result.Path == "" {
		t.Fatal("Path is empty after a successful save")
	}
	if got := decodeFile(t, result.Path).Bounds().Size(); got != (image.Point{X: 4, Y: 2}) {
		t.Fatalf("saved dimensions = %v, want 4x2", got)
	}
	width, height, err := clipboardImageSize()
	if err != nil {
		t.Fatalf("read clipboard image size: %v", err)
	}
	if width != 4 || height != 2 {
		t.Fatalf("clipboard image size = %dx%d, want 4x2", width, height)
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
	result := Deliver(img, optionsFor(blocker, screenshot.FormatPNG))
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
	r, g, b, a, err := clipboardPixel(1, 1)
	if err != nil {
		t.Fatalf("read clipboard pixel: %v", err)
	}
	want := img.RGBAAt(1, 1)
	if r != want.R || g != want.G || b != want.B || a != 255 {
		t.Fatalf("clipboard pixel = (%d,%d,%d,%d), want (%d,%d,%d,255)",
			r, g, b, a, want.R, want.G, want.B)
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
	emptyClipboardForTest(t)
	folder := t.TempDir()
	options := optionsFor(folder, screenshot.FormatJPEG)
	options.JPEGQuality = screenshot.MinJPEGQuality
	img := sampleImage(6, 6)
	result := Deliver(img, options)
	if !result.OK() {
		t.Fatalf("Deliver = %+v, want both destinations successful", result)
	}
	if filepath.Ext(result.Path) != ".jpg" {
		t.Fatalf("saved file = %q, want a .jpg file", result.Path)
	}
	decodeFile(t, result.Path)
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			r, g, b, a, err := clipboardPixel(x, y)
			if err != nil {
				t.Fatalf("read clipboard pixel (%d,%d): %v", x, y, err)
			}
			want := img.RGBAAt(x, y)
			if r != want.R || g != want.G || b != want.B || a != 255 {
				t.Fatalf("clipboard pixel (%d,%d) = (%d,%d,%d,%d), want exact captured pixels (%d,%d,%d,255)",
					x, y, r, g, b, a, want.R, want.G, want.B)
			}
		}
	}
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
