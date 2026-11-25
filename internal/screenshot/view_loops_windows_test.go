//go:build windows

package screenshot

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procViewTestCreatePopupMenu  = testUser32.NewProc("CreatePopupMenu")
	procViewTestAppendMenuW      = testUser32.NewProc("AppendMenuW")
	procViewTestDestroyMenu      = testUser32.NewProc("DestroyMenu")
	procViewTestTrackPopupMenu   = testUser32.NewProc("TrackPopupMenu")
	procViewTestEndMenu          = testUser32.NewProc("EndMenu")
	procViewTestSetTimer         = testUser32.NewProc("SetTimer")
	procViewTestKillTimer        = testUser32.NewProc("KillTimer")
	procViewTestSendMessageW     = testUser32.NewProc("SendMessageW")
	procViewTestPostMessageW     = testUser32.NewProc("PostMessageW")
	procViewTestGetGUIThreadInfo = testUser32.NewProc("GetGUIThreadInfo")
	procViewTestSetActiveWindow  = testUser32.NewProc("SetActiveWindow")
	procViewTestGetActiveWindow  = testUser32.NewProc("GetActiveWindow")
	procViewTestReleaseCapture   = testUser32.NewProc("ReleaseCapture")
)

// inLoopTimer is the timer that runs a test's step inside a modal loop: the
// loop dispatches WM_TIMER, as it dispatches WM_HOTKEY.
const inLoopTimer = 0x5eed

// runInsideModalLoop runs step once the GUI thread is inside the modal loop
// enter opens, then ends that loop with end. The step runs from a timer that
// the loop dispatches, as it dispatches the shortcut's WM_HOTKEY, and enter
// returns only once the loop has ended. It reports whether the step ran.
func runInsideModalLoop(t *testing.T, window windows.Handle, enter func(), step func(), end func()) bool {
	t.Helper()
	ran := false
	callback := windows.NewCallback(func(hwnd, message, id, tick uintptr) uintptr {
		procViewTestKillTimer.Call(hwnd, id)
		if ran {
			return 0
		}
		ran = true
		step()
		end()
		return 0
	})
	if set, _, err := procViewTestSetTimer.Call(uintptr(window), inLoopTimer, 20, callback); set == 0 {
		t.Fatalf("SetTimer: %v", err)
	}
	// A loop that never starts, or never dispatches the timer, must not hold
	// the test; the watchdog ends it from another thread.
	watchdog := time.AfterFunc(5*time.Second, func() {
		const wmCancelMode, wmKeyDown, vkEscape = 0x001f, 0x0100, 0x1b
		procViewTestPostMessageW.Call(uintptr(window), wmKeyDown, vkEscape, 0)
		procViewTestSendMessageW.Call(uintptr(window), wmCancelMode, 0, 0)
	})
	defer watchdog.Stop()
	enter()
	procViewTestKillTimer.Call(uintptr(window), inLoopTimer)
	return ran
}

// TestShortcutCapturesNothingWithAMenuOpen is the shortcut pressed while a
// menu of the application is open, as the main window's system menu is after
// Alt+Space: the menu's own loop delivers WM_HOTKEY, but hiding the main window
// would leave the menu in the image (SCR-044), and that loop does not run the
// overlay's keyboard handling, so Escape would close the menu rather than
// cancel a selection (SCR-052). Nothing starts, and the shortcut works again
// once the menu has closed.
func TestShortcutCapturesNothingWithAMenuOpen(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	captures := 0
	view.capture = func(CaptureRequest) CaptureResult {
		captures++
		return CaptureResult{Path: `C:\Shots\one.png`}
	}
	beeps := 0
	view.beep = func() { beeps++ }
	status := view.status.Text()

	menu, _, err := procViewTestCreatePopupMenu.Call()
	if menu == 0 {
		t.Fatalf("CreatePopupMenu: %v", err)
	}
	t.Cleanup(func() { procViewTestDestroyMenu.Call(menu) })
	const mfString = 0
	label, _ := windows.UTF16PtrFromString("Test item")
	procViewTestAppendMenuW.Call(menu, mfString, 1, uintptr(unsafe.Pointer(label)))

	inMenu, attempts, inProgress := false, uint64(0), false
	ran := runInsideModalLoop(t, view.parent, func() {
		const tpmReturnCmd, tpmNoNotify = 0x0100, 0x0080
		procViewTestTrackPopupMenu.Call(menu, tpmReturnCmd|tpmNoNotify, 0, 0, 0, uintptr(view.parent), 0)
	}, func() {
		inMenu = guiThreadFlags()&guiInMenuMode != 0
		view.NativeHotkey(globalShortcutHotkeyID)
		attempts, inProgress = view.captureAttempts, view.inProgress
	}, func() { procViewTestEndMenu.Call() })
	if !ran {
		t.Skip("the menu loop did not start on this desktop")
	}
	if !inMenu {
		t.Fatal("the step did not run inside the menu loop")
	}
	if attempts != 0 || inProgress || card.suspended {
		t.Fatalf("a capture started with a menu open: attempts %d, in progress %v, card suspended %v", attempts, inProgress, card.suspended)
	}
	if beeps != 1 {
		t.Fatalf("the refused shortcut beeped %d times, want 1", beeps)
	}
	if len(card.reports) != 0 || view.status.Text() != status {
		t.Fatalf("the refused shortcut changed the card %+v or the status %q", card.reports, view.status.Text())
	}

	view.NativeHotkey(globalShortcutHotkeyID)
	dispatcher.runNext(t)
	if captures != 1 || beeps != 1 {
		t.Fatalf("once the menu closed: %d captures, %d beeps, want 1 and 1", captures, beeps)
	}
}

