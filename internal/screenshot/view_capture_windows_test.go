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
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

var procGetWindowLongW = testUser32.NewProc("GetWindowLongW")

const (
	gwlStyle  = ^uintptr(15) // -16 as a uintptr
	wsVisible = 0x10000000
)

// queuedDispatcher is a shell dispatcher test double: posted tasks queue on
// a channel so the test decides when the completion runs, mirroring the
// GUI-thread hand-off the production dispatcher provides.
type queuedDispatcher struct {
	tasks chan func()
}

func (d *queuedDispatcher) Post(task func()) error {
	d.tasks <- task
	return nil
}

func (d *queuedDispatcher) runNext(t *testing.T) {
	t.Helper()
	select {
	case task := <-d.tasks:
		task()
	case <-time.After(5 * time.Second):
		t.Fatal("capture completion was not posted to the dispatcher")
	}
}

// pickerVisible reports the picker's WS_VISIBLE style. IsWindowVisible
// cannot be used here because the test parent window is never shown, and
// the style is what the production show/hide logic controls.
func pickerVisible(view *NativeView) bool {
	style, _, _ := procGetWindowLongW.Call(uintptr(view.displayCombo.Handle()), gwlStyle)
	return style&wsVisible != 0
}

func openFolderEnabled(view *NativeView) bool {
	enabled, _, _ := procIsWindowEnabled.Call(uintptr(view.openFolder.Handle()))
	return enabled != 0
}

func TestNativeViewDisplayPickerTracksMode(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeShow(true)

	if pickerVisible(view) {
		t.Fatal("display picker is visible outside Full Display mode")
	}
	if len(view.displayList) == 0 {
		t.Fatal("display picker was not populated")
	}
	cursor, err := display.NewMonitor().CursorDisplay()
	if err != nil {
		t.Fatalf("CursorDisplay: %v", err)
	}
	if got := view.selectedDeviceName(); got != cursor.DeviceName {
		t.Fatalf("default display = %q, want cursor display %q", got, cursor.DeviceName)
	}

	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	if !pickerVisible(view) {
		t.Fatal("display picker stayed hidden in Full Display mode")
	}
	view.NativeCommand(optionsCommandBase+modePresetCommand, 0)
	if pickerVisible(view) {
		t.Fatal("display picker stayed visible after leaving Full Display mode")
	}
}

func TestDisplayListItemDescribesDisplay(t *testing.T) {
	item := display.Display{
		Number:     2,
		DeviceName: `\\.\DISPLAY2`,
		Name:       "Dell U2415",
		Bounds:     display.Rect{Right: 1920, Bottom: 1200},
		DPIX:       144,
		DPIY:       144,
	}
	if got := displayListItem(item); got != "2 — Dell U2415 — 1920 × 1200 (150%)" {
		t.Fatalf("secondary item = %q", got)
	}
	primary := display.Display{
		Number:     1,
		DeviceName: `\\.\DISPLAY1`,
		Bounds:     display.Rect{Right: 3840, Bottom: 2160},
		Primary:    true,
		DPIX:       96,
		DPIY:       96,
	}
	if got := displayListItem(primary); got != `1 — \\.\DISPLAY1 — 3840 × 2160 (100%, primary)` {
		t.Fatalf("primary item = %q", got)
	}
}

func TestNativeViewDisplayPickerReservesLayoutHeight(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	scale := func(value int32) int32 { return value }
	measure := func(text string, width int32) int32 {
		if text == "" {
			return 0
		}
		return 120
	}

	baseline := view.NativeHeight(600, scale, measure)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	// Selecting the mode leaves a confirmation notice; clear it so the
	// measurement isolates the picker's own height.
	view.status.Clear()
	expanded := view.NativeHeight(600, scale, measure)
	if expanded-baseline != 66 {
		t.Fatalf("display picker height increase = %d, want 66", expanded-baseline)
	}
}

func TestNativeViewStartCaptureRectangleModesPending(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	view.NativeAction()
	if got := view.status.Text(); !strings.HasPrefix(got, "Information:") || !strings.Contains(got, "Full Display capture is available") {
		t.Fatalf("status for Free Rectangle start = %q", got)
	}
	view.NativeCommand(optionsCommandBase+modePresetCommand, 0)
	view.NativeAction()
	if got := view.status.Text(); !strings.HasPrefix(got, "Information:") || !strings.Contains(got, "Full Display capture is available") {
		t.Fatalf("status for Preset Rectangle start = %q", got)
	}
}

