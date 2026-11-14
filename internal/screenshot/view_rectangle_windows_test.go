//go:build windows

package screenshot

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

func TestNativeViewRectangleCaptureEndToEnd(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	log, native, _ := fakeOverlaySession(view)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	region := display.Rect{Left: 100, Top: 80, Right: 740, Bottom: 560}
	captureStarted := make(chan struct{})
	view.captureRectangle = func(got display.Rect, restore func(), options CaptureOptions) CaptureResult {
		close(captureStarted)
		if got != region {
			t.Errorf("captured region = %v, want %v", got, region)
		}
		// The main window must still be hidden while the pixels are read
		// (SCR-044); the session restores it only through this call.
		for _, entry := range *log {
			if entry == "restore" {
				t.Error("main window was restored before the capture read the pixels")
			}
		}
		if options.Mode != ModeFreeRectangle || options.OutputFolder != folder {
			t.Errorf("capture options = %+v", options)
		}
		restore()
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	if !view.sessions.Active() {
		t.Fatal("Free Rectangle start did not open the selection overlay")
	}
	// The native session reports the drawn selection and its Capture
	// command through the controller.
	native.onSelection(region)
	if len(native.ready) != 1 || !native.ready[0] {
		t.Fatalf("SetSelectionReady calls = %v, want [true]", native.ready)
	}
	native.onCapture()

	select {
	case <-captureStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("rectangle capture did not start")
	}
	dispatcher.runNext(t)

	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") ||
		!strings.Contains(got, "Selection captured") || !strings.Contains(got, `C:\Screenshots\Screenshot_1.png`) {
		t.Fatalf("status after rectangle capture = %q", got)
	}
	if !openFolderEnabled(view) {
		t.Fatal("Open Folder stayed disabled after a saved selection capture")
	}
	if view.inProgress {
		t.Fatal("capture stayed marked in progress after completion")
	}
	if len(*log) != 3 || (*log)[2] != "restore" {
		t.Fatalf("session call order = %v, want [hide create restore]", *log)
	}
}

func TestNativeViewRectangleCaptureFailureStillRestores(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	log, native, _ := fakeOverlaySession(view)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	view.captureRectangle = func(region display.Rect, restore func(), options CaptureOptions) CaptureResult {
		restore()
		return CaptureResult{Err: errors.New("graphics driver rejected the capture")}
	}

	view.NativeAction()
	native.onSelection(display.Rect{Left: 10, Top: 10, Right: 110, Bottom: 90})
	native.onCapture()
	dispatcher.runNext(t)

	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "graphics driver rejected the capture") {
		t.Fatalf("status after failed rectangle capture = %q", got)
	}
	if len(*log) != 3 || (*log)[2] != "restore" {
		t.Fatalf("session call order after failed capture = %v, want the window restored", *log)
	}
	if openFolderEnabled(view) {
		t.Fatal("Open Folder enabled after a failed capture without a saved file")
	}
	if view.inProgress {
		t.Fatal("capture stayed marked in progress after a failure")
	}
}

// TestNativeViewRectangleCancelProducesNoOutput is the view half of AC-S07:
// cancelling the session never reaches the capture pipeline, so no file is
// created and the clipboard stays untouched.
func TestNativeViewRectangleCancelProducesNoOutput(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	log, native, _ := fakeOverlaySession(view)

	captureCalled := false
	view.captureRectangle = func(display.Rect, func(), CaptureOptions) CaptureResult {
		captureCalled = true
		return CaptureResult{}
	}

	view.NativeAction()
	native.onSelection(display.Rect{Left: 10, Top: 10, Right: 110, Bottom: 90})
	view.sessions.Cancel()

	if captureCalled {
		t.Fatal("the capture pipeline ran for a cancelled session")
	}
	if len(*log) != 3 || (*log)[2] != "restore" {
		t.Fatalf("session call order after Cancel = %v, want [hide create restore]", *log)
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Information:") || !strings.Contains(got, "Nothing was captured") {
		t.Fatalf("status after cancel = %q", got)
	}
	if view.inProgress {
		t.Fatal("cancelled session marked a capture in progress")
	}
}

func TestNativeViewRectangleCaptureBlockedWhileBusy(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	log, native, _ := fakeOverlaySession(view)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	release := make(chan struct{})
	view.captureRectangle = func(region display.Rect, restore func(), options CaptureOptions) CaptureResult {
		<-release
		restore()
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	native.onSelection(display.Rect{Left: 10, Top: 10, Right: 110, Bottom: 90})
	native.onCapture()

	// While the rectangle capture delivers its outputs, another Start
	// Capture is rejected instead of hiding the window under the worker.
	view.NativeAction()
	if got := view.status.Text(); !strings.Contains(got, "already in progress") {
		t.Fatalf("status for capture during delivery = %q", got)
	}
	if len(*log) != 2 {
		t.Fatalf("second start changed state: %v", *log)
	}
	close(release)
	dispatcher.runNext(t)
	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") {
		t.Fatalf("status after released capture = %q", got)
	}
}

func TestNativeViewPresetRectangleOpensPresetSession(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	log, _, _ := fakeOverlaySession(view)

	var requested overlay.Mode
	view.sessions.Create = func(request overlay.CreateRequest) (overlay.NativeSession, error) {
		*log = append(*log, "create")
		requested = request.Mode
		return &fakeOverlayNative{}, nil
	}

	view.NativeCommand(optionsCommandBase+modePresetCommand, 0)
	view.NativeAction()
	if !view.sessions.Active() {
		t.Fatal("Preset Rectangle start did not open the selection overlay")
	}
	if requested != overlay.ModePresetRectangle {
		t.Fatalf("session mode = %v, want the preset mode", requested)
	}
	view.sessions.Shutdown()
}