// TestShortcutCapturesNothingDuringAMoveOrSize is the shortcut pressed while
// the main window is moved or sized, with the keyboard after Alt+Space or by
// a drag: the move's own loop delivers WM_HOTKEY, and hiding the window it is
// moving from under it, or opening a selection whose Escape that loop would
// take, is refused as with a menu. The move is reported by a stand-in: a real
// keyboard move puts the pointer on the window and leaves it there, which a
// test must not do to the desktop it runs on. GetGUIThreadInfo reported a
// real one as GUI_INMOVESIZE when it was confirmed, and the menu test reads
// the same flags through the real inMoveOrMenu.
func TestShortcutCapturesNothingDuringAMoveOrSize(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	captures := 0
	view.capture = func(CaptureRequest) CaptureResult {
		captures++
		return CaptureResult{Path: `C:\Shots\one.png`}
	}
	beeps := 0
	view.beep = func() { beeps++ }
	moving := true
	view.inMoveOrMenu = func() bool { return moving }

	view.NativeHotkey(globalShortcutHotkeyID)
	if view.captureAttempts != 0 || view.inProgress || card.suspended || beeps != 1 {
		t.Fatalf("during a move: attempts %d, in progress %v, card suspended %v, beeps %d", view.captureAttempts, view.inProgress, card.suspended, beeps)
	}

	moving = false
	view.NativeHotkey(globalShortcutHotkeyID)
	dispatcher.runNext(t)
	if captures != 1 || beeps != 1 {
		t.Fatalf("once the move ended: %d captures, %d beeps, want 1 and 1", captures, beeps)
	}
}

// TestShortcutEndsAMenuModeThatShowsNoMenu is the shortcut pressed after Alt
// was pressed and released on its own in the main window, which has no menu
// bar: Windows then keeps the window in menu mode, with its system menu
// selected but nothing on screen, until another key ends it. Nothing of it can
// be in the image, so the capture starts without a beep, and the menu mode is
// ended, so that its loop cannot take a selection's keys (SCR-052). The test
// sends the SC_KEYMENU that DefWindowProc sends for that Alt, as no test sends
// real input, and makes the test window active, as Windows enters the mode
// only for the active window.
func TestShortcutEndsAMenuModeThatShowsNoMenu(t *testing.T) {
	view, card, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	captures := 0
	view.capture = func(CaptureRequest) CaptureResult {
		captures++
		return CaptureResult{Path: `C:\Shots\one.png`}
	}
	beeps := 0
	view.beep = func() { beeps++ }
	showActiveOffScreen(t, view.parent)

	inMenu, shown, attempts, inProgress := false, false, uint64(0), false
	started := time.Now()
	ran := runInsideModalLoop(t, view.parent, func() {
		const wmSysCommand, scKeyMenu = 0x0112, 0xf100
		procViewTestSendMessageW.Call(uintptr(view.parent), wmSysCommand, scKeyMenu, 0)
	}, func() {
		inMenu = guiThreadFlags()&guiInMenuMode != 0
		shown = threadShowsMenu(uintptr(windows.GetCurrentThreadId()))
		view.NativeHotkey(globalShortcutHotkeyID)
		attempts, inProgress = view.captureAttempts, view.inProgress
	}, func() {})
	// runInsideModalLoop's watchdog ends a loop left running after 5 s.
	ended := time.Since(started) < 4*time.Second
	if !ran || !inMenu {
		t.Skip("Windows did not put the test window in menu mode on this desktop")
	}
	if shown {
		t.Fatal("a menu was on screen in the menu mode Alt alone leaves")
	}
	if attempts != 1 || !inProgress || !card.suspended || beeps != 0 {
		t.Fatalf("in menu mode with no menu shown: attempts %d, in progress %v, card suspended %v, beeps %d", attempts, inProgress, card.suspended, beeps)
	}
	if !ended {
		t.Fatal("the shortcut left the menu mode running")
	}
	dispatcher.runNext(t)
	if captures != 1 {
		t.Fatalf("%d captures, want 1", captures)
	}
}