func TestNativeViewFullDisplayCaptureSucceeds(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	var request CaptureRequest
	view.capture = func(got CaptureRequest) CaptureResult {
		request = got
		return CaptureResult{Path: `C:\Screenshots\Screenshot_20261112_101500000.png`}
	}

	view.NativeAction()
	dispatcher.runNext(t)

	if request.Window != uintptr(parent) {
		t.Fatalf("capture window = %d, want shell window %d", request.Window, parent)
	}
	if request.DeviceName != view.selectedDeviceName() || request.DeviceName == "" {
		t.Fatalf("capture display = %q, picker selection %q", request.DeviceName, view.selectedDeviceName())
	}
	if request.Options.Mode != ModeFullDisplay {
		t.Fatalf("capture options mode = %q", request.Options.Mode)
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") ||
		!strings.Contains(got, `C:\Screenshots\Screenshot_20261112_101500000.png`) {
		t.Fatalf("status after successful capture = %q", got)
	}
	if !openFolderEnabled(view) {
		t.Fatal("Open Folder stayed disabled after a successful save")
	}
	if view.inProgress {
		t.Fatal("capture stayed marked in progress after completion")
	}
}

func TestNativeViewCaptureRejectsConcurrentStart(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	started := make(chan struct{})
	release := make(chan struct{})
	view.capture = func(CaptureRequest) CaptureResult {
		close(started)
		<-release
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not start")
	}
	view.NativeAction()
	if got := view.status.Text(); !strings.Contains(got, "already in progress") {
		t.Fatalf("status for concurrent start = %q", got)
	}
	close(release)
	dispatcher.runNext(t)
	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") {
		t.Fatalf("status after released capture = %q", got)
	}
}

func TestNativeViewCaptureFailurePresentations(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	view.finishCapture(CaptureResult{Err: errors.New("display disconnected")})
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "display disconnected") {
		t.Fatalf("status for failed capture = %q", got)
	}
	if openFolderEnabled(view) {
		t.Fatal("Open Folder enabled after a failed capture without a saved file")
	}

	view.finishCapture(CaptureResult{Save: errors.New("disk full")})
	if got := view.status.Text(); !strings.HasPrefix(got, "Warning:") ||
		!strings.Contains(got, "copied to the clipboard") || !strings.Contains(got, "disk full") {
		t.Fatalf("status for failed save = %q", got)
	}

	view.finishCapture(CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`, Clipboard: errors.New("clipboard busy")})
	if got := view.status.Text(); !strings.HasPrefix(got, "Warning:") ||
		!strings.Contains(got, `C:\Screenshots\Screenshot_1.png`) || !strings.Contains(got, "clipboard busy") {
		t.Fatalf("status for failed clipboard copy = %q", got)
	}
	if !openFolderEnabled(view) {
		t.Fatal("Open Folder disabled although the file was saved")
	}

	view.finishCapture(CaptureResult{Save: errors.New("disk full"), Clipboard: errors.New("clipboard busy")})
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") ||
		!strings.Contains(got, "disk full") || !strings.Contains(got, "clipboard busy") {
		t.Fatalf("status for both outputs failing = %q", got)
	}
	if openFolderEnabled(view) {
		t.Fatal("Open Folder enabled without a saved file")
	}
}

func TestNativeViewOpenFolderRevealsSavedFile(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	revealed := ""
	view.reveal = func(path string) error {
		revealed = path
		return nil
	}
	view.NativeCommand(optionsCommandBase+openFolderCommand, 0)
	if revealed != "" {
		t.Fatal("Open Folder acted before any capture was saved")
	}

	view.finishCapture(CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`})
	view.NativeCommand(optionsCommandBase+openFolderCommand, 0)
	if revealed != `C:\Screenshots\Screenshot_1.png` {
		t.Fatalf("revealed path = %q", revealed)
	}

	view.reveal = func(string) error {
		return errors.New("explorer unavailable")
	}
	view.NativeCommand(optionsCommandBase+openFolderCommand, 0)
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "explorer unavailable") {
		t.Fatalf("status after failed reveal = %q", got)
	}
}

