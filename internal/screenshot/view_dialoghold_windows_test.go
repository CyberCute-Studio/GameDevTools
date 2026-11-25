//go:build windows

package screenshot

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var procViewTestFindWindowW = testUser32.NewProc("FindWindowW")

// askReal asks a real ui.Confirm question about parent and reports whether
// its dialog appeared. A dialog that appears is answered No by a goroutine
// beside it, so the test never waits on a person.
func askReal(t *testing.T, parent windows.Handle, title string) (shown bool) {
	t.Helper()
	var appeared atomic.Bool
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		class, _ := windows.UTF16PtrFromString("#32770")
		name, _ := windows.UTF16PtrFromString(title)
		const wmCommand, idNo = 0x0111, 7
		for {
			if dialog, _, _ := procViewTestFindWindowW.Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(name))); dialog != 0 {
				appeared.Store(true)
				procViewTestPostMessageW.Call(dialog, wmCommand, idNo, 0)
			}
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	if ui.Confirm(parent, title, "Dialog hold test") {
		t.Errorf("the question %q answered Yes", title)
	}
	close(stop)
	<-watched
	return appeared.Load()
}

// TestNoDialogOpensUntilAFullDisplayCaptureReadsTheScreen is a dialog asked
// for in the instant after a Full Display capture starts, as a click on Add
// Images or Browse queued just before the shortcut is: the GUI thread goes on
// dispatching messages while the worker hides the window, so the dialog would
// be in the image (SCR-044). It is refused until the display is read, and
// opens while the file is written and once the capture has ended.
func TestNoDialogOpensUntilAFullDisplayCaptureReadsTheScreen(t *testing.T) {
	view, _, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	reading, read, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	view.capture = func(request CaptureRequest) CaptureResult {
		close(reading)
		<-read
		request.Captured()
		<-finish
		return CaptureResult{Path: `C:\Shots\one.png`}
	}
	view.NativeHotkey(globalShortcutHotkeyID)
	<-reading
	if askReal(t, view.parent, "GameDevTools hold: before the read") {
		t.Fatal("a dialog opened before the capture read the screen (SCR-044)")
	}
	close(read)
	deadline := time.Now().Add(5 * time.Second)
	for ui.DialogsHeld() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !askReal(t, view.parent, "GameDevTools hold: while delivering") {
		t.Fatal("a dialog was refused once the screen was read, while the file is written")
	}
	close(finish)
	dispatcher.runNext(t)
	if ui.DialogsHeld() {
		t.Fatal("the dialogs stayed held once the capture ended")
	}
}

// TestNoDialogOpensDuringASelection is a dialog asked for while the view's
// selection is on screen: its modal loop would not run the overlay's keyboard
// handling, so Escape could not cancel the selection (SCR-052), and a capture
// would put it in the image. The view's session holds the dialogs until it
// ends, as the overlay's own tests pin on every path.
func TestNoDialogOpensDuringASelection(t *testing.T) {
	view, _, _ := newCardTestView(t)
	fakeOverlaySession(view)
	view.NativeAction()
	if !view.sessions.Active() {
		t.Fatal("the session did not start")
	}
	if !ui.DialogsHeld() {
		t.Fatal("the dialogs were not held during a selection")
	}
	view.sessions.Cancel()
	if ui.DialogsHeld() {
		t.Fatal("the dialogs stayed held once the selection was cancelled")
	}
}