// TestShortcutClosesAnOpenDropDownList is the shortcut pressed while a
// drop-down list of the main window is open, such as the display list: the
// list is a window of its own, which hiding the main window leaves on
// screen, where a Full Display image would show it (SCR-044). The shortcut
// closes it, as Windows closes it before a dialog opens, and then captures.
func TestShortcutClosesAnOpenDropDownList(t *testing.T) {
	view, _, dispatcher := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	captures := 0
	view.capture = func(CaptureRequest) CaptureResult {
		captures++
		return CaptureResult{Path: `C:\Shots\one.png`}
	}
	view.beep = func() { t.Error("the shortcut beeped with a drop-down list open") }
	showActiveOffScreen(t, view.parent)
	combo := dropDisplayList(t, view)

	view.NativeHotkey(globalShortcutHotkeyID)
	if displayListDropped(combo) {
		t.Fatal("the display list stayed open as the capture started (SCR-044)")
	}
	if view.captureAttempts != 1 || !view.inProgress {
		t.Fatalf("with a drop-down list open: attempts %d, in progress %v", view.captureAttempts, view.inProgress)
	}
	dispatcher.runNext(t)
	if captures != 1 {
		t.Fatalf("%d captures, want 1", captures)
	}
}

// The display list's drop-down messages: CB_SHOWDROPDOWN and
// CB_GETDROPPEDSTATE.
const viewTestCBShowDropDown, viewTestCBGetDroppedState = 0x014f, 0x0157

// dropDisplayList opens the view's display list, as Alt+Down does, and closes
// it when the test ends. It skips the test on a desktop where the list does
// not drop down or does not take the mouse.
func dropDisplayList(t *testing.T, view *NativeView) uintptr {
	t.Helper()
	combo := uintptr(view.displayCombo.Handle())
	procViewTestSendMessageW.Call(combo, viewTestCBShowDropDown, 1, 0)
	t.Cleanup(func() { procViewTestSendMessageW.Call(combo, viewTestCBShowDropDown, 0, 0) })
	if !displayListDropped(combo) {
		t.Skip("the display list did not drop down on this desktop")
	}
	if _, info, _ := readGUIThread(view.parent); info.handles[guiCaptureHandle] == 0 {
		t.Skip("the open display list did not take the mouse on this desktop")
	}
	return combo
}

// displayListDropped reports whether combo's list is open.
func displayListDropped(combo uintptr) bool {
	dropped, _, _ := procViewTestSendMessageW.Call(combo, viewTestCBGetDroppedState, 0, 0)
	return dropped != 0
}

// TestAnOpenDropDownListClosesOnceItLosesTheMouse pins what the shortcut's
// WM_CANCELMODE relies on to close an open drop-down list: the list holds the
// mouse for as long as it is open and closes once it loses it, so no list can
// stay on screen that the window with the mouse capture does not name.
func TestAnOpenDropDownListClosesOnceItLosesTheMouse(t *testing.T) {
	view, _, _ := newCardTestView(t)
	showActiveOffScreen(t, view.parent)
	combo := dropDisplayList(t, view)
	procViewTestReleaseCapture.Call()
	if displayListDropped(combo) {
		t.Fatal("the display list stayed open once it lost the mouse, where WM_CANCELMODE to the window with the mouse would not close it")
	}
}