func TestNativeViewCaptureCommitsUnsavedQuality(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	// Keyboard activation of Start Capture never moves focus out of the
	// quality field, so the capture must commit the field itself.
	view.quality.Edit.SetText("80")

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	var request CaptureRequest
	view.capture = func(got CaptureRequest) CaptureResult {
		request = got
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	dispatcher.runNext(t)

	if request.Options.JPEGQuality != 80 {
		t.Fatalf("capture quality = %d, want the committed 80", request.Options.JPEGQuality)
	}
	if got := view.options.Options().JPEGQuality; got != 80 {
		t.Fatalf("persisted quality = %d, want 80", got)
	}
}

func TestNativeViewCaptureRejectsUnsavedInvalidQuality(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.quality.Edit.SetText("0")

	captureCalled := false
	view.capture = func(CaptureRequest) CaptureResult {
		captureCalled = true
		return CaptureResult{}
	}

	view.NativeAction()

	if captureCalled {
		t.Fatal("capture ran with invalid quality text")
	}
	if view.inProgress {
		t.Fatal("capture marked in progress after rejected quality")
	}
	if view.quality.Error.Text() == "" {
		t.Fatal("quality field shows no validation error")
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") {
		t.Fatalf("status for invalid quality = %q", got)
	}
}

func TestNativeViewFullDisplayModeSelectionKeepsSuccessNotice(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)

	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)

	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") || !strings.Contains(got, "Full Display") {
		t.Fatalf("status after selecting Full Display = %q", got)
	}
}

func TestNativeViewCaptureQualityCommitStaysQuiet(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.quality.Edit.SetText("80")

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	view.capture = func(CaptureRequest) CaptureResult {
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	// The capture result follows moments later, so the quality commit must
	// not flash its own success notice first.
	if got := view.status.Text(); strings.Contains(got, "JPEG quality") {
		t.Fatalf("quality commit flashed a notice before the capture: %q", got)
	}
	dispatcher.runNext(t)
}

func TestNativeViewCaptureRetriesStaleDisplayFailure(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	// A transient enumeration failure leaves displayErr set and the picker
	// empty; the display service recovering must be enough for the next
	// Start Capture, without the user toggling the capture mode.
	view.displayErr = errors.New("transient enumeration failure")
	view.displayList = nil
	view.displayCombo.Reset()

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	var request CaptureRequest
	view.capture = func(got CaptureRequest) CaptureResult {
		request = got
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	dispatcher.runNext(t)
	if request.DeviceName == "" {
		t.Fatal("capture stayed blocked behind a stale display error after the service recovered")
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Success:") {
		t.Fatalf("status after retried capture = %q", got)
	}
}

func TestNativeViewCaptureReportsUnavailableDisplayService(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.displays = nil
	view.refreshDisplays()

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	captureCalled := make(chan struct{}, 1)
	view.capture = func(CaptureRequest) CaptureResult {
		captureCalled <- struct{}{}
		return CaptureResult{}
	}

	view.NativeAction()

	select {
	case <-captureCalled:
		t.Fatal("capture ran without a display service")
	case <-time.After(200 * time.Millisecond):
	}
	if got := view.status.Text(); !strings.HasPrefix(got, "Error:") || !strings.Contains(got, "display") {
		t.Fatalf("status for unavailable display service = %q", got)
	}
}

func TestNativeViewShutdownWaitsForInflightCapture(t *testing.T) {
	parent := newViewTestParent(t)
	folder := testFolder(t)
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(t.TempDir(), "settings.json")})
	preset.RegisterValidators(store)
	value := settings.Default()
	view := newNativeViewForTest(t, store, &value, folder, parent)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)

	dispatcher := &queuedDispatcher{tasks: make(chan func(), 1)}
	view.dispatcher = dispatcher
	started := make(chan struct{})
	release := make(chan struct{})
	view.capture = func(CaptureRequest) CaptureResult {
		close(started)
		<-release
		return CaptureResult{Path: `C:\Screenshots\Screenshot_1.png`}
	}

	view.NativeAction()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not start")
	}
	done := make(chan struct{})
	go func() {
		view.NativeShutdown()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("NativeShutdown returned while the capture was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("NativeShutdown did not return after the capture finished")
	}
	dispatcher.runNext(t)
}
