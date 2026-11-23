//go:build windows

package ui

import (
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCardTestSetActiveWindow     = user32.NewProc("SetActiveWindow")
	procCardTestGetActiveWindow     = user32.NewProc("GetActiveWindow")
	procCardTestIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procCardTestGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procCardTestGetWindowLongW      = user32.NewProc("GetWindowLongW")
	procCardTestGetCapture          = user32.NewProc("GetCapture")
	procCardTestSetWinEventHook     = user32.NewProc("SetWinEventHook")
	procCardTestUnhookWinEvent      = user32.NewProc("UnhookWinEvent")
	procCardTestPeekMessageW        = user32.NewProc("PeekMessageW")
	procCardTestDispatchMessageW    = user32.NewProc("DispatchMessageW")
)

func cardVisible(hwnd windows.Handle) bool {
	visible, _, _ := procCardTestIsWindowVisible.Call(uintptr(hwnd))
	return visible != 0
}

// newOffScreenCard places the card far from every display, so that a capture
// test running beside this one cannot see it.
func newOffScreenCard(t *testing.T, open func(string)) *ResultCard {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	card := NewResultCard(func() CardPlacement {
		return CardPlacement{Left: -22000, Top: -22000, Right: -20000, Bottom: -20000, DPI: 96}
	}, open)
	t.Cleanup(card.Destroy)
	return card
}

func TestResultCardAppearsWithoutTakingTheForeground(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "saving failed"}, "")
	if card.hwnd == 0 || !cardVisible(card.hwnd) {
		t.Fatal("the card did not appear")
	}
	// Only the card is checked: other packages' tests run beside this one
	// and move the foreground between their own windows.
	if after, _, _ := procCardTestGetForegroundWindow.Call(); windows.Handle(after) == card.hwnd {
		t.Fatal("the card took the foreground")
	}
	const gwlExStyle = ^uintptr(19) // -20 as a uintptr
	style, _, _ := procCardTestGetWindowLongW.Call(uintptr(card.hwnd), gwlExStyle)
	for name, want := range map[string]uintptr{"topmost": cardWSExTopmost, "tool window": cardWSExToolWindow, "no-activate": cardWSExNoActivate} {
		if style&want == 0 {
			t.Errorf("the card is not %s (style %#x)", name, style)
		}
	}
	if result := cardWindowProc(card.hwnd, cardWMMouseActivate, 0, 0); result != cardMANoActivate {
		t.Fatalf("WM_MOUSEACTIVATE = %d, want MA_NOACTIVATE", result)
	}
	if got := card.message.Text(); got != "Error: saving failed" {
		t.Fatalf("message = %q", got)
	}
}

func TestResultCardHidesForACaptureAndKeepsItsResults(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "first failed"}, "")
	card.Suspend()
	if cardVisible(card.hwnd) {
		t.Fatal("a suspended card stayed on screen")
	}
	card.Report(Notice{Kind: NoticeSuccess, Message: "second saved"}, `C:\Shots\two.png`)
	if cardVisible(card.hwnd) {
		t.Fatal("a result showed the card during a capture")
	}
	card.Resume()
	if !cardVisible(card.hwnd) || !strings.Contains(card.message.Text(), "second saved") {
		t.Fatalf("after Resume: visible %v, message %q", cardVisible(card.hwnd), card.message.Text())
	}
	if !strings.Contains(card.count.Text(), "1 earlier") {
		t.Fatalf("count = %q, want the failure waiting", card.count.Text())
	}

	// The success leaves by itself and the failure comes back.
	cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
	if !cardVisible(card.hwnd) || !strings.Contains(card.message.Text(), "first failed") {
		t.Fatalf("after the timer: visible %v, message %q", cardVisible(card.hwnd), card.message.Text())
	}
	cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
	if !cardVisible(card.hwnd) {
		t.Fatal("a failure left by itself")
	}
}

func TestResultCardButtons(t *testing.T) {
	var opened []string
	card := newOffScreenCard(t, func(path string) { opened = append(opened, path) })
	card.Report(Notice{Kind: NoticeWarning, Message: "saved, clipboard failed"}, `C:\Shots\one.png`)
	card.Report(Notice{Kind: NoticeError, Message: "no file"}, "")

	const wsVisibleStyle = 0x10000000
	const gwlStyle = ^uintptr(15) // -16 as a uintptr
	style, _, _ := procCardTestGetWindowLongW.Call(uintptr(card.openBtn.Handle()), gwlStyle)
	if style&wsVisibleStyle != 0 {
		t.Fatal("Open Folder is offered for a result with no file")
	}
	cardWindowProc(card.hwnd, cardWMCommand, cardDismissCommand, 0)
	style, _, _ = procCardTestGetWindowLongW.Call(uintptr(card.openBtn.Handle()), gwlStyle)
	if style&wsVisibleStyle == 0 {
		t.Fatal("Open Folder is not offered for a saved file")
	}
	cardWindowProc(card.hwnd, cardWMCommand, cardOpenCommand, 0)
	if len(opened) != 1 || opened[0] != `C:\Shots\one.png` {
		t.Fatalf("opened = %v", opened)
	}
	// A second quick click on Dismiss arrives as a double click.
	cardWindowProc(card.hwnd, cardWMCommand, cardDismissCommand|cardBNDoubleClicked<<16, 0)
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed after its last result was dismissed")
	}
}