// TestARefusedShortcutLeavesADropDownListOpen is the shortcut pressed with the
// display list open while the JPEG quality text is invalid: startCapture
// refuses the capture, so the list stays open, as the menu mode Alt alone
// leaves would, rather than close for a capture that never starts.
func TestARefusedShortcutLeavesADropDownListOpen(t *testing.T) {
	view, _, _ := newCardTestView(t)
	view.NativeCommand(optionsCommandBase+modeFullCommand, 0)
	view.capture = func(CaptureRequest) CaptureResult {
		t.Error("a refused shortcut captured")
		return CaptureResult{}
	}
	view.beep = func() {}
	selectJPEG(view)
	view.quality.Edit.SetText("abc")
	showActiveOffScreen(t, view.parent)
	combo := dropDisplayList(t, view)

	view.NativeHotkey(globalShortcutHotkeyID)
	if view.captureAttempts != 1 || view.inProgress {
		t.Fatalf("with invalid quality text: attempts %d, in progress %v, want one refused attempt", view.captureAttempts, view.inProgress)
	}
	if !displayListDropped(combo) {
		t.Fatal("a shortcut refused for its JPEG quality closed the display list")
	}
}

// TestMoveOrMenuReadsTheGUIThreadFlags pins the GetGUIThreadInfo flags that
// suspend the shortcut, by the values winuser.h gives them, as no test enters
// a real move or size loop: a real keyboard move puts the pointer on the
// window. A move or size suspends it, and so does menu mode with a menu on
// screen; menu mode with none shown, as Alt alone leaves it, does not.
func TestMoveOrMenuReadsTheGUIThreadFlags(t *testing.T) {
	const (
		caretBlinking, inMoveSize, inMenuMode = 0x01, 0x02, 0x04
		systemMenuMode, popupMenuMode         = 0x08, 0x10
	)
	for _, c := range []struct {
		name      string
		flags     uint32
		menuShown bool
		want      bool
	}{
		{"a move or size", inMoveSize, false, true},
		{"a move with the caret blinking", inMoveSize | caretBlinking, false, true},
		{"the system menu open", inMenuMode | systemMenuMode, true, true},
		{"a shortcut menu open", inMenuMode | popupMenuMode, true, true},
		{"menu mode with no menu shown", inMenuMode | systemMenuMode, false, false},
		{"a menu window of the thread outside menu mode", caretBlinking, true, false},
		{"nothing", 0, true, false},
	} {
		asked := false
		got := moveOrMenu(c.flags, func() bool {
			asked = true
			return c.menuShown
		})
		if got != c.want {
			t.Errorf("%s: moveOrMenu = %v, want %v", c.name, got, c.want)
		}
		if asked && c.flags&inMenuMode == 0 {
			t.Errorf("%s: looked for a menu window outside menu mode", c.name)
		}
	}
}

// showActiveOffScreen shows window far from every display and makes it the
// active window of its thread, as the main window is when the user presses
// a key in it. No input is sent.
func showActiveOffScreen(t *testing.T, window windows.Handle) {
	t.Helper()
	const swShowNoActivate = 4
	offScreen := int32(-20000)
	procViewTestMoveWindow.Call(uintptr(window), uintptr(uint32(offScreen)), uintptr(uint32(offScreen)), 400, 300, 0)
	procViewTestShowWindow.Call(uintptr(window), swShowNoActivate)
	procViewTestSetActiveWindow.Call(uintptr(window))
	if active, _, _ := procViewTestGetActiveWindow.Call(); windows.Handle(active) != window {
		t.Skip("the test window could not be made active on this desktop")
	}
}

// guiThreadFlags reads GetGUIThreadInfo's flags for the calling thread, which
// idThread 0 would not name: that is the foreground thread.
func guiThreadFlags() uint32 {
	info := guiThreadInfo{}
	info.size = uint32(unsafe.Sizeof(info))
	procViewTestGetGUIThreadInfo.Call(uintptr(windows.GetCurrentThreadId()), uintptr(unsafe.Pointer(&info)))
	return info.flags
}

// TestGUIThreadInfoMatchesTheWindowsStructure pins GUITHREADINFO's size, 72
// bytes on 64-bit Windows and 48 on 32-bit, as GetGUIThreadInfo refuses a
// structure of another size.
func TestGUIThreadInfoMatchesTheWindowsStructure(t *testing.T) {
	want := uintptr(72)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 48
	}
	if got := unsafe.Sizeof(guiThreadInfo{}); got != want {
		t.Fatalf("guiThreadInfo is %d bytes, want %d", got, want)
	}
}
