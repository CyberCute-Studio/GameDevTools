//go:build windows

package screenshot

import (
	"sync"
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
	requireNoHold(t)
	view, _, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	reading := make(chan struct{})
	read, finish := closer(t), closer(t)
	view.capture = func(request CaptureRequest) CaptureResult {
		close(reading)
		<-read.done
		request.Captured()
		<-finish.done
		return CaptureResult{Path: `C:\Shots\one.png`}
	}
	view.NativeHotkey(globalShortcutHotkeyID)
	<-reading
	if askReal(t, view.parent, "GameDevTools hold: before the read") {
		t.Fatal("a dialog opened before the capture read the screen (SCR-044)")
	}
	read.close()
	deadline := time.Now().Add(5 * time.Second)
	for ui.DialogsHeld() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !askReal(t, view.parent, "GameDevTools hold: while delivering") {
		t.Fatal("a dialog was refused once the screen was read, while the file is written")
	}
	finish.close()
	dispatcher.runNext(t)
	if ui.DialogsHeld() {
		t.Fatal("the dialogs stayed held once the capture ended")
	}
}

// requireNoHold fails a dialog-hold test that starts with the dialogs held,
// which a test before it left so, rather than let it report that test's
// failure as its own.
func requireNoHold(t *testing.T) {
	t.Helper()
	if ui.DialogsHeld() {
		t.Fatal("the dialogs were already held as the test started: an earlier test left a hold")
	}
}

// onceCloser is a channel a test closes once, or its cleanup closes if the
// test failed first, so a worker waiting on it ends and releases its hold.
type onceCloser struct {
	done chan struct{}
	once sync.Once
}

func closer(t *testing.T) *onceCloser {
	c := &onceCloser{done: make(chan struct{})}
	t.Cleanup(c.close)
	return c
}

func (c *onceCloser) close() { c.once.Do(func() { close(c.done) }) }

// TestInputForTheMainWindowIsDroppedWhileDialogsAreHeld is a click or key
// press queued for the main window just before the shortcut: GetMessage
// returns the shortcut's posted WM_HOTKEY first, so the press would reach the
// window as it hides and could open a field's shortcut menu, the system menu,
// a drop-down list, a move, or an Explorer window that the image would show
// (SCR-044). While the dialogs are held, key and mouse-button input for the
// main window and its controls is dropped, a press with a beep, but for a
// modifier key or a repeat; other messages and other windows' input pass, and
// nothing is dropped once the hold is released.
func TestInputForTheMainWindowIsDroppedWhileDialogsAreHeld(t *testing.T) {
	requireNoHold(t)
	view, _, _ := newCardTestView(t)
	beeps := 0
	view.beep = func() { beeps++ }
	other := newViewTestParent(t)
	control := uintptr(view.browseButton.Handle())
	filter := func(hwnd uintptr, message uint32, wParam, lParam uintptr) bool {
		queued := struct {
			queuedMessage
			time    uint32
			pt      [2]int32
			private uint32
		}{queuedMessage: queuedMessage{hwnd: hwnd, message: message, wParam: wParam, lParam: lParam}}
		return view.NativeFilterMessage(unsafe.Pointer(&queued))
	}
	const (
		wmKeyDown, wmKeyUp, wmSysKeyDown, wmSysChar = 0x0100, 0x0101, 0x0104, 0x0106
		wmNCLButtonDown, wmNCRButtonUp              = 0x00a1, 0x00a5
		wmNCXButtonDown                             = 0x00ab
		wmMouseMove, wmRButtonDown, wmRButtonUp     = 0x0200, 0x0204, 0x0205
		wmLButtonDblClk, wmMouseWheel               = 0x0203, 0x020a
		wmXButtonDown, wmXButtonUp                  = 0x020b, 0x020c
		wmPaint, wmHotkey                           = 0x000f, 0x0312
		vkControl, vkSpace, vkApps, vkA             = 0x11, 0x20, 0x5d, 0x41
		repeat                                      = 1 << 30
	)
	if filter(control, wmRButtonUp, 0, 0) || beeps != 0 {
		t.Fatal("input was dropped with no hold")
	}

	release := ui.HoldDialogs()
	t.Cleanup(release)
	for _, c := range []struct {
		name    string
		hwnd    uintptr
		message uint32
		wParam  uintptr
		lParam  uintptr
		press   bool
	}{
		{"a right press on a field", control, wmRButtonDown, 0, 0, true},
		{"its release, which opens the shortcut menu", control, wmRButtonUp, 0, 0, false},
		{"a double click", control, wmLButtonDblClk, 0, 0, true},
		{"a press on the caption, which starts a move", uintptr(view.parent), wmNCLButtonDown, 0, 0, true},
		{"a right release on the caption, which opens the system menu", uintptr(view.parent), wmNCRButtonUp, 0, 0, false},
		{"a side-button press on the frame", uintptr(view.parent), wmNCXButtonDown, 0, 0, true},
		{"a side-button press", control, wmXButtonDown, 0, 0, true},
		{"a side-button release", control, wmXButtonUp, 0, 0, false},
		{"the menu key", control, wmKeyDown, vkApps, 0, true},
		{"Alt+Space", control, wmSysKeyDown, vkSpace, 0, true},
		{"Alt+Space's character", control, wmSysChar, ' ', 0, false},
		{"a key's release", control, wmKeyUp, vkA, 0, false},
		{"a modifier the shortcut left queued", control, wmKeyDown, vkControl, 0, false},
		{"a held key's repeat", control, wmKeyDown, vkA, repeat, false},
	} {
		before := beeps
		if !filter(c.hwnd, c.message, c.wParam, c.lParam) {
			t.Errorf("%s reached the main window while the dialogs were held", c.name)
		}
		if beeped := beeps != before; beeped != c.press {
			t.Errorf("%s: beeped %v, want %v", c.name, beeped, c.press)
		}
	}
	for _, c := range []struct {
		name    string
		hwnd    uintptr
		message uint32
	}{
		{"a pointer move", control, wmMouseMove},
		{"the wheel", control, wmMouseWheel},
		{"a paint", control, wmPaint},
		{"the shortcut", uintptr(view.parent), wmHotkey},
		{"another window's input", uintptr(other), wmRButtonUp},
		{"a thread message", 0, wmKeyDown},
	} {
		if filter(c.hwnd, c.message, 0, 0) {
			t.Errorf("%s was dropped while the dialogs were held", c.name)
		}
	}

	release()
	beeps = 0
	if filter(control, wmRButtonUp, 0, 0) || filter(control, wmKeyDown, vkApps, 0) || beeps != 0 {
		t.Fatal("input was dropped once the hold was released")
	}
}

// TestNoDialogOpensDuringASelection is a dialog asked for while the view's
// selection is on screen: its modal loop would not run the overlay's keyboard
// handling, so Escape could not cancel the selection (SCR-052), and a capture
// would put it in the image. The view's session holds the dialogs until it
// ends, as the overlay's own tests pin on every path.
func TestNoDialogOpensDuringASelection(t *testing.T) {
	requireNoHold(t)
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