// TestResultCardPressLeavesActivationAndFocusAlone presses the real buttons
// with another window of this thread active, as the main window is when the
// card shows over another tool: a standard button would take the focus, and
// with it the activation.
func TestResultCardPressLeavesActivationAndFocusAlone(t *testing.T) {
	var opened []string
	card := newOffScreenCard(t, func(path string) { opened = append(opened, path) })
	card.Report(Notice{Kind: NoticeWarning, Message: "saved, clipboard failed"}, `C:\Shots\one.png`)
	card.Report(Notice{Kind: NoticeError, Message: "second problem"}, `C:\Shots\two.png`)

	other, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16("STATIC"))), 0, cardWSPopup,
		uintptr(uint32(0xffffa880)), uintptr(uint32(0xffffa880)), 50, 50, 0, 0, 0, 0)
	if other == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { procDestroyWindow.Call(other) })
	procCardTestSetActiveWindow.Call(other)
	active, _, _ := procCardTestGetActiveWindow.Call()
	focus, _, _ := procGetFocus.Call()

	press := func(button *Button) {
		procSendMessageW.Call(uintptr(button.hwnd), cardWMLButtonDown, 1, 0x00050005)
		procSendMessageW.Call(uintptr(button.hwnd), cardWMLButtonUp, 0, 0x00050005)
	}
	checkFocus := func(step string) {
		t.Helper()
		if now, _, _ := procCardTestGetActiveWindow.Call(); now != active {
			t.Fatalf("%s: the active window changed from %#x to %#x", step, active, now)
		}
		if now, _, _ := procGetFocus.Call(); now != focus {
			t.Fatalf("%s: the focus changed from %#x to %#x", step, focus, now)
		}
	}

	// The pointer leaving the button ends the press, as the release may then
	// go to another application: a later release over it is no click.
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDown, 1, 0x00050005)
	if held, _, _ := procCardTestGetCapture.Call(); held != 0 {
		t.Fatalf("a press took the mouse capture for %#x", held)
	}
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMMouseLeave, 0, 0)
	if cardButtons[card.dismiss.hwnd].pressed {
		t.Fatal("the press outlived the pointer leaving the button")
	}
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonUp, 0, 0x00050005)
	checkFocus("release after leaving Dismiss")
	if card.queue.waiting() != 1 {
		t.Fatal("a release after the pointer left dismissed the problem")
	}

	press(card.openBtn)
	checkFocus("Open Folder")
	if len(opened) != 1 || opened[0] != `C:\Shots\two.png` {
		t.Fatalf("opened = %v", opened)
	}

	press(card.dismiss)
	checkFocus("Dismiss")
	if card.queue.waiting() != 0 {
		t.Fatal("Dismiss did not dismiss the problem")
	}

	// The second click of a quick pair arrives as a double click.
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDblClk, 1, 0x00050005)
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonUp, 0, 0x00050005)
	checkFocus("double click")
	if cardVisible(card.hwnd) {
		t.Fatal("a quick second Dismiss was lost")
	}
}

// TestResultCardPressEndsWhenTheResultChanges is a press on Dismiss while the
// card moves on: releasing it must not remove a problem the user never saw.
func TestResultCardPressEndsWhenTheResultChanges(t *testing.T) {
	const onButton = 0x00050005
	press := func(card *ResultCard) {
		procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDown, 1, onButton)
	}
	release := func(card *ResultCard) {
		procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonUp, 0, onButton)
	}

	t.Run("the success leaves during the press", func(t *testing.T) {
		card := newOffScreenCard(t, nil)
		card.Report(Notice{Kind: NoticeError, Message: "unread failure"}, "")
		card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
		press(card)
		cardWindowProc(card.hwnd, cardWMTimer, cardTimerID, 0)
		release(card)
		if len(card.queue.problems) != 1 {
			t.Fatalf("problems = %+v, want the failure kept", card.queue.problems)
		}
	})
	t.Run("a capture hides the card during the press", func(t *testing.T) {
		card := newOffScreenCard(t, nil)
		card.Report(Notice{Kind: NoticeError, Message: "unread failure"}, "")
		press(card)
		card.Suspend()
		release(card)
		if len(card.queue.problems) != 1 {
			t.Fatalf("problems = %+v, want the failure kept", card.queue.problems)
		}
	})
	t.Run("the same result keeps its press", func(t *testing.T) {
		card := newOffScreenCard(t, nil)
		card.Report(Notice{Kind: NoticeError, Message: "failure"}, "")
		press(card)
		card.Reposition()
		release(card)
		if len(card.queue.problems) != 0 {
			t.Fatalf("problems = %+v, want the press to dismiss it", card.queue.problems)
		}
	})
}

