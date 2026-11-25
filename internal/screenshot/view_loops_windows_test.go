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