// TestResultCardPressAsksToHearThePointerLeave runs the real leave tracking:
// the card is off every display, so the pointer is already off the button,
// and Windows reports that at once. A malformed request would report nothing
// and leave the press held.
func TestResultCardPressAsksToHearThePointerLeave(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "unread failure"}, "")
	procSendMessageW.Call(uintptr(card.dismiss.hwnd), cardWMLButtonDown, 1, 0x00050005)
	var message [64]byte
	for deadline := time.Now().Add(2 * time.Second); cardButtons[card.dismiss.hwnd].pressed && time.Now().Before(deadline); {
		if got, _, _ := procCardTestPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 1); got != 0 {
			procCardTestDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if cardButtons[card.dismiss.hwnd].pressed {
		t.Fatal("the press was still held with the pointer off the button")
	}
	if len(card.queue.problems) != 1 {
		t.Fatalf("problems = %+v, want the failure kept", card.queue.problems)
	}
}

// TestResultCardDropsASuccessForACapture is a selection started while a
// success shows and then cancelled: the old success must not come back.
func TestResultCardDropsASuccessForACapture(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	card.Suspend()
	card.Resume()
	if cardVisible(card.hwnd) {
		t.Fatalf("the earlier success came back: %q", card.message.Text())
	}
}

// TestResultCardMoveLeavesASuccessTimeRunning is a dock sending display changes
// every few seconds: each repositions the card, and a success must still
// leave. KillTimer reports whether a timer was running.
func TestResultCardMoveLeavesASuccessTimeRunning(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running == 0 {
		t.Fatal("a success started no timer")
	}
	card.Reposition()
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running != 0 {
		t.Fatal("moving the card restarted the success's time")
	}
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running == 0 {
		t.Fatal("a new identical success started no timer")
	}
}

// TestResultCardResumeAnnouncesOnlyAChange is a problem waiting through a
// capture with no result of its own: Resume must not read it out as new, and
// a success identical to an earlier one must still start its time.
func TestResultCardResumeAnnouncesOnlyAChange(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "waiting problem"}, "")

	announcements := 0
	callback := windows.NewCallback(func(_, _, hwnd, _, _, _, _ uintptr) uintptr {
		if windows.Handle(hwnd) == card.message.hwnd {
			announcements++
		}
		return 0
	})
	hook, _, err := procCardTestSetWinEventHook.Call(eventObjectLiveRegionChanged, eventObjectLiveRegionChanged,
		0, callback, uintptr(windows.GetCurrentProcessId()), uintptr(windows.GetCurrentThreadId()), 0)
	if hook == 0 {
		t.Fatalf("SetWinEventHook: %v", err)
	}
	t.Cleanup(func() { procCardTestUnhookWinEvent.Call(hook) })
	// An out-of-context event arrives as this thread retrieves messages.
	settle := func() {
		var message [64]byte
		for deadline := time.Now().Add(150 * time.Millisecond); time.Now().Before(deadline); {
			procCardTestPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 1)
			time.Sleep(5 * time.Millisecond)
		}
	}

	card.Suspend()
	card.Resume()
	settle()
	if !cardVisible(card.hwnd) {
		t.Fatal("the problem did not come back")
	}
	if announcements != 0 {
		t.Fatalf("an unchanged problem was announced %d time(s)", announcements)
	}

	card.Suspend()
	card.Report(Notice{Kind: NoticeSuccess, Message: "saved"}, `C:\Shots\one.png`)
	card.Resume()
	settle()
	if announcements != 1 {
		t.Fatalf("a new result was announced %d time(s), want 1", announcements)
	}
	if running, _, _ := procKillTimer.Call(uintptr(card.hwnd), cardTimerID); running == 0 {
		t.Fatal("a success shown by Resume started no timer")
	}
}

func TestResultCardWithdrawsSettingProblems(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.ReportSetting(Notice{Kind: NoticeError, Message: "conflict"})
	if !cardVisible(card.hwnd) {
		t.Fatal("the setting problem did not appear")
	}
	card.WithdrawSettings()
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed after its only problem was withdrawn")
	}
}

func TestResultCardTakeHidesItAndHandsBackProblems(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")
	card.Report(Notice{Kind: NoticeError, Message: "two"}, "")
	entries := card.Take()
	if len(entries) != 2 || entries[0].Notice.Message != "one" {
		t.Fatalf("Take = %+v", entries)
	}
	if cardVisible(card.hwnd) {
		t.Fatal("the card stayed on screen after Take")
	}
}

func TestResultCardDoesNothingOnceDestroyed(t *testing.T) {
	card := newOffScreenCard(t, nil)
	card.Report(Notice{Kind: NoticeError, Message: "one"}, "")
	card.Destroy()
	card.Report(Notice{Kind: NoticeError, Message: "two"}, "")
	card.Resume()
	card.Reposition()
	if card.hwnd != 0 {
		t.Fatal("a destroyed card made a new window")
	}
	card.Destroy()
}
